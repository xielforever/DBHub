package server

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func TestIAMDirectRoleHTTPReadsAndRevokesReportAccess(t *testing.T) {
	a := setupAPI(t)
	space := a.call(http.MethodPost, "/workspaces", a.admin, map[string]any{
		"name":              "Direct role report access",
		"request_id":        testRequestID(),
		"permission_matrix": true,
	}, http.StatusOK)
	wid := int64(space["id"].(float64))
	workspace := fmt.Sprintf("/workspaces/%d", wid)
	directory := fmt.Sprintf("/role-assignments/workspaces/%d/members", wid)
	a.call(http.MethodPost, workspace+"/members", a.admin, map[string]any{"username": "developer", "role": "admin"}, http.StatusOK)
	a.call(http.MethodPost, workspace+"/members", a.admin, map[string]any{"username": "readonly", "role": "readonly"}, http.StatusOK)
	listed := a.call(http.MethodGet, directory, a.developer, nil, http.StatusOK)
	if listed["enabled"] != true || len(listed["roles"].([]any)) != 3 {
		t.Fatalf("templates not available: %v", listed)
	}
	var member map[string]any
	for _, row := range listed["members"].([]any) {
		if item := row.(map[string]any); item["username"] == "readonly" {
			member = item
			break
		}
	}
	if member == nil {
		t.Fatal("missing target member")
	}
	path := fmt.Sprintf("%s/%d", directory, int64(member["user_id"].(float64)))
	assigned := a.call(http.MethodPut, path, a.developer, map[string]any{
		"request_id": testRequestID(), "expected_version": member["version"], "role_codes": []string{"viewer"},
	}, http.StatusOK)
	if got := assigned["role_codes"].([]any); !slices.Equal(got, []any{"viewer"}) {
		t.Fatalf("unexpected role receipt: %v", got)
	}
	effective := a.call(http.MethodGet, workspace+"/iam/effective", a.readonly, nil, http.StatusOK)
	if !slices.Contains(effective["actions"].([]any), any("report.page")) {
		t.Fatalf("report page permission missing in the actual browser route snapshot: %v", effective["actions"])
	}
	a.call(http.MethodGet, workspace+"/published-reports", a.readonly, nil, http.StatusOK)
	revoked := a.call(http.MethodPut, path, a.developer, map[string]any{
		"request_id": testRequestID(), "expected_version": assigned["version"], "role_codes": []string{},
	}, http.StatusOK)
	if got := revoked["role_codes"].([]any); len(got) != 0 {
		t.Fatalf("empty role selection retained roles: %v", got)
	}
	// The same login token is intentionally reused after the last role was
	// removed. The normal API path must make a fresh authorization decision.
	a.call(http.MethodGet, workspace+"/published-reports", a.readonly, nil, http.StatusForbidden)
}
