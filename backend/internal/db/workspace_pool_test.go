package db

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/pkg/crypto"
	"os"
	"testing"
	"time"
)

func TestWorkspaceRuntimeRoleAndCompositeReferences(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real PostgreSQL not configured")
	}
	ctx := context.WithValue(WithWorkspace(context.Background(), 1), auth.ClaimsContextKey, &auth.Claims{Subject: 1, Role: "admin"})
	owner, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	schema := fmt.Sprintf("workspace_guard_%d", time.Now().UnixNano())
	if _, e = owner.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	defer owner.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	defer CloseScopedRuntime(pool)
	if _, e = Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	users := NewUserRepository(pool)
	if e = users.EnsureAdmin(ctx, "guard-admin", "opaque-test-hash"); e != nil {
		t.Fatal(e)
	}
	w, e := users.CreateWorkspace(ctx, "isolated")
	if e != nil {
		t.Fatal(e)
	}
	cipher, _ := crypto.NewCipher([]byte("workspace-test-key-32-bytes-long-1"))
	repo := NewConnectionRepository(pool, cipher)
	source := &Connection{UserID: 1, Name: "first", Type: "postgres", Host: "test-only", Port: 5432, SSLMode: "disable"}
	if e = repo.Create(ctx, source); e != nil {
		t.Fatal(e)
	}
	scoped := scopedPool(pool)
	var super, bypass, owns bool
	e = scoped.QueryRow(ctx, `SELECT r.rolsuper,r.rolbypassrls,c.relowner=r.oid FROM pg_roles r CROSS JOIN pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE r.rolname=current_user AND n.nspname=current_schema() AND c.relname='sys_connections'`).Scan(&super, &bypass, &owns)
	if e != nil || super || bypass || owns {
		t.Fatal("runtime bypasses row security", super, bypass, owns, e)
	}
	var count int
	if e = scoped.runtime.QueryRow(ctx, `SELECT count(*) FROM sys_connections`).Scan(&count); e != nil || count != 0 {
		t.Fatal("unscoped runtime connection leaked data", count, e)
	}
	foreign := WithWorkspace(ctx, w.ID)
	_, e = scoped.Exec(foreign, `INSERT INTO sys_query_policies(connection_id,table_name,role_code,updated_by) VALUES($1,'inventory','readonly',1)`, source.ID)
	if e == nil {
		t.Fatal("cross-workspace foreign key accepted")
	}
	if _, e = scoped.Exec(ctx, `UPDATE sys_users SET is_platform_admin=true WHERE id=1`); e == nil {
		t.Fatal("runtime may escalate a global identity")
	}
	var password string
	if e = scoped.QueryRow(ctx, `SELECT password_hash FROM sys_users WHERE id=1`).Scan(&password); e == nil {
		t.Fatal("runtime can read identity password hashes")
	}
	for i := 0; i < 10; i++ {
		if _, e = repo.Get(foreign, source.ID); e == nil {
			t.Fatal("scope leaked across pooled transactions")
		}
		if _, e = repo.Get(ctx, source.ID); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = pool.Exec(ctx, `UPDATE sys_workspace_members SET role_code='readonly' WHERE workspace_id=$1 AND user_id=1`, w.ID); e != nil {
		t.Fatal(e)
	}
	if e = scoped.QueryRow(foreign, `SELECT count(*) FROM sys_connections`).Scan(&count); e != nil || count != 0 {
		t.Fatal("stale workspace role still accepted", count, e)
	}
	if scoped.Stat().AcquiredConns() != 0 {
		t.Fatal("scoped transactions leaked connections")
	}
	if err := users.Ready(ctx); err != nil {
		t.Fatal("healthy metadata pools rejected", err)
	}
	scoped.runtime.Close()
	if err := users.Ready(ctx); err == nil {
		t.Fatal("runtime pool outage hidden by a healthy owner pool")
	}
}

func TestConcurrentMigrationWithSingleConnectionPools(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real PostgreSQL not configured")
	}
	ctx := context.Background()
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	schema := fmt.Sprintf("workspace_cold_%d", time.Now().UnixNano())
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	defer root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	pools := []*pgxpool.Pool{}
	for i := 0; i < 2; i++ {
		cfg, _ := pgxpool.ParseConfig(dsn)
		cfg.MaxConns = 1
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
		pool, e := pgxpool.NewWithConfig(ctx, cfg)
		if e != nil {
			t.Fatal(e)
		}
		pools = append(pools, pool)
		defer pool.Close()
		defer CloseScopedRuntime(pool)
	}
	done := make(chan error, 2)
	for _, pool := range pools {
		go func(pool *pgxpool.Pool) { _, e := Migrate(ctx, pool); done <- e }(pool)
	}
	for i := 0; i < 2; i++ {
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("concurrent migration deadlocked with one metadata connection")
		}
	}
	var records int
	if e = pools[0].QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&records); e != nil || records != 35 {
		t.Fatal("migrations were skipped or applied twice", records, e)
	}
}
