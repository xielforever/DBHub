package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func TestIsolatedDashboardReviewPolicy(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "review")
}

func TestIsolatedContentAvailability(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "availability")
}

func TestIsolatedContentCustody(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "custody")
}

func TestIsolatedDashboardCustodyBoundary(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "custody-dashboard")
}

func TestIsolatedFormalReportExport(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "formal-export")
}

func TestIsolatedSubscriptionReviews(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "subscription-review")
}

func TestIsolatedRecipientDelivery(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "recipient-delivery")
}

func TestIsolatedDeliveryRetention(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "delivery-retention")
}

func TestIsolatedDashboardDelivery(t *testing.T) {
	testIsolatedDashboardReviewPolicy(t, "dashboard-delivery")
}

func testIsolatedDashboardReviewPolicy(t *testing.T, scenario string) {
	availability := scenario == "availability"
	retention := scenario == "delivery-retention"
	recipientDelivery := scenario == "recipient-delivery" || retention
	dashboardDelivery := scenario == "dashboard-delivery"
	const isolation = "p3-dashboard-review"
	var testKey []byte
	a := setupConfiguredAPI(t, "", func(cfg *config.Config, _ string) {
		cfg.IAMIsolationID, cfg.Phase2ReportVerification = isolation, true
		testKey = cfg.SecretKey
	})
	f := setupManagedAPI(t, a)
	ctx := context.Background()
	create := func(name, role string) (int64, string) {
		t.Helper()
		const fixturePassword = "TestPassword123!"
		out := a.call("POST", "/users", a.admin, map[string]any{"username": name, "password": fixturePassword, "role": role}, 201)
		login := a.call("POST", "/auth/login", "", map[string]any{"username": name, "password": fixturePassword}, 200)
		return int64(out["id"].(float64)), login["access_token"].(string)
	}
	businessID, business := create("p322_business", "admin")
	dataID, data := create("p322_cross_dataset", "readonly")
	partialID, partial := create("p322_partial", "readonly")
	otherViewerID, otherViewer := create("p322_viewer", "readonly")
	now := time.Now().UTC()
	source := db.BusinessMappingSource{Authority: "隔离验收", SourceVersion: "v1", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}
	deptA, deptB := db.OrganizationScopeEntry{DepartmentKey: "dept-a"}, db.OrganizationScopeEntry{DepartmentKey: "dept-b"}
	zero, one := int64(0), int64(1)
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolation_guard VALUES($1,current_database(),'P3 仪表盘验收')`, isolation); err != nil {
		t.Fatal(err)
	}
	enum := func(options ...string) db.ReportParameter {
		p := db.ReportParameter{Name: "category", Label: "类别", Type: "enum", Required: true, Default: options[0]}
		for _, value := range options {
			p.Options = append(p.Options, db.ReportParameterOption{Label: value, Value: value})
		}
		return p
	}
	datasets := []string{"dash-a", "dash-b"}
	for _, key := range datasets {
		a.call("PUT", "/workspaces/1/business-datasets/"+key, a.admin, db.BusinessDatasetInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, AccountID: f.account, AccountVersion: 1,
		}, 200)
	}
	setScope := func(id int64, key string, version *int64, scope db.OrganizationScopeEntry) {
		t.Helper()
		a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d/datasets/%s/organization-scope", id, key), a.admin,
			db.OrganizationScopeInput{BusinessMappingSource: source, ExpectedVersion: version, IdentityVersion: 1, DatasetVersion: 1,
				Scopes: []db.OrganizationScopeEntry{scope}}, 200)
	}
	for _, id := range []int64{1, 2, 3, businessID, dataID, partialID, otherViewerID} {
		a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d", id), a.admin, db.BusinessIdentityInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, BusinessKey: fmt.Sprintf("dash-actor-%d", id),
			Datasets: []db.BusinessDatasetVersion{{Key: datasets[0], Version: 1}, {Key: datasets[1], Version: 1}},
		}, 200)
		for _, key := range datasets {
			scope := deptA
			if id == partialID && key == datasets[1] {
				scope = deptB
			}
			setScope(id, key, &zero, scope)
		}
	}
	if recipientDelivery {
		for _, id := range []int64{1, 3, dataID, otherViewerID} {
			a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d/datasets/dash-a/organization-scope", id), a.admin,
				db.OrganizationScopeInput{BusinessMappingSource: source, ExpectedVersion: &one, IdentityVersion: 1,
					DatasetVersion: 1, Scopes: []db.OrganizationScopeEntry{deptA, deptB}}, 200)
		}
	}
	if dashboardDelivery {
		for _, id := range []int64{1, 3, businessID, dataID} {
			a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d/datasets/dash-a/organization-scope", id), a.admin,
				db.OrganizationScopeInput{BusinessMappingSource: source, ExpectedVersion: &one, IdentityVersion: 1,
					DatasetVersion: 1, Scopes: []db.OrganizationScopeEntry{deptA, deptB}}, 200)
		}
	}
	grant := func(key string, id int64) {
		t.Helper()
		if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_report_data_reviewers(workspace_id,dataset_key,user_id,valid_until)
			VALUES(1,$1,$2,now()+interval '1 hour')`, key, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range datasets {
		for _, id := range []int64{2, 3, businessID, dataID} {
			grant(key, id)
		}
	}
	grant(datasets[0], partialID)

	type fixtureReport struct {
		id    int64
		draft map[string]any
		users []int64
	}
	publishReport := func(p fixtureReport, draft, publication int64, double bool) {
		t.Helper()
		review := f.submit(p.id, draft)
		preview := f.validate(review, p.users, nil)
		decision := a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin,
			map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": preview, "contract_confirmed": true}, 200)
		if double {
			if decision["approval_stage"] != "data_review" {
				t.Fatal("required report data review did not start")
			}
			path := fmt.Sprintf("/report-reviews/%d/data-review", review)
			detail := a.call("GET", path, data, nil, 200)
			a.call("POST", path, data, map[string]any{"request_id": testRequestID(), "decision": "approved", "manifest_sha256": detail["manifest_sha256"]}, 200)
		}
		f.release(p.id, publication, review, "publish")
	}
	makeReport := func(i int, parameter db.ReportParameter, masked bool, users []int64) fixtureReport {
		t.Helper()
		view := fmt.Sprintf("%s_card_%d", a.table, i)
		qualified := pgx.Identifier{"public", view}.Sanitize()
		quantity := "quantity"
		if scenario == "formal-export" {
			quantity += `+ CASE WHEN department='dept-a' AND category='blue' THEN (
				SELECT max(id)-1 FROM ` + pgx.Identifier{"public", a.table}.Sanitize() + ` WHERE owner_id=1) ELSE 0 END`
		}
		if dashboardDelivery {
			quantity += `+ CASE WHEN department='dept-a' AND category='red' THEN (
				SELECT COALESCE(max(id)-1,0) FROM ` + pgx.Identifier{"public", a.table}.Sanitize() + ` WHERE owner_id=1) ELSE 0 END`
		}
		if _, err := a.targetPool.Exec(ctx, "CREATE VIEW "+qualified+` AS
			SELECT department,category,`+quantity+` AS quantity,note,date_trunc('minute',now()) AS watermark
			FROM (VALUES ('dept-a','red',11::bigint,'synthetic'),('dept-a','blue',21::bigint,'other-category'),
			             ('dept-b','red',99::bigint,'outside')) v(department,category,quantity,note)`); err != nil {
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
		key := datasets[i]
		allowed := []string{"quantity", "category", "note", "watermark"}
		dataset := "/workspaces/1/business-datasets/" + key
		a.call("PUT", dataset+"/row-column-policy", a.admin, db.BusinessRowColumnPolicyInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1,
			Definition: db.BusinessRowColumnDefinition{Schema: "public", View: view, Dimensions: db.OrganizationDimensionColumns{Department: "department"},
				AllowedScopes: []db.OrganizationScopeEntry{deptA, deptB}, AllowedColumns: allowed,
				ParameterBindings: []db.BusinessReportParameterBinding{{Column: "category", Operator: "eq", Domain: parameter}}},
		}, 200)
		fields := map[string]db.BusinessFieldClassification{}
		for _, value := range allowed {
			fields[value] = db.BusinessFieldClassification{Classification: "internal"}
		}
		if masked {
			fields["note"] = db.BusinessFieldClassification{Classification: "sensitive"}
		}
		a.call("PUT", dataset+"/output-policy", a.admin, db.BusinessOutputPolicyInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1, RowColumnPolicyVersion: 1,
			BusinessOutputDefinition: db.BusinessOutputDefinition{Fields: fields},
		}, 200)
		contract := db.DataContract{
			SemanticKey: key, Meaning: "库存卡片", Schema: []db.ContractColumn{
				{Name: "quantity", Type: "integer"}, {Name: "category", Type: "string"}, {Name: "note", Type: "string", Nullable: masked},
				{Name: "watermark", Type: "timestamp_with_timezone"},
			},
			ChartType: "table", Unit: "item_count", Timezone: "UTC", ScopeKey: key, ScopeDescription: "按部门",
			TimeWindow: []byte(`{"kind":"current_snapshot"}`),
			Freshness:  db.ContractFreshness{Mode: "watermark_column", Column: "watermark", MaxAge: 900, FutureSkew: 60},
		}
		body := map[string]any{"request_id": testRequestID(), "name": key, "connection_id": a.connectionFor(a.developer),
			"sql":        `SELECT "quantity", "category", "note", "watermark" FROM ` + qualified + ` WHERE ("category"::text COLLATE "C") = :category::text`,
			"chart_type": "table", "chart_config": map[string]any{}, "data_contract": contract, "parameters": []db.ReportParameter{parameter}}
		if dashboardDelivery && i == 1 {
			contract.ChartType, contract.Metrics = "metric", []string{"quantity"}
			body["chart_type"], body["data_contract"] = "metric", contract
			body["chart_config"] = map[string]any{"metrics": []string{"quantity"}}
		}
		if (availability || scenario == "custody" || scenario == "formal-export" || recipientDelivery) && i == 0 {
			body["export_policy"] = map[string]any{"enabled": true, "max_rows": 10}
		}
		if dashboardDelivery {
			body["export_policy"] = map[string]any{"enabled": true, "max_rows": 10}
		}
		out := a.call("POST", "/managed-reports", a.developer, body, 200)
		id := int64(out["id"].(float64))
		if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_business_reports VALUES(1,$1,$2,$3)`, id, key, isolation); err != nil {
			t.Fatal(err)
		}
		if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_report_reviews VALUES(1,$1)`, id); err != nil {
			t.Fatal(err)
		}
		p := fixtureReport{id, body, users}
		publishReport(p, 1, 0, true)
		return p
	}
	required := makeReport(0, enum("red", "blue"), scenario == "formal-export" || recipientDelivery, []int64{2, 3, businessID, otherViewerID})
	if scenario == "formal-export" {
		verifyFormalReportExport(t, a, required.id, business, testKey)
		return
	}
	if scenario == "subscription-review" {
		verifySubscriptionReviews(t, a, required.id, otherViewerID, businessID, otherViewer, business)
		return
	}
	if recipientDelivery {
		checkRevokedPickup := verifyFormalPickup(t, a, required.id, business)
		if retention {
			verifyFormalRetention(t, a)
		}
		verifyTeamRecipientDelivery(t, a, required.id, otherViewerID, businessID, otherViewer, business, retention)
		if !retention {
			checkRevokedPickup()
		}
		return
	}
	optional := makeReport(1, enum("red"), true, []int64{2, 3, businessID})
	legacy, _ := dashboardReportAudience(f, []int64{1, 2, 3, businessID, otherViewerID})
	tile := func(key string, id int64, required bool) map[string]any {
		return map[string]any{"key": key, "report_id": id, "required": required, "width": "full", "parameter_bindings": map[string]string{"category": "category"}}
	}
	tiles := []map[string]any{tile("needed", required.id, true), tile("extra", optional.id, false)}
	definition := map[string]any{
		"name": "受管双数据集仪表盘", "tiles": []map[string]any{tiles[0], {"key": "legacy", "report_id": legacy, "required": false, "width": "full"}},
		"parameters": []db.ReportParameter{enum("red")}, "user_ids": []int64{3, otherViewerID},
		"quality": map[string]any{"allow_unknown_freshness": false, "max_watermark_skew_seconds": 300},
	}
	if dashboardDelivery {
		definition["user_ids"] = []int64{2, 3, otherViewerID}
	}
	created := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": definition}, 200)
	dashboard := int64(created["id"].(float64))
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_dashboard_reviews VALUES(1,$1,$2)`, dashboard, isolation); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/managed-dashboards/%d", dashboard)
	submit := func(version int64, want int) int64 {
		t.Helper()
		out := a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": version, "reviewer_id": businessID}, want)
		if want != 200 {
			return 0
		}
		return int64(out["id"].(float64))
	}
	save := func(version int64) {
		t.Helper()
		a.call("PUT", path+"/draft", a.developer, map[string]any{"request_id": testRequestID(), "version": version, "definition": definition}, 200)
	}
	release := func(review, expected int64, want int) {
		t.Helper()
		a.call("POST", path+"/releases", business, map[string]any{"request_id": testRequestID(), "expected_publication_no": expected, "review_id": review, "kind": "publish"}, want)
	}
	runTile := func(refresh, tile, actor string, masked bool) {
		t.Helper()
		result := a.call("POST", fmt.Sprintf("/dashboard-refreshes/%s/tiles/%s/execute", refresh, tile), actor, map[string]any{}, 200)
		rows := result["result"].(map[string]any)["rows"].([]any)
		if dashboardDelivery && tile == "needed" {
			if len(rows) != 2 || result["quality"].(map[string]any)["freshness"] != "fresh" {
				t.Fatal("required card did not cover both original ranges")
			}
			return
		}
		if len(rows) != 1 || fmt.Sprint(rows[0].([]any)[0]) != "11" ||
			(rows[0].([]any)[2] == nil) != masked || result["quality"].(map[string]any)["freshness"] != "fresh" {
			t.Fatal("card did not preserve authorized scope, masking and freshness")
		}
	}
	signBusiness := func(review int64, stage string) {
		t.Helper()
		preview := a.call("POST", path+"/refreshes", business, map[string]any{"mode": "review", "review_id": review}, 200)["refresh_id"].(string)
		runTile(preview, "needed", business, false)
		if len(definition["tiles"].([]map[string]any)) == 2 {
			runTile(preview, "extra", business, true)
		}
		rpath := fmt.Sprintf("/dashboard-reviews/%d", review)
		a.call("POST", rpath+"/validate", business, map[string]any{"request_id": testRequestID(), "refresh_id": preview}, 200)
		out := a.call("POST", rpath+"/decision", business, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
		if actual, _ := out["approval_stage"].(string); actual != stage {
			t.Fatalf("stage %q, expected %q", actual, stage)
		}
	}
	signData := func(review int64) {
		t.Helper()
		p := fmt.Sprintf("/dashboard-reviews/%d/data-review", review)
		detail := a.call("GET", p, data, nil, 200)
		a.call("POST", p, data, map[string]any{"request_id": testRequestID(), "manifest_sha256": strings.Repeat("0", 64), "decision": "approved"}, 409)
		a.call("POST", p, data, map[string]any{"request_id": testRequestID(), "manifest_sha256": detail["manifest_sha256"], "decision": "approved"}, 200)
	}
	checkPreflight := func(version int, code string) {
		t.Helper()
		out := a.call("POST", path+"/preflight", a.developer, map[string]any{"version": version}, 200)
		for _, issue := range out["issues"].([]any) {
			if issue.(map[string]any)["code"] == code && out["can_submit"] == false {
				return
			}
		}
		t.Fatalf("preflight did not report %s", code)
	}
	checkPreflight(1, "report_governance")
	submit(1, 409)
	definition["tiles"] = tiles
	definition["parameters"] = []db.ReportParameter{enum("red", "blue")}
	save(1)
	checkPreflight(2, "parameter_scope")
	submit(2, 409)
	definition["parameters"] = []db.ReportParameter{enum("red")}
	save(2)
	first := submit(3, 200)
	signBusiness(first, "data_review")
	release(first, 0, 409)
	dataPath := fmt.Sprintf("/dashboard-reviews/%d/data-review", first)
	for _, token := range []string{business, a.developer, a.readonly, partial} {
		a.call("GET", dataPath, token, nil, 403)
	}
	grant(datasets[1], partialID)
	a.call("GET", dataPath, partial, nil, 403)
	setScope(partialID, datasets[1], &one, deptA)
	if !dashboardDelivery {
		a.call("GET", dataPath, partial, nil, 200)
	}
	signData(first)
	release(first, 0, 200)
	if dashboardDelivery {
		verifyDashboardDelivery(t, a, dashboard, otherViewerID, dataID, partialID, data, partial, otherViewer)
		return
	}
	published := a.call("POST", path+"/refreshes", a.readonly, map[string]any{"mode": "published"}, 200)["refresh_id"].(string)
	runTile(published, "needed", a.readonly, false)
	runTile(published, "extra", a.readonly, true)
	read := a.call("GET", fmt.Sprintf("/published-dashboards/%d", dashboard), otherViewer, nil, 200)
	if optional := read["tiles"].([]any)[1].(map[string]any); optional["status"] != "restricted" || optional["report"] != nil {
		t.Fatal("optional card granted a report to an unrelated viewer")
	}
	if availability {
		republish := func() {
			definition["name"] = "正常下线后重新送审"
			save(3)
			revision := submit(4, 200)
			signBusiness(revision, "data_review")
			signData(revision)
			release(revision, 4, 200)
		}
		refreshReportReviewer := func() {
			a.call("PUT", "/workspaces/1/business-identities/1", a.admin, db.BusinessIdentityInput{
				BusinessMappingSource: source, ExpectedVersion: &one, BusinessKey: "dash-actor-1",
				Datasets: []db.BusinessDatasetVersion{{Key: datasets[0], Version: 1}, {Key: datasets[1], Version: 1}},
			}, 200)
			a.call("PUT", "/workspaces/1/business-identities/1/datasets/"+datasets[0]+"/organization-scope", a.admin,
				db.OrganizationScopeInput{BusinessMappingSource: source, ExpectedVersion: &one, IdentityVersion: 2, DatasetVersion: 1,
					Scopes: []db.OrganizationScopeEntry{deptA}}, 200)
		}
		verifyContentAvailability(t, a, f, dashboard, required.id, first, otherViewerID, businessID, otherViewer, business, published, republish, refreshReportReviewer)
		return
	}
	if scenario == "custody" {
		reviewerID, reviewer := create("p324_governor", "admin")
		pendingReport := f.submit(required.id, 1)
		pendingDashboard := submit(3, 200)
		verifyContentCustody(t, a, f, required.id, dashboard, pendingReport, pendingDashboard, businessID, reviewerID, business, reviewer)
		return
	}
	if scenario == "custody-dashboard" {
		reviewerID, _ := create("p324_governor", "admin")
		a.call("POST", fmt.Sprintf("/managed-reports/%d/transfer", optional.id), a.admin,
			map[string]any{"request_id": testRequestID(), "version": 1, "owner_id": businessID}, 200)
		freezeCustodyOwner(t, a, businessID, reviewerID)
		verifyDashboardCustodyBoundary(t, a, f, dashboard, optional.id, published)
		return
	}

	definition["name"] = "核对授权撤回"
	save(3)
	revokeCase := submit(4, 200)
	signBusiness(revokeCase, "data_review")
	signData(revokeCase)
	if _, err := a.pool.Exec(ctx, `DELETE FROM sys_iam_isolated_report_data_reviewers WHERE workspace_id=1 AND dataset_key=$1 AND user_id=$2`, datasets[1], dataID); err != nil {
		t.Fatal(err)
	}
	release(revokeCase, 1, 403)
	grant(datasets[1], dataID)
	release(revokeCase, 1, 409)

	definition["name"], definition["tiles"] = "收窄到内部必需卡片", tiles[:1]
	save(4)
	narrowed := submit(5, 200)
	signBusiness(narrowed, "")
	required.draft["name"], required.draft["version"], required.draft["request_id"] = "兼容的报表新修订", 1, testRequestID()
	a.call("PUT", fmt.Sprintf("/managed-reports/%d/draft", required.id), a.developer, required.draft, 200)
	publishReport(required, 2, 1, false)
	release(narrowed, 1, 409)
	definition["name"] = "引用已复验的兼容报表"
	save(5)
	revalidated := submit(6, 200)
	signBusiness(revalidated, "")
	release(revalidated, 1, 200)
}
