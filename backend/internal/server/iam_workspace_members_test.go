package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/user/dbhub/internal/auth"
)

func TestIAMMatrixMembershipHTTP(t *testing.T) {
	a := setupAPI(t)
	left := a.call(http.MethodPost, "/workspaces", a.admin, map[string]any{
		"name": "Membership left", "request_id": testRequestID(), "permission_matrix": true,
	}, http.StatusOK)
	leftID := int64(left["id"].(float64))
	var founderID int64
	if err := a.pool.QueryRow(a.actor(), `INSERT INTO sys_users(username,password_hash,role_id,is_platform_admin)
	 SELECT 'membership-founder','opaque-fixture-hash',id,true FROM sys_roles WHERE code='admin' RETURNING id`).Scan(&founderID); err != nil {
		t.Fatal(err)
	}
	founder := auth.WithClaims(context.Background(), &auth.Claims{Subject: founderID})
	right, err := a.deps.UserRepo.CreateMatrixWorkspace(founder, "Membership right", testRequestID())
	if err != nil {
		t.Fatal(err)
	}
	rolesPath := func(space int64) string { return fmt.Sprintf("/role-assignments/workspaces/%d", space) }
	effective := func(space int64, token string, status int) map[string]any {
		t.Helper()
		return a.call(http.MethodGet, fmt.Sprintf("/workspaces/%d/iam/effective", space), token, nil, status)
	}
	join := func(space int64, token string, member int64, request string, status int) map[string]any {
		t.Helper()
		return a.call(http.MethodPost, rolesPath(space)+"/members", token, map[string]any{"user_id": member, "request_id": request}, status)
	}
	change := func(space int64, token string, member int64, version any, operation, request string, status int) map[string]any {
		t.Helper()
		return a.call(http.MethodPatch, fmt.Sprintf("%s/members/%d/membership", rolesPath(space), member), token,
			map[string]any{"request_id": request, "expected_version": version, "operation": operation}, status)
	}
	setRoles := func(space int64, token string, member int64, version any, roles ...string) map[string]any {
		t.Helper()
		return a.call(http.MethodPut, fmt.Sprintf("%s/members/%d", rolesPath(space), member), token,
			map[string]any{"request_id": testRequestID(), "expected_version": version, "role_codes": append([]string{}, roles...)}, http.StatusOK)
	}
	assertAction := func(out map[string]any, key string, want bool) {
		t.Helper()
		if granted := slices.Contains(out["actions"].([]any), any(key)); granted != want {
			t.Fatalf("action %q granted=%v want=%v", key, granted, want)
		}
	}

	local := join(leftID, a.admin, 2, testRequestID(), http.StatusOK)
	appointed := change(leftID, a.admin, 2, local["version"], "appoint_admin", testRequestID(), http.StatusOK)
	if appointed["member_role"] != "admin" || appointed["active"] != true || len(appointed["role_codes"].([]any)) != 0 {
		t.Fatalf("unexpected appointment result: %v", appointed)
	}
	localEffective := effective(leftID, a.developer, http.StatusOK)
	if localEffective["is_workspace_admin"] != true {
		t.Fatalf("missing separate workspace administrator duty: %v", localEffective["is_workspace_admin"])
	}
	assertAction(localEffective, "space.page", true)
	assertAction(localEffective, "space.members.view", true)
	assertAction(localEffective, "query.write", false)
	assertAction(localEffective, "report.page", false)
	listed := a.call(http.MethodGet, rolesPath(leftID)+"/members", a.developer, nil, http.StatusOK)
	if listed["can_manage_members"] != true || listed["can_manage_admins"] != false {
		t.Fatalf("mixed member and administrator permissions: %v", listed)
	}
	for _, protected := range []struct {
		id      int64
		version any
	}{{2, appointed["version"]}, {1, 1}} {
		a.call(http.MethodPut, fmt.Sprintf("%s/members/%d", rolesPath(leftID), protected.id), a.developer,
			map[string]any{"request_id": testRequestID(), "expected_version": protected.version, "role_codes": []string{"business_it"}}, http.StatusForbidden)
	}
	a.call(http.MethodGet, rolesPath(leftID)+"/member-candidates?keyword=readonly", a.developer, nil, http.StatusOK)
	a.call(http.MethodGet, rolesPath(right.ID)+"/member-candidates", a.developer, nil, http.StatusForbidden)

	request := testRequestID()
	member := join(leftID, a.developer, 3, request, http.StatusOK)
	if member["member_role"] != "readonly" || member["active"] != true || len(member["role_codes"].([]any)) != 0 {
		t.Fatalf("ordinary account inherited a role: %v", member)
	}
	if replay := join(leftID, a.developer, 3, request, http.StatusOK); replay["version"] != member["version"] {
		t.Fatalf("join receipt changed: %v", replay)
	}
	assertAction(effective(leftID, a.readonly, http.StatusOK), "report.page", false)
	change(leftID, a.developer, 3, member["version"], "appoint_admin", testRequestID(), http.StatusForbidden)
	a.call(http.MethodGet, rolesPath(leftID)+"/member-candidates", a.readonly, nil, http.StatusForbidden)
	change(leftID, a.developer, 1, 1, "remove", testRequestID(), http.StatusForbidden)
	assigned := setRoles(leftID, a.developer, 3, member["version"], "viewer")
	assertAction(effective(leftID, a.readonly, http.StatusOK), "report.page", true)
	change(leftID, a.developer, 3, member["version"], "remove", testRequestID(), http.StatusConflict)

	var groupID int64
	if err = a.pool.QueryRow(a.actor(), `INSERT INTO sys_user_groups(workspace_id,name) VALUES($1,'membership fixture') RETURNING id`, leftID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pool.Exec(a.actor(), `INSERT INTO sys_user_group_members(workspace_id,group_id,user_id) VALUES($1,$2,2),($1,$2,3)`, leftID, groupID); err != nil {
		t.Fatal(err)
	}
	removeRequest := testRequestID()
	removed := change(leftID, a.developer, 3, assigned["version"], "remove", removeRequest, http.StatusOK)
	if removed["active"] != false || len(removed["role_codes"].([]any)) != 0 {
		t.Fatalf("membership record still grants access: %v", removed)
	}
	if replay := change(leftID, a.developer, 3, assigned["version"], "remove", removeRequest, http.StatusOK); replay["version"] != removed["version"] {
		t.Fatalf("remove receipt changed: %v", replay)
	}
	effective(leftID, a.readonly, http.StatusNotFound)
	join(leftID, a.developer, 3, testRequestID(), http.StatusConflict)
	var groupMembers []int64
	if err = a.pool.QueryRow(a.actor(), `SELECT array_agg(user_id ORDER BY user_id) FROM sys_user_group_members
	 WHERE workspace_id=$1 AND group_id=$2`, leftID, groupID).Scan(&groupMembers); err != nil || !slices.Equal(groupMembers, []int64{2}) {
		t.Fatalf("group removal modified other members: %v %v", groupMembers, err)
	}

	// Platform management targets another workspace through the path. It never
	// creates a business membership for the acting super administrator.
	effective(right.ID, a.admin, http.StatusNotFound)
	change(right.ID, a.admin, founderID, 1, "revoke_admin", testRequestID(), http.StatusConflict)
	other := join(right.ID, a.admin, 3, testRequestID(), http.StatusOK)
	other = setRoles(right.ID, a.admin, 3, other["version"], "viewer")
	other = change(right.ID, a.admin, 3, other["version"], "appoint_admin", testRequestID(), http.StatusOK)
	if other["member_role"] != "admin" || !slices.Equal(other["role_codes"].([]any), []any{"viewer"}) {
		t.Fatalf("appointment changed business roles: %v", other)
	}
	other = change(right.ID, a.admin, 3, other["version"], "revoke_admin", testRequestID(), http.StatusOK)
	a.call(http.MethodGet, rolesPath(right.ID)+"/members", a.readonly, nil, http.StatusForbidden)
	assertAction(effective(right.ID, a.readonly, http.StatusOK), "report.page", true)
	setRoles(right.ID, a.admin, 3, other["version"])
	assertAction(effective(right.ID, a.readonly, http.StatusOK), "report.page", false)
	effective(right.ID, a.admin, http.StatusNotFound)

	var audits int
	if err = a.pool.QueryRow(a.actor(), `SELECT count(*) FROM sys_iam_events WHERE workspace_id=$1 AND actor_id=1
	 AND operation IN ('matrix.members.appoint_admin','matrix.members.revoke_admin') AND payload ? 'previous_membership'`, right.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("expected both administrator changes in audit, got %d %v", audits, err)
	}
}
