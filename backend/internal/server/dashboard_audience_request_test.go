package server

import (
	"fmt"
	"testing"
)

func TestDashboardDraftAudienceRequestsAndGroupBenefitGuards(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	report, _ := dashboardReportAudience(f, []int64{1, 2})
	a.call("POST", "/users", a.admin, map[string]any{"username": "grant_reviewer", "password": "TestPassword123!", "role": "admin"}, 201)
	reviewer := a.call("POST", "/auth/login", "", map[string]any{"username": "grant_reviewer", "password": "TestPassword123!"}, 200)["access_token"].(string)
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "库存运营组", "description": "", "member_ids": []int64{3}}, 200)
	gid := int64(group["id"].(float64))
	def := map[string]any{"name": "待补权看板", "description": "", "tiles": []any{map[string]any{"key": "stock", "report_id": report, "required": true, "width": "full"}}, "user_ids": []int64{3}, "group_ids": []int64{gid}, "quality": map[string]any{"max_watermark_skew_seconds": 300}}
	saved := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": def}, 200)
	did := int64(saved["id"].(float64))
	path := fmt.Sprintf("/managed-dashboards/%d", did)
	a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": 1}, 400)
	a.call("POST", path+"/access-requests", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "tile_key": "stock", "reason": "看板所需库存范围", "user_ids": []int64{4}, "group_ids": []int64{}}, 403)
	created := a.call("POST", path+"/access-requests", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "tile_key": "stock", "reason": "看板所需库存范围", "user_ids": []int64{3}, "group_ids": []int64{gid}}, 200)
	ids := []int64{}
	for _, value := range created["ids"].([]any) {
		ids = append(ids, int64(value.(float64)))
	}
	if len(ids) != 2 {
		t.Fatal("individual/group requests were merged incorrectly")
	}
	proposal := a.call("POST", fmt.Sprintf("/managed-reports/%d/audience-changes", report), a.admin, map[string]any{"request_id": testRequestID(), "base_publication_no": 1, "user_ids": []int64{1, 2, 3}, "group_ids": []int64{gid}, "request_ids": ids}, 200)
	rv := int64(proposal["id"].(float64))
	validation := testRequestID()
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", rv), reviewer, map[string]any{"request_id": validation, "account_id": f.account, "user_ids": []int64{1, 2, 3}, "group_ids": []int64{gid}}, 200)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", rv), reviewer, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(report, 1, rv, "publish")
	requests := a.call("GET", "/access-requests", a.admin, nil, 200)["items"].([]any)
	if len(requests) != 2 {
		t.Fatal("target records lost")
	}
	for _, item := range requests {
		if item.(map[string]any)["status"] != "fulfilled" || item.(map[string]any)["actual_publication_no"] != float64(2) {
			t.Fatal("group/individual grant fulfilled before actual release", item)
		}
	}
	a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": 1}, 200)
	update := map[string]any{"name": "库存运营组", "description": "", "member_ids": []int64{1, 3}, "expected_version": 1}
	a.call("PUT", fmt.Sprintf("/user-groups/%d", gid), a.admin, update, 400)
	a.call("PUT", fmt.Sprintf("/user-groups/%d", gid), reviewer, update, 200)
}
