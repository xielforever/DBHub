package db

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

func TestIAMPrivateIdentityOnlyThroughCheckedWrappers(t *testing.T) {
	_, users, ctx := iamTestDatabase(t)
	ctx = iamBound(t, users, 1)
	tx, e := users.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var allowed bool
	e = tx.QueryRow(ctx, `SELECT qualified FROM dbhub_iam_identity(1)`).Scan(&allowed)
	var denied *pgconn.PgError
	if !errors.As(e, &denied) || denied.Code != "42501" {
		t.Fatal("runtime directly called private identity helper", e)
	}
	_ = tx.Rollback(context.Background())
	tx, e = users.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	var role string
	if e = tx.QueryRow(ctx, `SELECT dbhub_member_role(1)`).Scan(&role); e != nil || role != "admin" {
		t.Fatal("checked wrapper lost its authority", e, role)
	}
}

func TestIAMLegacyDependencyRevocationGoSQLParity(t *testing.T) {
	pool, users, _ := iamTestDatabase(t)
	users.ConfigureIAMIsolation("unit-only", "dependency-revocation-test")
	for _, kind := range []string{"api", "database", "worker"} {
		if e := users.IAMHeartbeat(context.Background(), kind, "dependency-revocation-test"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := pool.Exec(context.Background(), `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,action_id,reason) VALUES(1,1,2,$1::uuid,'action_revoke','query.page','isolated dependency revoke')`, dashboardUUID()); e != nil {
		t.Fatal(e)
	}
	ctx := iamBound(t, users, 2)
	c := claim(t, ctx)
	if c.IAM.Target || auth.HasAction(c, "query.read") || auth.HasAction(c, "query.write") || !auth.HasAction(c, "report.view") {
		t.Fatal("legacy revocation lost dependency or unrelated-right semantics", c.IAM)
	}
	tx, e := users.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	for _, cap := range iam.ReadCatalog().Capabilities {
		var allowed bool
		if e = tx.QueryRow(ctx, `SELECT dbhub_iam_has_action($1,$2)`, c.Subject, cap.ID).Scan(&allowed); e != nil {
			t.Fatal(e)
		}
		if allowed != auth.HasAction(c, cap.ID) {
			t.Fatal("legacy dependent revoke differs between Go and SQL", cap.ID)
		}
	}
}
