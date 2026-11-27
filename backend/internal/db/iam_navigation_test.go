package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/user/dbhub/internal/auth"
)

func TestIAMJoinedNavigationIsSingleUseAndNeverAuthorizesBinding(t *testing.T) {
	pool, users, _ := iamTestDatabase(t)
	ctx := auth.WithClaims(context.Background(), &auth.Claims{Subject: 2, Role: "developer"})
	expected, e := users.WorkspacesDirectory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	user, joined, e := users.FindIdentityAndWorkspaceDirectory(ctx, "developer")
	if e != nil || user.ID != 2 || user.PasswordHash == "" || !user.IsActive {
		t.Fatal("identity fields changed", e)
	}
	actual, e := users.WorkspacesDirectory(joined)
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if e != nil || string(a) != string(b) || len(actual) != 1 || actual[0].IAM != nil {
		t.Fatal("joined navigation changed directory surface", actual, expected, e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,2,$1::uuid,'member_freeze','joined navigation current binding')`, dashboardUUID()); e != nil {
		t.Fatal(e)
	}
	if _, e = users.BindWorkspace(joined, 1); e == nil {
		t.Fatal("joined navigation supplied authority after freeze")
	}
	actual, e = users.WorkspacesDirectory(joined)
	if e != nil || len(actual) != 0 {
		t.Fatal("navigation was reused beyond its single request read", actual, e)
	}
}

func TestIAMJoinedNavigationCannotBeSubstitutedForAnotherPrincipal(t *testing.T) {
	pool, users, _ := iamTestDatabase(t)
	root := context.Background()
	if _, e := pool.Exec(root, `INSERT INTO sys_workspaces(id,name) VALUES(2,'private navigation fixture');INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES(2,2,'developer')`); e != nil {
		t.Fatal(e)
	}
	_, joined, e := users.FindIdentityAndWorkspaceDirectory(root, "developer")
	if e != nil {
		t.Fatal(e)
	}
	other := auth.WithClaims(joined, &auth.Claims{Subject: 3, Role: "readonly"})
	spaces, e := users.WorkspacesDirectory(other)
	if e != nil || len(spaces) != 1 || spaces[0].ID != 1 {
		t.Fatal("substituted principal received another directory", spaces, e)
	}
	if _, _, e = users.FindIdentityAndWorkspaceDirectory(root, "not-a-current-user"); e != auth.ErrUserNotFound {
		t.Fatal("unknown identity accepted", e)
	}
	if _, e = pool.Exec(root, `UPDATE sys_users SET is_active=false WHERE id=2`); e != nil {
		t.Fatal(e)
	}
	if _, _, e = users.FindIdentityAndWorkspaceDirectory(root, "developer"); e != auth.ErrUserNotFound {
		t.Fatal("disabled identity accepted", e)
	}
}
