package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func independentProvisionPool(t *testing.T, source *pgxpool.Pool) (*pgxpool.Pool, string) {
	t.Helper()
	cfg := source.Config().Copy()
	name := "atomic-runtime-" + dashboardUUID()
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { CloseScopedRuntime(pool); pool.Close() })
	return pool, name
}

func runtimeRoleCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_roles WHERE rolname LIKE 'dbhub_runtime_%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestScopedRuntimeProvisioningIsInvisibleUntilItsGrantsCommit(t *testing.T) {
	pool, _, ctx := iamTestDatabase(t)
	other, app := independentProvisionPool(t, pool)
	before := runtimeRoleCount(t, pool)
	block, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(context.Background())
	// Hold the ACL catalog tuple that GRANT actually updates. A data-table
	// ACCESS EXCLUSIVE lock does not stop a catalog-only ACL change.
	if _, err = block.Exec(ctx, `GRANT SELECT ON sys_users TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	initCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- configureScopedRuntime(initCtx, other) }()
	until := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, app).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(until) {
			t.Fatal("runtime provisioning never reached the grant lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtimeRoleCount(t, pool); n != before {
		t.Fatal("partially provisioned role became externally visible", n, before)
	}
	cancel()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancelled provisioning was published")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled provisioning did not roll back")
	}
	if n := runtimeRoleCount(t, pool); n != before {
		t.Fatal("cancelled provisioning leaked a role", n, before)
	}
}

func TestScopedRuntimeProvisioningRollsBackEveryGrantOnFailure(t *testing.T) {
	pool, _, ctx := iamTestDatabase(t)
	other, _ := independentProvisionPool(t, pool)
	if _, err := pool.Exec(ctx, `DROP FUNCTION dbhub_iam_runtime_admit(BIGINT,BIGINT,TEXT,BIGINT,BIGINT,BIGINT,TEXT,TEXT)`); err != nil {
		t.Fatal(err)
	}
	before := runtimeRoleCount(t, pool)
	if err := configureScopedRuntime(ctx, other); err == nil {
		t.Fatal("missing required guard did not abort provisioning")
	}
	if n := runtimeRoleCount(t, pool); n != before {
		t.Fatal("failed final guard grant left partial role grants", n, before)
	}
	if _, ok := scopedRegistry.Load(other); ok {
		t.Fatal("failed provisioning installed a runtime pool")
	}
}
