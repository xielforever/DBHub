package server

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestIAMDefaultMatrixWorkspaceHTTP(t *testing.T) {
	a := setupDefaultMatrixAPI(t)
	defaultEffective := a.call("GET", "/workspaces/1/iam/effective", a.admin, nil, 200)
	if defaultEffective["matrix_enabled"] != true || defaultEffective["is_super_admin"] != true {
		t.Fatal("default workspace is unavailable to its bootstrap administrator")
	}
	const directory = "/role-assignments/workspaces/1/members"
	roles := a.call("GET", directory, a.admin, nil, 200)
	if roles["enabled"] != true || len(roles["roles"].([]any)) != 3 {
		t.Fatal("default workspace does not offer the shared role directory")
	}
	var version any
	for _, item := range roles["members"].([]any) {
		membership := item.(map[string]any)
		if membership["username"] == "readonly" {
			version = membership["version"]
		}
	}
	if version == nil {
		t.Fatal("missing ordinary member for matrix role allocation")
	}
	a.call("PUT", directory+"/3", a.admin, map[string]any{
		"request_id": testRequestID(), "expected_version": version, "role_codes": []string{"viewer"},
	}, 200)
	viewer := a.call("GET", "/workspaces/1/iam/effective", a.readonly, nil, 200)
	actions := viewer["actions"].([]any)
	if viewer["matrix_enabled"] != true || viewer["snapshot"].(map[string]any)["target"] != true ||
		!slices.Contains(actions, any("report.view")) || slices.Contains(actions, any("query.page")) {
		t.Fatalf("unexpected viewer result after default workspace allocation: %v", actions)
	}
}

