package server

import (
	"context"
	"fmt"
	"testing"
)

func TestDashboardLifecycleExactReviewAndPublishInbox(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := context.Background()
	createSupervisor := func(name string) (int64, string) {
		t.Helper()
		user := a.call("POST", "/users", a.admin, map[string]any{
			"username": name, "password": "TestPassword123!", "role": "readonly",
		}, 201)
		login := a.call("POST", "/auth/login", "", map[string]any{
			"username": name, "password": "TestPassword123!",
		}, 200)
		return int64(user["id"].(float64)), login["access_token"].(string)
	}
	signerID, signer := createSupervisor("dashboard_lifecycle_signer")
	otherID, other := createSupervisor("dashboard_lifecycle_other")
	report, _ := dashboardReportAudience(f, []int64{1, 2, 3, signerID, otherID})
	if _, err := a.pool.Exec(ctx, `UPDATE sys_iam_workspace_state SET matrix_enabled=true WHERE workspace_id=1`); err != nil {
		t.Fatal(err)
	}
	grant := func(id int64, roles ...string) {
		t.Helper()
		var version int64
		if err := a.pool.QueryRow(ctx, `SELECT version FROM sys_workspace_members WHERE workspace_id=1 AND user_id=$1`, id).Scan(&version); err != nil {
			t.Fatal(err)
		}
		a.call("PUT", fmt.Sprintf("/role-assignments/workspaces/1/members/%d", id), a.admin, map[string]any{
			"request_id": testRequestID(), "expected_version": version, "role_codes": append([]string{}, roles...),
		}, 200)
	}
	grant(2, "business_it", "business_supervisor")
	grant(3, "viewer")
	grant(signerID, "business_supervisor")
	grant(otherID, "business_supervisor")
	var enabled bool
	if err := a.pool.QueryRow(ctx, `SELECT dbhub_iam_enable_dashboard_matrix(1)`).Scan(&enabled); err != nil || !enabled {
		t.Fatalf("enable isolated dashboard matrix: %v %v", enabled, err)
	}
	definition := map[string]any{
		"name": "经营分析 v1", "user_ids": []int64{3}, "group_ids": []int64{},
		"tiles":   []any{map[string]any{"key": "stock", "report_id": report, "required": true, "width": "full"}},
		"quality": map[string]any{"max_watermark_skew_seconds": 300},
	}
	createDashboard := func() float64 {
		t.Helper()
		return a.call("POST", "/managed-dashboards", a.developer, map[string]any{
			"request_id": testRequestID(), "definition": definition,
		}, 200)["id"].(float64)
	}
	dashboard := createDashboard()
	path := fmt.Sprintf("/managed-dashboards/%.0f", dashboard)
	readPath := fmt.Sprintf("/published-dashboards/%.0f", dashboard)
	submit := func(version int) float64 {
		t.Helper()
		return a.call("POST", path+"/submit", a.developer, map[string]any{
			"request_id": testRequestID(), "version": version, "reviewer_id": signerID,
		}, 200)["id"].(float64)
	}
	decision := func(review float64, actor string, status int) {
		t.Helper()
		a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/decision", review), actor, map[string]any{
			"request_id": testRequestID(), "decision": "approved",
		}, status)
	}
	approve := func(review float64) {
		t.Helper()
		refresh := a.call("POST", path+"/refreshes", signer, map[string]any{"mode": "review", "review_id": review}, 200)
		refreshID := refresh["refresh_id"].(string)
		a.call("POST", "/dashboard-refreshes/"+refreshID+"/tiles/stock/execute", signer, map[string]any{}, 200)
		a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/validate", review), signer, map[string]any{
			"request_id": testRequestID(), "refresh_id": refreshID,
		}, 200)
		decision(review, signer, 200)
	}
	publish := func(review float64, number int, actor string, status int) {
		t.Helper()
		a.call("POST", path+"/releases", actor, map[string]any{
			"request_id": testRequestID(), "expected_publication_no": number, "review_id": review, "kind": "publish",
		}, status)
	}
	checkRead := func(version float64) {
		t.Helper()
		if live := a.call("GET", readPath, a.readonly, nil, 200); live["version"] != version {
			t.Fatal("unexpected live dashboard version", live)
		}
	}
	v1 := submit(1)
	approve(v1)
	publish(v1, 0, signer, 200)
	checkRead(1)
	definition["name"] = "经营分析 v2"
	a.call("PUT", path+"/draft", a.developer, map[string]any{
		"request_id": testRequestID(), "version": 1, "definition": definition,
	}, 200)
	v2 := submit(2)
	readReview := func(review float64, token string, status int) map[string]any {
		t.Helper()
		out := a.call("GET", fmt.Sprintf("%s/versions?review_id=%.0f", path, review), token, nil, status)
		if status != 200 {
			return nil
		}
		items := out["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["id"] != review {
			t.Fatal("exact version read was not pinned", out)
		}
		return items[0].(map[string]any)
	}
	checkTodo := func(kind, token string, review float64, version int) {
		t.Helper()
		todos := a.call("GET", "/inbox/todos?page_size=1&kind="+kind, token, nil, 200)
		items := todos["items"].([]any)
		if review == 0 {
			if len(items) != 0 || todos["total"] != float64(0) {
				t.Fatal("unexpected action in inbox", todos)
			}
			return
		}
		if len(items) != 1 || todos["total"] != float64(1) {
			t.Fatal("dashboard action not found in filtered inbox", todos)
		}
		item := items[0].(map[string]any)
		if item["kind"] != kind || item["id"] != review || item["resource_id"] != dashboard ||
			item["content_version"] != float64(version) || item["content_name"] != fmt.Sprintf("经营分析 v%d", version) {
			t.Fatal("inbox lost the frozen version identity", item)
		}
	}
	if first := readReview(v1, a.developer, 200); first["number"] != float64(1) || first["was_published"] != true {
		t.Fatal("exact older version is not the live v1", first)
	}
	if frozen := readReview(v2, signer, 200); frozen["number"] != float64(2) || frozen["status"] != "pending" {
		t.Fatal("exact pending version was not returned", frozen)
	}
	readReview(v2, other, 404)
	mismatch := createDashboard()
	a.call("GET", fmt.Sprintf("/managed-dashboards/%.0f/versions?review_id=%.0f", mismatch, v2), a.developer, nil, 404)
	a.call("GET", path+"/versions?review_id=0", a.developer, nil, 400)
	a.call("GET", "/inbox/todos?kind=unknown", signer, nil, 400)
	if detail := a.call("GET", path, a.developer, nil, 200); detail["active_version"] != float64(1) || detail["review_version"] != float64(2) {
		t.Fatal("live and review versions were not separated", detail)
	}
	if summary := a.call("GET", fmt.Sprintf("/dashboard-reviews/%.0f/summary", v2), signer, nil, 200); summary["version"] != float64(2) || summary["baseline_version"] != float64(1) {
		t.Fatal("review summary lost the live baseline", summary)
	}
	checkTodo("dashboard_review", signer, v2, 2)
	checkTodo("dashboard_publish", signer, 0, 0)
	decision(v2, a.developer, 403)
	approve(v2)
	checkRead(1)
	checkTodo("dashboard_review", signer, 0, 0)
	checkTodo("dashboard_publish", signer, v2, 2)
	for _, actor := range []string{a.developer, other, a.readonly} {
		checkTodo("dashboard_publish", actor, 0, 0)
	}
	if count := a.call("GET", "/inbox", signer, nil, 200)["todo_count"]; count != float64(1) {
		t.Fatal("notification count disagrees with the actionable publication task", count)
	}
	if page := a.call("GET", "/inbox/todos?kind=dashboard_publish&page=2&page_size=1", signer, nil, 200); page["total"] != float64(1) || len(page["items"].([]any)) != 0 {
		t.Fatal("publication task pagination changed filtered total", page)
	}
	publish(v2, 1, other, 403)
	publish(v2, 1, signer, 200)
	checkTodo("dashboard_publish", signer, 0, 0)
	checkRead(2)

	definition["name"] = "经营分析 v3"
	a.call("PUT", path+"/draft", a.developer, map[string]any{
		"request_id": testRequestID(), "version": 2, "definition": definition,
	}, 200)
	v3 := submit(3)
	approve(v3)
	checkTodo("dashboard_publish", signer, v3, 3)
	grant(signerID)
	checkTodo("dashboard_publish", signer, 0, 0)
	if count := a.call("GET", "/inbox", signer, nil, 200)["todo_count"]; count != float64(0) {
		t.Fatal("revoked signer still has an actionable publication task", count)
	}
	publish(v3, 2, signer, 403)
	grant(signerID, "business_supervisor")
	checkTodo("dashboard_publish", signer, 0, 0)
	if frozen := readReview(v3, signer, 200); frozen["status"] != "approved" ||
		frozen["business_signer_id"] != float64(signerID) || frozen["can_publish"] != false || frozen["requires_re_review"] != true {
		t.Fatal("regained role revived the old approval or lost its history", frozen)
	}
	publish(v3, 2, signer, 409)
	checkRead(2)
}
