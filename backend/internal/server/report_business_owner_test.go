package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func TestManagedReportBusinessOwnerHandover(t *testing.T) {
	const isolation = "iam-report-business-owner"
	a := setupConfiguredAPI(t, "", func(cfg *config.Config, _ string) {
		cfg.IAMIsolationID, cfg.Phase2ReportVerification = isolation, true
	})
	f := setupManagedAPI(t, a)
	createSupervisor := func(name string) (int64, string) {
		t.Helper()
		out := a.call("POST", "/users", a.admin, map[string]any{
			"username": name, "password": "TestPassword123!", "role": "readonly",
		}, 201)
		login := a.call("POST", "/auth/login", "", map[string]any{
			"username": name, "password": "TestPassword123!",
		}, 200)
		return int64(out["id"].(float64)), login["access_token"].(string)
	}
	firstID, first := createSupervisor("iam_report_first_supervisor")
	nextID, next := createSupervisor("iam_report_next_supervisor")
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `UPDATE sys_iam_workspace_state SET matrix_enabled=true WHERE workspace_id=1`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{firstID, nextID} {
		var version int64
		if err := a.pool.QueryRow(ctx, `SELECT version FROM sys_workspace_members WHERE workspace_id=1 AND user_id=$1`, id).Scan(&version); err != nil {
			t.Fatal(err)
		}
		a.call("PUT", fmt.Sprintf("/role-assignments/workspaces/1/members/%d", id), a.admin, map[string]any{
			"request_id": testRequestID(), "expected_version": version, "role_codes": []string{"business_supervisor"},
		}, 200)
	}
	report, draft := f.draft(`SELECT id,email FROM public.` + f.view + ` ORDER BY id`)
	path := fmt.Sprintf("/managed-reports/%d", report)
	candidates := a.call("GET", path+"/business-owner-candidates", a.developer, nil, 200)
	if len(candidates["items"].([]any)) != 2 {
		t.Fatalf("eligible business supervisors: %#v", candidates)
	}
	detail := a.call("GET", path, a.developer, nil, 200)
	if detail["business_owner_required"] != true || detail["owner_id"] != float64(2) {
		t.Fatalf("unexpected initial ownership: %#v", detail)
	}
	a.call("POST", path+"/submit", a.developer, map[string]any{
		"request_id": testRequestID(), "version": 1,
	}, 400)
	submit := func(version int64, candidate int64) int64 {
		t.Helper()
		body := map[string]any{"request_id": testRequestID(), "version": version}
		if candidate > 0 {
			body["business_owner_id"] = candidate
		}
		return int64(a.call("POST", path+"/submit", a.developer, body, 200)["id"].(float64))
	}
	validate := func(actor string, review int64, expected int) string {
		t.Helper()
		key := testRequestID()
		a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), actor, map[string]any{
			"request_id": key, "account_id": f.account, "user_ids": []int64{3},
		}, expected)
		return key
	}
	approve := func(actor string, review int64, validation string, expected int) map[string]any {
		t.Helper()
		return a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), actor, map[string]any{
			"request_id": testRequestID(), "decision": "approved", "validation_id": validation,
		}, expected)
	}
	release := func(actor string, review, publication int64, expected int) {
		t.Helper()
		a.call("POST", path+"/releases", actor, map[string]any{
			"request_id": testRequestID(), "expected_publication_no": publication, "review_id": review, "kind": "publish",
		}, expected)
	}
	changeOwner := func(actor string, version, target int64, expected int) {
		t.Helper()
		a.call("PUT", path+"/business-owner", actor, map[string]any{
			"request_id": testRequestID(), "expected_version": version, "business_owner_id": target,
		}, expected)
	}
	firstReview := submit(1, firstID)
	validate(next, firstReview, 403)
	approve(first, firstReview, validate(first, firstReview, 200), 200)
	release(first, firstReview, 0, 200)
	detail = a.call("GET", path, a.developer, nil, 200)
	if detail["business_owner_id"] != float64(firstID) || detail["business_owner_version"] != float64(1) {
		t.Fatalf("initial owner not bound on publication: %#v", detail)
	}
	f.run(report, 1, 200)
	draft["request_id"], draft["version"], draft["name"] = testRequestID(), 1, "交接后的库存报表"
	a.call("PUT", path+"/draft", a.developer, draft, 200)
	second := submit(2, 0)
	previewBeforeTransfer := validate(first, second, 200)
	changeOwner(first, 1, nextID, 403)
	changeOwner(a.admin, 1, nextID, 200)
	changeOwner(a.admin, 1, firstID, 409)
	approve(first, second, previewBeforeTransfer, 403)
	f.run(report, 1, 200)
	approve(next, second, validate(next, second, 200), 200)
	changeOwner(a.admin, 2, firstID, 200)
	release(first, second, 1, 409)
	third := submit(2, 0)
	approve(a.admin, third, validate(a.admin, third, 200), 200)
	release(a.admin, third, 1, 200)
	f.run(report, 2, 200)
	detail = a.call("GET", path, a.developer, nil, 200)
	if detail["owner_id"] != float64(2) || detail["business_owner_id"] != float64(firstID) ||
		detail["business_owner_version"] != float64(3) || detail["publication_no"] != float64(2) {
		t.Fatalf("handover changed technical owner or publication accounting: %#v", detail)
	}

	// Enroll the same report in the existing high-risk policy and prove that the
	// new supervisor's valid business signature cannot replace data approval.
	view := f.view + "_scope"
	qualified := pgx.Identifier{"public", view}.Sanitize()
	if _, err := a.targetPool.Exec(ctx, "CREATE VIEW "+qualified+" AS SELECT 'dept-a'::text AS department,email::text AS title FROM "+pgx.Identifier{"public", f.view}.Sanitize()); err != nil {
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
	const dataset = "/workspaces/1/business-datasets/ownership"
	zero := int64(0)
	now := time.Now().UTC()
	source := db.BusinessMappingSource{Authority: "报表负责人隔离验收", SourceVersion: "v1", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}
	a.call("PUT", dataset, a.admin, db.BusinessDatasetInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, AccountID: f.account, AccountVersion: 1,
	}, 200)
	scopes := []db.OrganizationScopeEntry{{DepartmentKey: "dept-a"}}
	for _, user := range []int64{1, 3, firstID, nextID} {
		identity := fmt.Sprintf("/workspaces/1/business-identities/%d", user)
		a.call("PUT", identity, a.admin, db.BusinessIdentityInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, BusinessKey: fmt.Sprintf("owner-%d", user),
			Datasets: []db.BusinessDatasetVersion{{Key: "ownership", Version: 1}},
		}, 200)
		a.call("PUT", identity+"/datasets/ownership/organization-scope", a.admin, db.OrganizationScopeInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, IdentityVersion: 1, DatasetVersion: 1, Scopes: scopes,
		}, 200)
	}
	a.call("PUT", dataset+"/row-column-policy", a.admin, db.BusinessRowColumnPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1,
		Definition: db.BusinessRowColumnDefinition{Schema: "public", View: view,
			Dimensions: db.OrganizationDimensionColumns{Department: "department"}, AllowedScopes: scopes, AllowedColumns: []string{"title"}},
	}, 200)
	a.call("PUT", dataset+"/output-policy", a.admin, db.BusinessOutputPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1, RowColumnPolicyVersion: 1,
		BusinessOutputDefinition: db.BusinessOutputDefinition{Fields: map[string]db.BusinessFieldClassification{"title": {Classification: "sensitive"}}},
	}, 200)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO sys_iam_isolation_guard VALUES($1,current_database(),'角色矩阵报表隔离验收')`, []any{isolation}},
		{`INSERT INTO sys_iam_isolated_business_reports(workspace_id,report_id,dataset_key,isolation_id) VALUES(1,$1,'ownership',$2)`, []any{report, isolation}},
		{`INSERT INTO sys_iam_isolated_report_reviews VALUES(1,$1)`, []any{report}},
		{`INSERT INTO sys_iam_isolated_report_data_reviewers(workspace_id,dataset_key,user_id,valid_until) VALUES(1,'ownership',1,now()+interval '1 hour')`, nil},
	} {
		if _, err := a.pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	draft["request_id"], draft["version"], draft["sql"] = testRequestID(), 2, `SELECT "title" FROM `+qualified
	a.call("PUT", path+"/draft", a.developer, draft, 200)
	highRisk := submit(3, 0)
	stage := approve(first, highRisk, validate(first, highRisk, 200), 200)
	if stage["approval_stage"] != "data_review" {
		t.Fatalf("high-risk version missing independent data review stage: %#v", stage)
	}
	release(first, highRisk, 2, 409)
	dataPath := fmt.Sprintf("/report-reviews/%d/data-review", highRisk)
	a.call("GET", dataPath, first, nil, 403)
	changeOwner(a.admin, 3, nextID, 200)
	a.call("GET", dataPath, a.admin, nil, 409)
	highRisk = submit(3, 0)
	stage = approve(next, highRisk, validate(next, highRisk, 200), 200)
	if stage["approval_stage"] != "data_review" {
		t.Fatalf("handover bypassed the high-risk data review: %#v", stage)
	}
	release(next, highRisk, 2, 409)
	dataPath = fmt.Sprintf("/report-reviews/%d/data-review", highRisk)
	proof := a.call("GET", dataPath, a.admin, nil, 200)
	a.call("POST", dataPath, a.admin, map[string]any{
		"request_id": testRequestID(), "decision": "approved", "manifest_sha256": proof["manifest_sha256"],
	}, 200)
	release(next, highRisk, 2, 200)
	if rows := f.run(report, 3, 200)["rows"].([]any); len(rows) == 0 {
		t.Fatal("high-risk report returned no approved rows after dual review")
	}
}
