package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/user/dbhub/internal/db"
	"strings"
	"testing"
)

func dashboardReport(f *managedFixture) (int64, map[string]any) {
	return dashboardReportAudience(f, []int64{1, 2, 3})
}
func dashboardReportAudience(f *managedFixture, users []int64) (int64, map[string]any) {
	contract := map[string]any{"semantic_key": "stock", "meaning": "库存演示受限数据", "result_schema": []any{map[string]any{"name": "id", "logical_type": "integer", "nullable": false}, map[string]any{"name": "email", "logical_type": "string", "nullable": false}, map[string]any{"name": "watermark", "logical_type": "timestamp_with_timezone", "nullable": false}}, "chart_type": "table", "unit": "item_count", "currency": "", "timezone": "UTC", "scope_key": "owner1", "scope_description": "仅owner_id=1", "time_window": map[string]any{"kind": "current_snapshot"}, "comparison_key": "stock", "freshness": map[string]any{"mode": "watermark_column", "column": "watermark", "max_data_age_seconds": 900, "max_future_skew_seconds": 60}}
	body := map[string]any{"request_id": testRequestID(), "name": "库存明细", "connection_id": f.a.connectionFor(f.a.developer), "sql": "SELECT id,email,date_trunc('minute',now()) AS watermark FROM public." + f.view + " ORDER BY id", "chart_type": "table", "chart_config": map[string]any{}, "data_contract": contract}
	out := f.a.call("POST", "/managed-reports", f.a.developer, body, 200)
	id := int64(out["id"].(float64))
	review := f.submit(id, 1)
	validation := f.validate(review, users, nil)
	f.a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), f.a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation}, 400)
	f.a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), f.a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(id, 0, review, "publish")
	return id, body
}
func TestDashboardPhase1Lifecycle(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	id, _ := dashboardReport(f)
	catalog := a.call("GET", "/dashboard-report-catalog", a.developer, nil, 200)
	if len(catalog["items"].([]any)) != 1 {
		t.Fatal("approved contract missing")
	}
	definition := map[string]any{"name": "库存运营看板", "description": "审批发布验收", "tiles": []any{map[string]any{"key": "stock", "report_id": id, "required": true, "width": "full", "binding_policy": "follow_published"}}, "user_ids": []int64{3}, "group_ids": []int64{}, "quality": map[string]any{"allow_unknown_freshness": false, "max_watermark_skew_seconds": 300}}
	created := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": definition}, 200)
	did := int64(created["id"].(float64))
	path := fmt.Sprintf("/managed-dashboards/%d", did)
	a.call("GET", fmt.Sprintf("/published-dashboards/%d", did), a.readonly, nil, 404)
	a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": 2}, 400)
	submission := a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": 1}, 200)
	review := int64(submission["id"].(float64))
	rpath := fmt.Sprintf("/dashboard-reviews/%d", review)
	a.call("POST", rpath+"/decision", a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 400)
	refresh := a.call("POST", path+"/refreshes", a.admin, map[string]any{"mode": "review", "review_id": review}, 200)
	rid := refresh["refresh_id"].(string)
	tilepath := "/dashboard-refreshes/" + rid + "/tiles/stock/execute"
	a.call("POST", tilepath, a.readonly, map[string]any{}, 409)
	result := a.call("POST", tilepath, a.admin, map[string]any{}, 200)
	if result["quality"].(map[string]any)["freshness"] != "fresh" {
		t.Fatal("watermark not checked")
	}
	a.call("POST", tilepath, a.admin, map[string]any{}, 409)
	a.call("POST", rpath+"/validate", a.admin, map[string]any{"request_id": testRequestID(), "refresh_id": rid}, 200)
	a.call("POST", rpath+"/decision", a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 0, "review_id": review, "kind": "publish"}, 200)
	viewer := a.call("GET", fmt.Sprintf("/published-dashboards/%d", did), a.readonly, nil, 200)
	raw, _ := json.Marshal(viewer)
	for _, secret := range []string{"sql_text", "execution_boundary", "report_account_id", f.username} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("viewer metadata leak", secret)
		}
	}
	refresh = a.call("POST", path+"/refreshes", a.readonly, map[string]any{"mode": "published"}, 200)
	rid = refresh["refresh_id"].(string)
	a.call("POST", "/dashboard-refreshes/"+rid+"/tiles/stock/execute", a.readonly, map[string]any{"sql": "SELECT 1"}, 400)
	a.call("POST", "/dashboard-refreshes/"+rid+"/tiles/stock/execute", a.readonly, map[string]any{}, 200)
	for _, item := range a.call("GET", "/query/jobs", a.readonly, nil, 200)["items"].([]any) {
		job := item.(map[string]any)
		if job["connection_id"] != float64(0) || strings.Contains(job["sql"].(string), "SELECT") {
			t.Fatal("query jobs disclosed the managed execution boundary", job)
		}
	}
	a.call("POST", path+"/archive", a.developer, map[string]any{"request_id": testRequestID(), "version": 1}, 200)
	a.call("POST", "/dashboard-refreshes/"+rid+"/tiles/stock/execute", a.readonly, map[string]any{}, 409)
	a.call("POST", path+"/restore", a.developer, map[string]any{"request_id": testRequestID(), "version": 2}, 200)
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 2, "review_id": review, "kind": "publish"}, 409)
	a.call("POST", path+"/delete", a.developer, map[string]any{"request_id": testRequestID(), "version": 3}, 400)
	if e := a.deps.AuditJournal.Flush(a.actor()); e != nil {
		t.Fatal(e)
	}
	audit, _, e := a.deps.Audit.List(a.actor(), db.AuditFilter{Action: "DASHBOARD_EXECUTE", Outcome: "succeeded"}, 100, 0)
	if e != nil || len(audit) == 0 {
		t.Fatal("dashboard execution audit missing", e)
	}
	event := audit[0].Context
	if event.WorkspaceID != 1 || event.DashboardID != did || event.ReportID != id || event.ReportPublicationNo != 1 || event.ReportAccountID != f.account || event.ReportAccountGeneration != 1 || event.DatabaseAccount != f.username || event.ContractHash == "" || event.RefreshID == "" {
		t.Fatal("dashboard audit omitted actual execution identity", event)
	}

}
func TestWorkspaceIsolation(t *testing.T) {
	a := setupAPI(t)
	creation := map[string]any{"request_id": testRequestID(), "name": "第二组织"}
	created := a.call("POST", "/workspaces", a.admin, creation, 200)
	again := a.call("POST", "/workspaces", a.admin, creation, 200)
	if again["id"] != created["id"] {
		t.Fatal("lost create response duplicated workspace")
	}
	creation["name"] = "不同内容"
	a.call("POST", "/workspaces", a.admin, creation, 409)
	a.call("GET", "/workspaces/invalid/members", a.admin, nil, 404)
	wid := int64(created["id"].(float64))
	base := fmt.Sprintf("/workspaces/%d", wid)
	a.call("GET", "/connections", a.admin, nil, 428)
	a.call("GET", base+"/connections", a.readonly, nil, 404)
	list := a.call("GET", base+"/connections", a.admin, nil, 200)
	if len(list["items"].([]any)) != 0 {
		t.Fatal("sources copied across workspace")
	}
	a.call("GET", base+fmt.Sprintf("/connections/%d", a.connID), a.admin, nil, 404)
	a.call("GET", base+"/query/history", a.admin, nil, 200)
	// Direct repository reads use the runtime role, even with deliberately wrong IDs.
	ctx := db.WithWorkspace(a.actor(), wid)
	if _, e := a.deps.Conns.Get(ctx, a.connID); e == nil {
		t.Fatal("repository bypassed RLS")
	}
	if _, e := a.deps.Conns.Get(context.Background(), a.connID); e == nil {
		t.Fatal("missing scope did not fail closed")
	}
	for i := 0; i < 20; i++ {
		ctx := db.WithWorkspace(a.actor(), wid)
		if _, e := a.deps.Conns.Get(ctx, a.connID); e == nil {
			t.Fatal("pooled workspace scope leaked")
		}
		if _, e := a.deps.Conns.Get(a.actor(), a.connID); e != nil {
			t.Fatal("scope not reset", e)
		}
	}
	a.call("POST", base+"/members", a.admin, map[string]any{"username": "readonly", "role": "admin"}, 200)
	a.call("POST", base+"/users/2/reset-password", a.readonly, map[string]any{"new_password": "AnotherPassword123!"}, 403)
	a.call("GET", base+"/metrics/query", a.readonly, nil, 403)
	a.call("GET", "/platform/audit-logs", a.readonly, nil, 403)
}

