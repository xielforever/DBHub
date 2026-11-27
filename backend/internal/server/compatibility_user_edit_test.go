package server

import (
	"fmt"
	"testing"
)

func TestCompatibilityUserEmailEdit(t *testing.T) {
	a := setupAPI(t)
	create := func(username, role string) int64 {
		t.Helper()
		user := a.call("POST", "/users", a.admin, map[string]any{
			"username": username, "password": "TestPassword123!", "role": role, "email": "initial@example.test",
		}, 201)
		return int64(user["id"].(float64))
	}
	login := func(username string) string {
		t.Helper()
		return a.call("POST", "/auth/login", "", map[string]any{
			"username": username, "password": "TestPassword123!",
		}, 200)["access_token"].(string)
	}
	assertUser := func(token, username, role, email string, editable bool) {
		t.Helper()
		items := a.call("GET", "/users?keyword="+username, token, nil, 200)["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("expected one matching user for %s, got %d", username, len(items))
		}
		item := items[0].(map[string]any)
		if item["role"] != role || item["email"] != email || item["can_edit_email"] != editable {
			t.Fatalf("unexpected role, email or email edit eligibility for %s: %v", username, item)
		}
	}

	local := create("compat_edit_local", "developer")
	shared := create("compat_edit_shared", "developer")
	create("compat_edit_admin", "admin")
	workspaceAdmin := login("compat_edit_admin")
	ctx := a.actor()
	var otherWorkspace int64
	if err := a.pool.QueryRow(ctx, `INSERT INTO sys_workspaces(name) VALUES('兼容邮箱权限测试空间') RETURNING id`).Scan(&otherWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES($1,$2,'readonly')`, otherWorkspace, shared); err != nil {
		t.Fatal(err)
	}

	localPath := fmt.Sprintf("/users/%d", local)
	sharedPath := fmt.Sprintf("/users/%d", shared)
	assertUser(a.admin, "compat_edit_local", "developer", "initial@example.test", true)
	assertUser(workspaceAdmin, "compat_edit_local", "developer", "initial@example.test", false)
	assertUser(a.admin, "compat_edit_shared", "developer", "initial@example.test", false)

	a.call("PUT", localPath, workspaceAdmin, map[string]any{"role": "readonly", "email": "denied@example.test"}, 403)
	assertUser(a.admin, "compat_edit_local", "developer", "initial@example.test", true)
	a.call("PUT", sharedPath, a.admin, map[string]any{"role": "readonly", "email": "denied@example.test"}, 403)
	assertUser(a.admin, "compat_edit_shared", "developer", "initial@example.test", false)
	a.call("PUT", sharedPath, a.admin, map[string]any{"role": "readonly"}, 200)
	assertUser(a.admin, "compat_edit_shared", "readonly", "initial@example.test", false)

	a.call("PUT", localPath, workspaceAdmin, map[string]any{"role": "readonly", "email": "initial@example.test"}, 200)
	assertUser(a.admin, "compat_edit_local", "readonly", "initial@example.test", true)
	a.call("PUT", localPath, a.admin, map[string]any{"role": "developer", "email": "  edited@example.test  "}, 200)
	assertUser(a.admin, "compat_edit_local", "developer", "edited@example.test", true)
	a.call("PUT", localPath, a.admin, map[string]any{"role": "readonly", "email": ""}, 200)
	assertUser(a.admin, "compat_edit_local", "readonly", "", true)
}
