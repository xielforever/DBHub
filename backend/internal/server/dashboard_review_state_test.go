package server

import (
	"context"
	"fmt"
	"testing"
)

func TestDashboardReviewAssignmentAndValidationVersions(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	a.call("POST", "/users", a.admin, map[string]any{"username": "second_review", "password": "TestPassword123!", "role": "admin"}, 201)
	second := a.call("POST", "/auth/login", "", map[string]any{"username": "second_review", "password": "TestPassword123!"}, 200)["access_token"].(string)
	report, _ := dashboardReportAudience(f, []int64{1, 2, 3, 4})
	def := map[string]any{"name": "审阅状态验收", "tiles": []any{map[string]any{"key": "stock", "report_id": report, "required": true, "width": "full"}}, "user_ids": []int64{3}, "group_ids": []int64{}, "quality": map[string]any{"max_watermark_skew_seconds": 300}}
	saved := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": def}, 200)
	id := int64(saved["id"].(float64))
	path := fmt.Sprintf("/managed-dashboards/%d", id)
	submitted := a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": 1}, 200)
	review := int64(submitted["id"].(float64))
	rp := fmt.Sprintf("/dashboard-reviews/%d", review)
	refresh := a.call("POST", path+"/refreshes", a.admin, map[string]any{"mode": "review", "review_id": review}, 200)
	old := refresh["refresh_id"].(string)
	a.call("POST", rp+"/reassign", a.admin, map[string]any{"request_id": testRequestID(), "reviewer_id": 4}, 200)
	a.call("POST", rp+"/reassign", second, map[string]any{"request_id": testRequestID(), "reviewer_id": 1}, 200)
	a.call("POST", "/dashboard-refreshes/"+old+"/tiles/stock/execute", a.admin, map[string]any{}, 409)
	refresh = a.call("POST", path+"/refreshes", a.admin, map[string]any{"mode": "review", "review_id": review}, 200)
	current := refresh["refresh_id"].(string)
	result := a.call("POST", "/dashboard-refreshes/"+current+"/tiles/stock/execute", a.admin, map[string]any{}, 200)
	receipt := a.call("GET", "/dashboard-refreshes/"+current+"/tiles/stock/receipt", a.admin, nil, 200)
	if receipt["connection_id"] != float64(0) || receipt["database"] != "" {
		t.Fatal("receipt disclosed the service identity")
	}
	a.call("GET", "/query/executions/"+result["execution_id"].(string), a.readonly, nil, 404)
	// A completed read cannot become approval evidence when delivery or its
	// durable receipt failed after the tile quality check.
	if _, e := a.pool.Exec(context.Background(), `UPDATE sys_query_executions SET status='failed' WHERE request_id=$1::uuid`, result["execution_id"]); e != nil {
		t.Fatal(e)
	}
	a.call("POST", rp+"/validate", a.admin, map[string]any{"request_id": testRequestID(), "refresh_id": current}, 400)
	if _, e := a.pool.Exec(context.Background(), `UPDATE sys_query_executions SET status='succeeded' WHERE request_id=$1::uuid`, result["execution_id"]); e != nil {
		t.Fatal(e)
	}
	a.call("POST", rp+"/validate", a.admin, map[string]any{"request_id": testRequestID(), "refresh_id": current}, 200)
	a.call("PATCH", "/workspaces/1/members/3", a.admin, map[string]any{"expected_version": 1, "role": "developer"}, 200)
	a.call("POST", rp+"/decision", a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 409)
	a.call("POST", rp+"/validate", a.admin, map[string]any{"request_id": testRequestID(), "refresh_id": current}, 200)
	if _, e := a.pool.Exec(context.Background(), `UPDATE sys_dashboard_reviews SET validated_at=now()-interval '16 minutes' WHERE id=$1`, review); e != nil {
		t.Fatal(e)
	}
	a.call("POST", rp+"/decision", a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 400)
	a.call("POST", rp+"/validate", a.admin, map[string]any{"request_id": testRequestID(), "refresh_id": current}, 200)
	a.call("POST", rp+"/decision", a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
	if _, e := a.pool.Exec(context.Background(), `UPDATE sys_dashboard_refreshes SET expires_at=now()-interval '1 second' WHERE id=$1::uuid`, current); e != nil {
		t.Fatal(e)
	}
	a.call("GET", "/query/executions/"+result["execution_id"].(string), a.admin, nil, 409)
}

func TestDashboardUnpublishCancelsUnreleasedRequests(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	required, _ := dashboardReport(f)
	optional, _ := dashboardReportAudience(f, []int64{1, 2})
	did := publishTestDashboard(f, required, optional)
	path := fmt.Sprintf("/managed-dashboards/%d", did)
	request := func() {
		a.call("POST", fmt.Sprintf("/published-dashboards/%d/access-requests", did), a.readonly, map[string]any{"request_id": testRequestID(), "tile_key": "optional", "reason": "需要补权"}, 200)
	}
	request()
	versions := a.call("GET", path+"/versions", a.admin, nil, 200)["items"].([]any)
	review := versions[0].(map[string]any)["id"]
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 1, "review_id": 0, "kind": "unpublish"}, 200)
	records := a.call("GET", "/access-requests", a.readonly, nil, 200)["items"].([]any)
	if records[0].(map[string]any)["status"] != "cancelled" {
		t.Fatal("unpublish left a grant request active")
	}
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 2, "review_id": review, "kind": "publish"}, 200)
	request()
	a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", optional), a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 1, "review_id": 0, "kind": "unpublish"}, 200)
	records = a.call("GET", "/access-requests", a.readonly, nil, 200)["items"].([]any)
	if records[0].(map[string]any)["status"] != "cancelled" {
		t.Fatal("report shutdown left a grant request active")
	}
	a.call("POST", fmt.Sprintf("/published-dashboards/%d/access-requests", did), a.readonly, map[string]any{"request_id": testRequestID(), "tile_key": "optional", "reason": "已下线目标不能受理"}, 404)
}