func publishTestDashboard(f *managedFixture, required, optional int64) int64 {
	a := f.a
	tiles := []any{map[string]any{"key": "required", "report_id": required, "required": true, "width": "half"}}
	if optional > 0 {
		tiles = append(tiles, map[string]any{"key": "optional", "report_id": optional, "required": false, "width": "half"})
	}
	def := map[string]any{"name": "权限闭环验收", "description": "演示", "tiles": tiles, "user_ids": []int64{3}, "group_ids": []int64{}, "quality": map[string]any{"max_watermark_skew_seconds": 300}}
	saved := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": def}, 200)
	id := int64(saved["id"].(float64))
	path := fmt.Sprintf("/managed-dashboards/%d", id)
	submitted := a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": 1}, 200)
	review := int64(submitted["id"].(float64))
	refresh := a.call("POST", path+"/refreshes", a.admin, map[string]any{"mode": "review", "review_id": review}, 200)
	rid := refresh["refresh_id"].(string)
	for _, tile := range refresh["tiles"].([]any) {
		a.call("POST", "/dashboard-refreshes/"+rid+"/tiles/"+tile.(map[string]any)["key"].(string)+"/execute", a.admin, map[string]any{}, 200)
	}
	a.call("POST", fmt.Sprintf("/dashboard-reviews/%d/validate", review), a.admin, map[string]any{"request_id": testRequestID(), "refresh_id": rid}, 200)
	a.call("POST", fmt.Sprintf("/dashboard-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 0, "review_id": review, "kind": "publish"}, 200)
	return id
}
func TestDashboardAccessRequestReleaseLoop(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	required, _ := dashboardReport(f)
	optional, _ := dashboardReportAudience(f, []int64{1, 2})
	did := publishTestDashboard(f, required, optional)
	// Another admin can validate a formal review but gets no implicit viewer access.
	a.call("POST", "/users", a.admin, map[string]any{"username": "reviewer", "password": "TestPassword123!", "role": "admin"}, 201)
	reviewer := a.call("POST", "/auth/login", "", map[string]any{"username": "reviewer", "password": "TestPassword123!"}, 200)["access_token"].(string)
	view := a.call("GET", fmt.Sprintf("/published-dashboards/%d", did), a.readonly, nil, 200)
	restricted := view["tiles"].([]any)[1].(map[string]any)
	if restricted["status"] != "restricted" || restricted["report_id"] != float64(0) || restricted["report"] != nil {
		t.Fatal("optional denial leaked report metadata", restricted)
	}
	req := a.call("POST", fmt.Sprintf("/published-dashboards/%d/access-requests", did), a.readonly, map[string]any{"request_id": testRequestID(), "tile_key": "optional", "reason": "库存核对"}, 200)
	requestID := int64(req["id"].(float64))
	change := map[string]any{"request_id": testRequestID(), "base_publication_no": 1, "user_ids": []int64{1, 2, 3}, "group_ids": []int64{}, "request_ids": []int64{requestID}}
	proposal := a.call("POST", fmt.Sprintf("/managed-reports/%d/audience-changes", optional), a.admin, change, 200)
	review := int64(proposal["id"].(float64))
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), a.admin, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{1, 2, 3}}, 400)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), reviewer, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{1, 2, 3, 4}}, 409)
	validation := testRequestID()
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), reviewer, map[string]any{"request_id": validation, "account_id": f.account, "user_ids": []int64{1, 2, 3}}, 200)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), reviewer, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	requests := a.call("GET", "/access-requests", a.readonly, nil, 200)["items"].([]any)
	if requests[0].(map[string]any)["status"] != "approved_pending_release" {
		t.Fatal("approval incorrectly granted access", requests)
	}
	a.call("POST", fmt.Sprintf("/published-reports/%d/execute", optional), a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 1}, 404)
	// A05: cancelling an approved but unpublished request must block the grant.
	current := requests[0].(map[string]any)
	a.call("POST", fmt.Sprintf("/access-requests/%d/cancel", requestID), a.readonly, map[string]any{"request_id": testRequestID(), "version": current["version"]}, 200)
	a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", optional), a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 1, "review_id": review, "kind": "publish"}, 409)
	replacement := a.call("POST", fmt.Sprintf("/published-dashboards/%d/access-requests", did), a.readonly, map[string]any{"request_id": testRequestID(), "tile_key": "optional", "reason": "重新提交用途说明"}, 200)
	requestID = int64(replacement["id"].(float64))
	change["request_id"] = testRequestID()
	change["request_ids"] = []int64{requestID}
	proposal = a.call("POST", fmt.Sprintf("/managed-reports/%d/audience-changes", optional), a.admin, change, 200)
	review = int64(proposal["id"].(float64))
	validation = testRequestID()
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), reviewer, map[string]any{"request_id": validation, "account_id": f.account, "user_ids": []int64{1, 2, 3}}, 200)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), reviewer, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	// Publishing copies the exact approved audience, then fulfills the request atomically.
	f.release(optional, 1, review, "publish")
	requests = a.call("GET", "/access-requests", a.readonly, nil, 200)["items"].([]any)
	if requests[0].(map[string]any)["status"] != "fulfilled" || requests[0].(map[string]any)["actual_publication_no"] != float64(2) {
		t.Fatal("release did not fulfill permission", requests)
	}
	refreshed := a.call("POST", fmt.Sprintf("/managed-dashboards/%d/refreshes", did), a.readonly, map[string]any{"mode": "published"}, 200)
	if refreshed["tiles"].([]any)[1].(map[string]any)["status"] != "ready" {
		t.Fatal("audience-only release broke compatible dashboard")
	}
	a.call("POST", "/dashboard-refreshes/"+refreshed["refresh_id"].(string)+"/tiles/optional/execute", a.readonly, map[string]any{}, 200)
	// Concurrent proposals share a base. Publishing one makes the other conflict.
	for i := 0; i < 2; i++ {
		body := map[string]any{"request_id": testRequestID(), "base_publication_no": 2, "user_ids": []int64{1, 2, 3}, "group_ids": []int64{}, "request_ids": []int64{}}
		p := a.call("POST", fmt.Sprintf("/managed-reports/%d/audience-changes", optional), a.admin, body, 200)
		rv := int64(p["id"].(float64))
		v := testRequestID()
		a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", rv), reviewer, map[string]any{"request_id": v, "account_id": f.account, "user_ids": []int64{1, 2, 3}}, 200)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", rv), reviewer, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": v, "contract_confirmed": true}, 200)
		if i == 0 {
			review = rv
		} else {
			a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", optional), a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 2, "review_id": review, "kind": "publish"}, 200)
			a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", optional), a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 3, "review_id": rv, "kind": "publish"}, 409)
		}
	}
}
