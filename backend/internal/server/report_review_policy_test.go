package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func TestIsolatedReportReviewPolicy(t *testing.T) {
	const isolation = "p3-report-review"
	a := setupConfiguredAPI(t, "", func(cfg *config.Config, _ string) {
		cfg.IAMIsolationID, cfg.Phase2ReportVerification = isolation, true
	})
	f := setupManagedAPI(t, a)
	ctx := context.Background()
	view := a.table + "_review"
	qualified := pgx.Identifier{"public", view}.Sanitize()
	if _, err := a.targetPool.Exec(ctx, "CREATE VIEW "+qualified+` AS
		SELECT * FROM (VALUES
		 ('dept-a','synthetic','red',3.5::numeric,DATE '2026-11-02',true),
		 ('dept-a','higher','red',22.5::numeric,DATE '2026-11-02',true),
		 ('dept-a','later','red',3.5::numeric,DATE '2026-11-20',true),
		 ('dept-a','other-category','blue',3.5::numeric,DATE '2026-11-02',true),
		 ('dept-a','inactive','red',3.5::numeric,DATE '2026-11-02',false),
		 ('dept-b','outside','red',3.5::numeric,DATE '2026-11-02',true)
		) v(department,title,category,amount,sale_day,active)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := a.targetPool.Exec(ctx, "DROP VIEW "+qualified); err != nil {
			t.Error(err)
		}
	})
	if _, err := a.targetPool.Exec(ctx, "GRANT SELECT ON "+qualified+" TO "+pgx.Identifier{f.username}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	report, draft := f.draft(`SELECT "title" FROM ` + qualified)
	createMember := func(name, role string) (int64, string) {
		t.Helper()
		const fixturePassword = "TestPassword123!"
		out := a.call("POST", "/users", a.admin, map[string]any{"username": name, "password": fixturePassword, "role": role}, 201)
		login := a.call("POST", "/auth/login", "", map[string]any{"username": name, "password": fixturePassword}, 200)
		return int64(out["id"].(float64)), login["access_token"].(string)
	}
	dataActor, dataToken := createMember("p3_data_review", "readonly")
	addedViewer, exportReviewer := createMember("p3_added_viewer", "admin")
	const dataset = "/workspaces/1/business-datasets/phase3"
	now := time.Now().UTC()
	source := db.BusinessMappingSource{Authority: "隔离验收", SourceVersion: "v1", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}
	zero, one := int64(0), int64(1)
	a.call("PUT", dataset, a.admin, db.BusinessDatasetInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, AccountID: f.account, AccountVersion: 1,
	}, 200)
	deptA, deptB := db.OrganizationScopeEntry{DepartmentKey: "dept-a"}, db.OrganizationScopeEntry{DepartmentKey: "dept-b"}
	putScope := func(user int64, version *int64, scopes ...db.OrganizationScopeEntry) {
		t.Helper()
		a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d/datasets/phase3/organization-scope", user), a.admin,
			db.OrganizationScopeInput{BusinessMappingSource: source, ExpectedVersion: version, IdentityVersion: 1, DatasetVersion: 1, Scopes: scopes}, 200)
	}
	for _, user := range []int64{1, 2, 3, dataActor, addedViewer} {
		a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d", user), a.admin, db.BusinessIdentityInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, BusinessKey: fmt.Sprintf("review-%d", user),
			Datasets: []db.BusinessDatasetVersion{{Key: "phase3", Version: 1}},
		}, 200)
		if user == dataActor {
			putScope(user, &zero, deptB)
		} else {
			putScope(user, &zero, deptA)
		}
	}
	a.call("PUT", dataset+"/row-column-policy", a.admin, db.BusinessRowColumnPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1,
		Definition: db.BusinessRowColumnDefinition{
			Schema: "public", View: view, Dimensions: db.OrganizationDimensionColumns{Department: "department"},
			AllowedScopes: []db.OrganizationScopeEntry{deptA, deptB}, AllowedColumns: []string{"title"},
		},
	}, 200)
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolation_guard VALUES($1,current_database(),'阶段 3 隔离验收')`, isolation); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_business_reports(workspace_id,report_id,dataset_key,isolation_id)
		VALUES(1,$1,'phase3',$2)`, report, isolation); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_report_reviews VALUES(1,$1)`, report); err != nil {
		t.Fatal(err)
	}
	grant := func(user int64) {
		t.Helper()
		if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_report_data_reviewers(workspace_id,dataset_key,user_id,valid_until)
			VALUES(1,'phase3',$1,now()+interval '1 hour')`, user); err != nil {
			t.Fatal(err)
		}
	}
	grant(1)
	grant(2)
	grant(dataActor)
	grant(3)
	first := f.submit(report, 1)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", first), a.admin,
		map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 409)
	classification := db.BusinessOutputPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1, RowColumnPolicyVersion: 1,
		BusinessOutputDefinition: db.BusinessOutputDefinition{Fields: map[string]db.BusinessFieldClassification{
			"title": {Classification: "internal"},
		}},
	}
	a.call("PUT", dataset+"/output-policy", a.admin, classification, 200)
	businessSign := func(review int64, users []int64, expectedStage string) {
		t.Helper()
		preview := f.validate(review, users, nil)
		out := a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin,
			map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": preview}, 200)
		stage, _ := out["approval_stage"].(string)
		if stage != expectedStage {
			t.Fatalf("unexpected review stage: %q, want %q", stage, expectedStage)
		}
	}
	release := func(review, expected int64, want int) {
		t.Helper()
		a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", report), a.admin,
			map[string]any{"request_id": testRequestID(), "expected_publication_no": expected, "review_id": review, "kind": "publish"}, want)
	}
	signData := func(review int64) {
		t.Helper()
		path := fmt.Sprintf("/report-reviews/%d/data-review", review)
		detail := a.call("GET", path, dataToken, nil, 200)
		hash, ok := detail["manifest_sha256"].(string)
		if !ok || len(hash) != 64 {
			t.Fatal("missing review manifest fingerprint")
		}
		a.call("POST", path, dataToken, map[string]any{"request_id": testRequestID(), "decision": "approved", "manifest_sha256": strings.Repeat("0", 64)}, 409)
		a.call("POST", path, dataToken, map[string]any{"request_id": testRequestID(), "decision": "approved", "manifest_sha256": hash}, 200)
	}
	businessSign(first, []int64{3}, "data_review")
	release(first, 0, 409)
	dataPath := fmt.Sprintf("/report-reviews/%d/data-review", first)
	a.call("GET", dataPath, a.admin, nil, 403)
	a.call("GET", dataPath, a.readonly, nil, 403)
	a.call("GET", dataPath, a.developer, nil, 403)
	a.call("GET", dataPath, dataToken, nil, 403)
	putScope(dataActor, &one, deptA)
	signData(first)
	release(first, 0, 200)
	if got := f.run(report, 1, 200)["rows"].([]any)[0].([]any)[0]; got != "synthetic" {
		t.Fatal("published internal report result is incorrect")
	}

	newRevision := func(draftVersion int64) int64 {
		t.Helper()
		draft["request_id"], draft["version"], draft["name"] = testRequestID(), draftVersion, fmt.Sprintf("报表修订 %d", draftVersion+1)
		saved := a.call("PUT", fmt.Sprintf("/managed-reports/%d/draft", report), a.developer, draft, 200)
		return f.submit(report, int64(saved["version"].(float64)))
	}
	revision := newRevision(1)
	businessSign(revision, []int64{3}, "")
	release(revision, 1, 200)

	expanded := newRevision(2)
	businessSign(expanded, []int64{3, addedViewer}, "data_review")
	signData(expanded)
	if _, err := a.pool.Exec(ctx, `DELETE FROM sys_iam_isolated_report_data_reviewers WHERE workspace_id=1 AND dataset_key='phase3' AND user_id=$1`, dataActor); err != nil {
		t.Fatal(err)
	}
	release(expanded, 2, 403)
	grant(dataActor)
	release(expanded, 2, 409)

	classification.ExpectedVersion = &one
	classification.Fields["title"] = db.BusinessFieldClassification{Classification: "sensitive"}
	a.call("PUT", dataset+"/output-policy", a.admin, classification, 200)
	sensitive := newRevision(3)
	businessSign(sensitive, []int64{3}, "data_review")
	release(sensitive, 2, 409)
	signData(sensitive)
	release(sensitive, 2, 200)
	if got := f.run(report, 3, 200)["rows"].([]any)[0].([]any)[0]; got != nil {
		t.Fatal("sensitive revision did not retain default masking")
	}

	expired := newRevision(4)
	currentPreview := f.validate(expired, []int64{3}, nil)
	expiredPreview := testRequestID()
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_report_validations(workspace_id,id,review_id,account_id,account_generation,
		account_version,manifest,manifest_hash,performed_by,created_at,business_policy_stamp)
		SELECT workspace_id,$1::uuid,review_id,account_id,account_generation,account_version,manifest,manifest_hash,performed_by,
			statement_timestamp()-interval '16 minutes',business_policy_stamp
		FROM sys_report_validations WHERE id=$2::uuid`, expiredPreview, currentPreview); err != nil {
		t.Fatal(err)
	}
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", expired), a.admin,
		map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": expiredPreview}, 409)

	draft["request_id"], draft["version"] = testRequestID(), 5
	draft["sql"] = `SELECT "title" FROM ` + qualified + ` WHERE length("title") >= :minimum::integer`
	draft["parameters"] = []any{map[string]any{"name": "minimum", "label": "最小长度", "type": "integer", "min": 0, "max": 20}}
	saved := a.call("PUT", fmt.Sprintf("/managed-reports/%d/draft", report), a.developer, draft, 200)
	parameterized := f.submit(report, int64(saved["version"].(float64)))
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", parameterized), a.admin,
		map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}, "params": map[string]any{"minimum": 1}}, 409)

	zeroAmount, ten, hundred := 0.0, 10.0, 100.0
	enumOptions := []db.ReportParameterOption{{Label: "红色", Value: "red"}, {Label: "蓝色", Value: "blue"}}
	filters := []db.BusinessReportParameterBinding{
		{Column: "category", Operator: "eq", Domain: db.ReportParameter{Name: "category", Label: "类别", Type: "enum", Required: true, Options: enumOptions, Default: "red"}},
		{Column: "active", Operator: "eq", Domain: db.ReportParameter{Name: "active", Label: "有效", Type: "boolean", Required: true, Default: true}},
		{Column: "amount", Operator: "gte", Domain: db.ReportParameter{Name: "amount_min", Label: "金额下界", Type: "decimal", Required: true, Min: &zeroAmount, Max: &hundred, Default: "0"}},
		{Column: "amount", Operator: "lte", Domain: db.ReportParameter{Name: "amount_max", Label: "金额上界", Type: "decimal", Required: true, Min: &zeroAmount, Max: &hundred, Default: "10"}},
		{Column: "sale_day", Operator: "gte", Domain: db.ReportParameter{Name: "day_min", Label: "起始日期", Type: "date", Required: true, DateMin: "2026-11-01", DateMax: "2026-11-30", Default: "2026-11-01"}},
		{Column: "sale_day", Operator: "lte", Domain: db.ReportParameter{Name: "day_max", Label: "结束日期", Type: "date", Required: true, DateMin: "2026-11-01", DateMax: "2026-11-30", Default: "2026-11-07"}},
	}
	columns := []string{"title", "category", "active", "amount", "sale_day"}
	a.call("PUT", dataset+"/row-column-policy", a.admin, db.BusinessRowColumnPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &one, DatasetVersion: 1,
		Definition: db.BusinessRowColumnDefinition{
			Schema: "public", View: view, Dimensions: db.OrganizationDimensionColumns{Department: "department"},
			AllowedScopes: []db.OrganizationScopeEntry{deptA, deptB}, AllowedColumns: columns, ParameterBindings: filters,
		},
	}, 200)
	two := int64(2)
	classification.ExpectedVersion, classification.RowColumnPolicyVersion = &two, two
	classification.Fields = map[string]db.BusinessFieldClassification{}
	for _, col := range columns {
		classification.Fields[col] = db.BusinessFieldClassification{Classification: "internal"}
	}
	a.call("PUT", dataset+"/output-policy", a.admin, classification, 200)
	parameters := make([]db.ReportParameter, 0, len(filters))
	for _, filter := range filters {
		parameters = append(parameters, filter.Domain)
	}
	parameters[0].Options = enumOptions[:1]
	parameters[2].Max, parameters[3].Max = &ten, &ten
	parameters[4].DateMax, parameters[5].DateMax = "2026-11-07", "2026-11-07"
	draft["sql"] = `SELECT "title", "category", "active", "amount", "sale_day" FROM ` + qualified +
		` WHERE ("category"::text COLLATE "C") = :category::text AND "active"::boolean = :active::boolean AND "amount"::numeric >= :amount_min::numeric AND "amount"::numeric <= :amount_max::numeric AND "sale_day"::date >= :day_min::date AND "sale_day"::date <= :day_max::date`
	draft["export_policy"] = map[string]any{"enabled": true, "max_rows": 10}
	submitParameters := func(version int64, defs []db.ReportParameter) int64 {
		t.Helper()
		draft["parameters"] = defs
		return newRevision(version)
	}
	bounded := submitParameters(6, parameters)
	businessSign(bounded, []int64{3, addedViewer}, "data_review")
	signData(bounded)
	release(bounded, 3, 200)

	execute := func(params map[string]any, status int) map[string]any {
		t.Helper()
		return a.call("POST", fmt.Sprintf("/published-reports/%d/execute", report), a.readonly,
			map[string]any{"request_id": testRequestID(), "publication_no": 4, "params": params}, status)
	}
	expectOne := func(rows []any) {
		t.Helper()
		if len(rows) != 1 || rows[0].([]any)[0] != "synthetic" {
			t.Fatal("filters did not intersect with current organization scope")
		}
	}
	expectOne(execute(nil, 200)["rows"].([]any))
	execute(map[string]any{"category": "blue"}, 400)
	execute(map[string]any{"amount_max": "50"}, 400)
	execute(map[string]any{"amount_min": "9", "amount_max": "3"}, 400)
	execute(map[string]any{"day_max": "2026-11-20"}, 400)
	execute(map[string]any{"day_min": "2026-11-06", "day_max": "2026-11-02"}, 400)

	exported := a.call("POST", fmt.Sprintf("/published-reports/%d/exports", report), a.readonly,
		map[string]any{"publication_no": 4, "request_id": testRequestID(), "format": "csv", "reason": "验证参数文件"}, 200)
	exportPath := fmt.Sprintf("/report-exports/%d", int64(exported["id"].(float64)))
	a.call("POST", exportPath+"/review", exportReviewer, map[string]any{"version": 1, "decision": "approved"}, 200)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.server.URL+"/api/v1/workspaces/1"+exportPath+"/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+a.readonly)
	response, err := a.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	csv, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || strings.Count(string(csv), "\n") != 2 || !strings.Contains(string(csv), "synthetic") {
		t.Fatal("actual export did not preserve the bounded parameter result")
	}
	a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(),
		"definition": map[string]any{"name": "参数订阅", "report_id": report, "publication_no": 4, "interval_minutes": 5, "enabled": true,
			"params": map[string]any{"amount_min": "2.75", "day_min": "2026-11-02"}},
	}, 200)
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	if count, err := service.Subscriptions.GenerateDue(ctx); err != nil || count != 1 {
		t.Fatalf("subscription not queued: %d %v", count, err)
	}
	run, err := service.RunSubscription(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := a.call("GET", fmt.Sprintf("/report-deliveries/%d", run.DeliveryID), a.readonly, nil, 200)["snapshot"].(map[string]any)
	expectOne(snapshot["result"].(map[string]any)["rows"].([]any))

	unchanged := submitParameters(7, parameters)
	businessSign(unchanged, []int64{3, addedViewer}, "")
	release(unchanged, 4, 200)
	for i, widening := range []struct {
		index int
		value db.ReportParameter
	}{{0, filters[0].Domain}, {3, filters[3].Domain}, {5, filters[5].Domain}} {
		wider := append([]db.ReportParameter{}, parameters...)
		wider[widening.index] = widening.value
		review := submitParameters(int64(8+i), wider)
		businessSign(review, []int64{3, addedViewer}, "data_review")
		release(review, 5, 409)
		signData(review)
		if i == 2 {
			release(review, 5, 200)
		}
	}
}
