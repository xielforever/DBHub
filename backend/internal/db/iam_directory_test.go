package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/auth"
)

func TestIAMDirectoryNeverSuppliesAuthorizationSnapshot(t *testing.T) {
	pool, users, _ := iamTestDatabase(t)
	ctx := auth.WithClaims(context.Background(), &auth.Claims{Subject: 2, Role: "developer"})
	// Warm the reused SQL plans before changing current qualification. No
	// earlier directory result or prepared plan may preserve frozen access.
	for range 8 {
		spaces, e := users.WorkspacesDirectory(ctx)
		if e != nil || len(spaces) != 1 || spaces[0].IAM != nil {
			t.Fatal("directory became an authorization snapshot", spaces, e)
		}
		if _, e = users.BindWorkspace(ctx, 1); e != nil {
			t.Fatal("current member cannot bind", e)
		}
	}
	for _, query := range []string{`SELECT * FROM dbhub_iam_workspace_directory(1,false)`, `SELECT * FROM dbhub_iam_binding(1)`, `SELECT dbhub_iam_request_current()`, `SELECT dbhub_iam_admit_current(1,1,'admin',1,1,1,'','phase1-bridge-2')`, `SELECT dbhub_iam_current_target_action(1,'report.view','admin')`, `SELECT * FROM dbhub_iam_identity_directory('readonly')`, `SELECT * FROM dbhub_iam_identity_context(1)`, `SELECT * FROM dbhub_iam_bind_workspace(1,1)`, `SELECT dbhub_iam_control_allowed(1,'space.roles.manage')`} {
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(context.Background())
		if _, e = tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{users.pool.role}.Sanitize()); e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec(ctx, query)
		if denied, ok := e.(*pgconn.PgError); !ok || denied.Code != "42501" {
			t.Fatal("tenant runtime can call private identity helper", e)
		}
		_ = tx.Rollback(ctx)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,2,$1::uuid,'member_freeze','isolated directory current qualification')`, dashboardUUID()); e != nil {
		t.Fatal(e)
	}
	spaces, e := users.WorkspacesDirectory(ctx)
	if e != nil || len(spaces) != 0 {
		t.Fatal("directory retained frozen membership", spaces, e)
	}
	if _, e = users.BindWorkspace(ctx, 1); e == nil {
		t.Fatal("binding trusted an earlier directory result")
	}
}
