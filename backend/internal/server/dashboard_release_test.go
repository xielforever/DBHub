package server

import (
	"fmt"
	"testing"
)

func TestDashboardAudienceVersionsRollbackAndDependencies(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	report, body := dashboardReport(f)
	id := publishTestDashboard(f, report, 0)
	path := fmt.Sprintf("/managed-dashboards/%d", id)
	old := a.call("GET", path+"/versions", a.admin, nil, 200)["items"].([]any)[0].(map[string]any)["id"]
	a.call("POST", path+"/audience-changes", a.developer, map[string]any{"request_id": testRequestID(), "base_publication_no": 1, "reviewer_id": 1, "user_ids": []int64{1, 3}, "group_ids": []int64{}}, 400)
	proposal := a.call("POST", path+"/audience-changes", a.developer, map[string]any{"request_id": testRequestID(), "base_publication_no": 1, "reviewer_id": 1, "user_ids": []int64{2, 3}, "group_ids": []int64{}}, 200)
	review := int64(proposal["id"].(float64))
	rp := fmt.Sprintf("/dashboard-reviews/%d", review)
	preview := a.call("POST", path+"/refreshes", a.admin, map[string]any{"mode": "review", "review_id": review}, 200)
	refresh := preview["refresh_id"].(string)
	a.call("POST", "/dashboard-refreshes/"+refresh+"/tiles/required/execute", a.admin, map[string]any{}, 200)
	a.call("POST", rp+"/validate", a.admin, map[string]any{"request_id": testRequestID(), "refresh_id": refresh}, 200)
	a.call("POST", rp+"/decision", a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 1, "review_id": review, "kind": "publish"}, 200)
	a.call("GET", fmt.Sprintf("/published-dashboards/%d", id), a.developer, nil, 200)
	// Publish a compatible report repair independently of the dashboard layout.
	body["request_id"] = testRequestID()
	body["version"] = 1
	body["sql"] = body["sql"].(string) + " /* current repair */"
	a.call("PUT", fmt.Sprintf("/managed-reports/%d/draft", report), a.developer, body, 200)
	childReview := f.submit(report, 2)
	validation := f.validate(childReview, []int64{1, 2, 3}, nil)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", childReview), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(report, 1, childReview, "publish")
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 2, "review_id": old, "kind": "publish"}, 409)
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 2, "review_id": 0, "kind": "unpublish"}, 200)
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 3, "review_id": old, "kind": "publish"}, 409)
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 3, "review_id": old, "kind": "rollback"}, 200)
	next := a.call("POST", path+"/refreshes", a.readonly, map[string]any{"mode": "published"}, 200)
	if next["version"] != float64(1) {
		t.Fatal("dashboard layout rollback did not choose v1")
	}
	result := a.call("POST", "/dashboard-refreshes/"+next["refresh_id"].(string)+"/tiles/required/execute", a.readonly, map[string]any{}, 200)
	if result["report_publication_no"] != float64(2) {
		t.Fatal("layout rollback ran historical report SQL")
	}
	for _, scope := range []string{"current", "development", "history"} {
		references := a.call("GET", fmt.Sprintf("/managed-reports/%d/dashboard-dependencies?scope=%s&page_size=1", report, scope), a.admin, nil, 200)
		if references["total"] != float64(1) || len(references["items"].([]any)) != 1 {
			t.Fatal("dependency stages or pagination are wrong", scope, references)
		}
	}
	f.release(report, 2, 0, "unpublish")
	next = a.call("POST", path+"/refreshes", a.readonly, map[string]any{"mode": "published"}, 200)
	tile := next["tiles"].([]any)[0].(map[string]any)
	if tile["status"] != "restricted" || tile["execution_id"] != nil {
		t.Fatal("stopped report silently fell back to history", tile)
	}
}