func TestIAMMatrixWorkspaceHTTPNavigationAndSuperAdmin(t *testing.T) {
	a := setupAPI(t)
	const menuID = "M04"
	const pageAction = "query.page"
	ctx := context.Background()
	legacy := a.call("GET", "/iam/effective", a.admin, nil, 200)
	if legacy["matrix_enabled"] != false || legacy["visible_menu_ids"] != nil {
		t.Fatal("an existing workspace entered matrix mode")
	}
	legacyBaseline := legacy["snapshot"].(map[string]any)
	a.call("PUT", "/iam/menus", a.admin, map[string]any{
		"request_id": testRequestID(), "configuration": legacy["menus"],
		"baseline": map[string]any{"config_version": legacyBaseline["config_version"], "authorization_epoch": legacyBaseline["authorization_epoch"]},
	}, 200)
	requestID := testRequestID()
	create := map[string]any{"request_id": requestID, "name": "Isolated matrix workspace", "permission_matrix": true}
	newSpace := a.call("POST", "/workspaces", a.admin, create, 200)
	wid := int64(newSpace["id"].(float64))
	prefix := fmt.Sprintf("/workspaces/%d", wid)
	if newSpace["permission_matrix"] != true || a.call("POST", "/workspaces", a.admin, create, 200)["id"] != newSpace["id"] {
		t.Fatal("new workspace replay changed its identity or mode")
	}
	a.call("POST", "/workspaces", a.admin, map[string]any{"request_id": requestID, "name": create["name"]}, 409)
	adminEffective := a.call("GET", prefix+"/iam/effective", a.admin, nil, 200)
	if adminEffective["matrix_enabled"] != true || adminEffective["is_super_admin"] != true || adminEffective["snapshot"].(map[string]any)["target"] != false {
		t.Fatal("starting a matrix workspace changed real IAM activation or lost its owner")
	}
	a.call("POST", prefix+"/members", a.admin, map[string]any{"username": "developer", "role": "admin"}, 200)
	a.call("POST", prefix+"/members", a.developer, map[string]any{"username": "readonly", "role": "readonly"}, 403)
	a.call("POST", prefix+"/members", a.admin, map[string]any{"username": "readonly", "role": "readonly"}, 200)
	configuration := func(actor string) map[string]any {
		effective := a.call("GET", prefix+"/iam/effective", actor, nil, 200)
		snapshot := effective["snapshot"].(map[string]any)
		return map[string]any{
			"request_id": testRequestID(), "configuration": effective["menus"],
			"baseline": map[string]any{"config_version": snapshot["config_version"], "authorization_epoch": snapshot["authorization_epoch"]},
		}
	}
	adminLike := a.call("GET", prefix+"/iam/effective", a.developer, nil, 200)
	if adminLike["is_super_admin"] != false || adminLike["menus"].(map[string]any)["role_visibility"] != nil {
		t.Fatal("ordinary workspace administrator read the raw role matrix")
	}
	a.call("PUT", prefix+"/iam/menus", a.developer, configuration(a.developer), 403)
	a.call("POST", prefix+"/iam/proposals/preview", a.developer, map[string]any{"patch": map[string]any{"kind": "role_upsert"}}, 403)
	adminBaseline := configuration(a.admin)["baseline"]
	a.call("POST", prefix+"/iam/proposals/preview", a.admin, map[string]any{
		"baseline": adminBaseline,
		"patch": map[string]any{
			"kind": "role_upsert", "role_id": testRequestID(), "name": "Disallowed grant", "enabled": true,
			"actions": []string{"space.page", "space.roles.view", "space.roles.manage"}, "valid_until": time.Now().Add(time.Hour).UTC(),
		},
	}, 403)

	// Only activated role sources are fixtures. The workspace, membership,
	// configuration, effective permissions and rejection checks use real HTTP.
	fixture := func(query string, args ...any) {
		t.Helper()
		if _, err := a.pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	hiddenRole, visibleRole, bundle := testRequestID(), testRequestID(), testRequestID()
	hiddenGrant, visibleGrant := testRequestID(), testRequestID()
	for _, role := range []struct {
		id, grant string
		actions   []string
	}{{hiddenRole, hiddenGrant, []string{pageAction, "query.read"}}, {visibleRole, visibleGrant, []string{pageAction, "audit.page"}}} {
		fixture(`INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by) VALUES($1,$2::uuid,1,$2,$3,true,1)`, wid, role.id, role.actions)
		fixture(`INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled) VALUES($1,$2::uuid,1,$2,true)`, wid, role.id)
		fixture(`INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id)
			VALUES($1,$2::uuid,$3::uuid,1,'user',3,ARRAY[3]::bigint[],statement_timestamp()+interval '1 hour',1,$4::uuid)`, wid, role.grant, role.id, testRequestID())
	}
	fixture(`INSERT INTO sys_iam_bundles(workspace_id,bundle_id,config_version,minimum_bridge,manifest,state,created_by)
		SELECT $1,$2::uuid,config_version+1,'phase1-bridge-2','{}','shadow',1 FROM sys_iam_workspace_state WHERE workspace_id=$1`, wid, bundle)
	fixture(`INSERT INTO sys_iam_principal_modes(workspace_id,user_id,mode,confirmed,legacy_data_role,legacy_capabilities,envelope_ref,bundle_id)
		SELECT $1,user_id,'TARGET',true,role_code,capabilities,'{"fixture":"isolated matrix"}',$2::uuid FROM sys_workspace_members WHERE workspace_id=$1 AND user_id=3`, wid, bundle)
	fixture(`UPDATE sys_iam_workspace_state SET mode='COMPAT_ACTIVE',activation_ready=true,active_bundle_id=$2::uuid,minimum_bridge='phase1-bridge-2' WHERE workspace_id=$1`, wid, bundle)

	cmd := configuration(a.admin)
	cmd["configuration"].(map[string]any)["role_visibility"] = map[string]any{testRequestID(): map[string]string{menuID: "show"}}
	a.call("PUT", prefix+"/iam/menus", a.admin, cmd, 400)
	cmd = configuration(a.admin)
	cmd["configuration"].(map[string]any)["role_visibility"] = map[string]any{hiddenRole: map[string]string{menuID: "hide"}, visibleRole: map[string]string{menuID: "show"}}
	if a.call("PUT", prefix+"/iam/menus", a.admin, cmd, 200)["permissions_changed"] != false {
		t.Fatal("publishing the role matrix reported a functional grant")
	}
	cmd = configuration(a.admin)
	delete(cmd["configuration"].(map[string]any), "role_visibility")
	a.call("PUT", prefix+"/iam/menus", a.admin, cmd, 200)
	if configuration(a.admin)["configuration"].(map[string]any)["role_visibility"].(map[string]any)[hiddenRole] == nil {
		t.Fatal("legacy payload omission cleared the saved matrix")
	}
	check := func(visible, queryAccess, auditAccess bool) {
		t.Helper()
		effective := a.call("GET", prefix+"/iam/effective", a.readonly, nil, 200)
		gotMenus, gotActions := effective["visible_menu_ids"].([]any), effective["actions"].([]any)
		if effective["menus"].(map[string]any)["role_visibility"] != nil || slices.Contains(gotMenus, any(menuID)) != visible ||
			slices.Contains(gotActions, any("query.read")) != queryAccess || slices.Contains(gotActions, any("audit.page")) != auditAccess {
			t.Fatalf("matrix or role union mismatch: menus=%v actions=%v", gotMenus, gotActions)
		}
	}
	check(true, true, true)
	a.call("PUT", prefix+"/iam/home", a.readonly, map[string]any{"menu_id": menuID}, 200)
	fixture(`UPDATE sys_iam_assignments SET revoked_at=statement_timestamp(),version=version+1 WHERE workspace_id=$1 AND assignment_id=$2::uuid`, wid, visibleGrant)
	check(false, true, false)
	a.call("PUT", prefix+"/iam/home", a.readonly, map[string]any{"menu_id": menuID}, 403)
	a.call("GET", prefix+"/query/script-folders", a.readonly, nil, 200)
	fixture(`UPDATE sys_iam_assignments SET revoked_at=statement_timestamp(),version=version+1 WHERE workspace_id=$1 AND assignment_id=$2::uuid`, wid, hiddenGrant)
	check(false, false, false)
	a.call("GET", prefix+"/query/script-folders", a.readonly, nil, 403)
}
