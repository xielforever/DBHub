package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDashboardPreflightCoverageAndReviewerChanges(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	a.call("POST", "/users", a.admin, map[string]any{"username": "ungranted_reviewer", "password": "TestPassword123!", "role": "admin"}, 201)
	required, _ := dashboardReportAudience(f, []int64{1, 2})
	optional, _ := dashboardReportAudience(f, []int64{1, 2})
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "库存运营组", "description": "", "member_ids": []int64{3}}, 200)
	gid := int64(group["id"].(float64))
	def := map[string]any{"name": "统一送审检查", "tiles": []any{map[string]any{"key": "required", "report_id": required, "required": true, "width": "half"}, map[string]any{"key": "optional", "report_id": optional, "required": false, "width": "half"}}, "user_ids": []int64{3}, "group_ids": []int64{gid}, "quality": map[string]any{"max_watermark_skew_seconds": 300}}
	saved := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": def}, 200)
	id := int64(saved["id"].(float64))
	path := fmt.Sprintf("/managed-dashboards/%d", id)
	var executions, reviews int
	count := func() (int, int) {
		var x, y int
		if e := a.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM sys_query_executions),(SELECT count(*) FROM sys_dashboard_reviews)`).Scan(&x, &y); e != nil {
			t.Fatal(e)
		}
		return x, y
	}
	executions, reviews = count()
	check := a.call("POST", path+"/preflight", a.developer, map[string]any{"version": 1}, 200)
	if check["can_submit"] != false {
		t.Fatal("missing required grants passed preflight")
	}
	candidates := check["eligible_reviewers"].([]any)
	if len(candidates) != 1 || candidates[0].(map[string]any)["id"] != float64(1) {
		t.Fatal("unqualified administrator appeared in reviewer choices", candidates)
	}
	coverage := 0
	for _, value := range check["issues"].([]any) {
		issue := value.(map[string]any)
		if issue["code"] != "audience_coverage" {
			continue
		}
		coverage++
		if len(issue["users"].([]any)) != 1 || len(issue["groups"].([]any)) != 1 {
			t.Fatal("did not collect individual and group gaps together", issue)
		}
		if issue["tile_key"] == "optional" && issue["severity"] != "warning" {
			t.Fatal("optional report gap should be a warning")
		}
	}
	if coverage != 2 {
		t.Fatal("preflight stopped after its first issue", check)
	}
	x, y := count()
	if x != executions || y != reviews {
		t.Fatal("preflight executed SQL or created an approval")
	}
	a.call("POST", path+"/preflight", a.readonly, map[string]any{"version": 1}, 403)
	def["user_ids"] = []int64{2}
	def["group_ids"] = []int64{}
	a.call("PUT", path+"/draft", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "definition": def}, 200)
	a.call("POST", path+"/preflight", a.developer, map[string]any{"version": 1}, 409)
	check = a.call("POST", path+"/preflight", a.developer, map[string]any{"version": 2}, 200)
	if check["can_submit"] != true {
		t.Fatal("covered saved draft did not pass", check)
	}
	// A positive preflight must never be an authorization token for later writes.
	a.call("PATCH", "/workspaces/1/members/1", a.admin, map[string]any{"expected_version": 1, "role": "developer"}, 200)
	a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 2, "reviewer_id": 1}, 400)
	check = a.call("POST", path+"/preflight", a.developer, map[string]any{"version": 2}, 200)
	if check["can_submit"] != false || len(check["eligible_reviewers"].([]any)) != 0 {
		t.Fatal("preflight reused an administrator's old role")
	}
}

func TestDashboardBusinessReviewSummaryAndIsolation(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	report, _ := dashboardReport(f)
	optional, _ := dashboardReport(f)
	id := publishTestDashboard(f, report, 0)
	path := fmt.Sprintf("/managed-dashboards/%d", id)
	draft := a.call("GET", path, a.developer, nil, 200)["draft"].(map[string]any)
	draft["name"] = "库存看板更新"
	draft["tiles"].([]any)[0].(map[string]any)["width"] = "full"
	draft["tiles"] = append(draft["tiles"].([]any), map[string]any{"key": "new", "report_id": optional, "required": false, "width": "half"})
	draft["user_ids"] = []int64{2}
	a.call("PUT", path+"/draft", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "definition": draft}, 200)
	rv := a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 2, "reviewer_id": 1}, 200)
	review := int64(rv["id"].(float64))
	summaryPath := fmt.Sprintf("/dashboard-reviews/%d/summary", review)
	summary := a.call("GET", summaryPath, a.admin, nil, 200)
	if summary["baseline_kind"] != "current_release" || summary["baseline_version"] != float64(1) || summary["reviewer_name"] != "admin" {
		t.Fatal("summary used wrong baseline or showed a raw reviewer ID", summary)
	}
	seen := map[string]bool{}
	for _, value := range summary["changes"].([]any) {
		change := value.(map[string]any)
		seen[change["area"].(string)+":"+change["subject"].(string)] = true
	}
	for _, expected := range []string{"卡片:新增卡片", "受众:新增个人受众", "受众:移除个人受众", "布局:库存明细 · 宽度"} {
		if !seen[expected] {
			t.Fatal("missing business change", expected, seen)
		}
	}
	cards := summary["cards"].([]any)
	if len(cards) != 2 || cards[0].(map[string]any)["contract"].(map[string]any)["scope_description"] != "仅owner_id=1" {
		t.Fatal("business scope absent from review", cards)
	}
	raw, _ := json.Marshal(summary)
	for _, secret := range []string{"sql_text", "execution_boundary", "account_id", f.username, f.password} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("review summary leaked internal execution data", secret)
		}
	}
	listed := a.call("GET", path, a.developer, nil, 200)
	if listed["review_status"] != "pending" || listed["review_id"] != float64(review) {
		t.Fatal("management state did not track the pending version")
	}
	a.call("GET", summaryPath, a.readonly, nil, 403)
	a.call("POST", "/users", a.admin, map[string]any{"username": "restricted_admin", "password": "TestPassword123!", "role": "admin"}, 201)
	restricted := a.call("POST", "/auth/login", "", map[string]any{"username": "restricted_admin", "password": "TestPassword123!"}, 200)["access_token"].(string)
	hidden := a.call("GET", summaryPath, restricted, nil, 200)
	for _, value := range hidden["cards"].([]any) {
		card := value.(map[string]any)
		if card["name"] != "受限报表" || card["contract"] != nil {
			t.Fatal("administrative metadata view bypassed ordinary report access", card)
		}
	}
	space := a.call("POST", "/workspaces", a.admin, map[string]any{"request_id": testRequestID(), "name": "摘要隔离验收"}, 200)
	wid := int64(space["id"].(float64))
	base := fmt.Sprintf("/workspaces/%d", wid)
	a.call("GET", base+summaryPath, a.admin, nil, 404)
	a.call("POST", base+path+"/preflight", a.admin, map[string]any{"version": 2}, 404)
}

func TestDashboardFollowsApprovedPresentationChange(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	report, body := dashboardReport(f)
	dashboard := publishTestDashboard(f, report, 0)
	path := fmt.Sprintf("/published-dashboards/%d", dashboard)
	view := a.call("GET", path, a.readonly, nil, 200)
	original := view["tiles"].([]any)[0].(map[string]any)["contract_hash"]
	body["request_id"] = testRequestID()
	body["version"] = 1
	body["chart_config"] = map[string]any{"display": map[string]any{"labels": map[string]string{"email": "联系邮箱"}, "grouping": true}}
	a.call("PUT", fmt.Sprintf("/managed-reports/%d/draft", report), a.developer, body, 200)
	review := f.submit(report, 2)
	validation := f.validate(review, []int64{1, 2, 3}, nil)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(report, 1, review, "publish")
	view = a.call("GET", path, a.readonly, nil, 200)
	tile := view["tiles"].([]any)[0].(map[string]any)
	if tile["status"] != "ready" || tile["contract_hash"] != original || view["publication_no"] != float64(1) {
		t.Fatal("presentation-only release unnecessarily broke the dashboard", tile)
	}
	config := tile["report"].(map[string]any)["chart_config"].(map[string]any)
	if config["display"].(map[string]any)["labels"].(map[string]any)["email"] != "联系邮箱" {
		t.Fatal("approved display names were lost")
	}
}
