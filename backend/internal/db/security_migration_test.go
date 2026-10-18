package db

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSecurityUpgradePreservesHistoryAndInvalidatesUnsafeApprovals(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real PostgreSQL not configured")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	schema := fmt.Sprintf("security_upgrade_%d", time.Now().UnixNano())
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["dbhub.workspace_id"] = "1"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	defer CloseScopedRuntime(pool)
	if _, err = pool.Exec(ctx, `CREATE TABLE schema_migrations(version varchar(255) PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= "0037_" {
			continue
		}
		raw, _ := migrationFS.ReadFile("migrations/" + entry.Name())
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatal(entry.Name(), err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.Name()); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO sys_users(username,password_hash,role_id) SELECT 'owner','test-only-hash',id FROM sys_roles WHERE code='admin'`)
	exec(`INSERT INTO sys_users(username,password_hash,role_id) SELECT 'reviewer','test-only-hash',id FROM sys_roles WHERE code='admin'`)
	exec(`INSERT INTO sys_source_families(family_id) VALUES('11111111-1111-1111-1111-111111111111')`)
	exec(`INSERT INTO sys_source_definitions(family_id,revision,name,owner_id,visibility,config) VALUES('11111111-1111-1111-1111-111111111111',1,'old source',1,'private','{}')`)
	exec(`INSERT INTO sys_ssh_tunnels(user_id,name,host,username,auth_type,host_key) VALUES(1,'old tunnel','fixture.invalid','test','password','SHA256:fixture')`)
	exec(`INSERT INTO sys_proxies(user_id,name,host,port) VALUES(1,'old proxy','fixture.invalid',1080)`)
	exec(`INSERT INTO sys_connections(user_id,name,type,host,port,definition_revision_id,definition_family_id,ssh_tunnel_id,proxy_id)
 VALUES(1,'old binding','postgres','fixture.invalid',5432,1,'11111111-1111-1111-1111-111111111111',1,1)`)
	for _, status := range []string{"pending", "approved", "executing", "executed", "failed"} {
		exec(`INSERT INTO sys_tickets(title,type,table_name,connection_id,sql_text,risk,status,requester_id,approved_by)
 VALUES($1,'dml','fixture',1,'UPDATE fixture SET id=1 WHERE id=2','low',$1,1,2)`, status)
	}
	exec(`INSERT INTO sys_export_requests(user_id,connection_id,format,row_count,reason,payload,status,reviewed_by)
 VALUES(1,1,'json',1,'self','{}','approved',1),(1,1,'json',1,'independent','{}','approved',2)`)
	applied, err := Migrate(ctx, pool)
	if err != nil || len(applied) != 1 || applied[0] != "0037_security_execution_boundaries.sql" {
		t.Fatal("security upgrade failed", applied, err)
	}
	var records, invalidated, historical int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE title IN('pending','approved') AND status='rejected' AND execution_error LIKE '%重新提交%'),count(*) FILTER(WHERE title IN('executing','executed','failed') AND status=title) FROM sys_tickets`).Scan(&records, &invalidated, &historical); err != nil || records != 5 || invalidated != 2 || historical != 3 {
		t.Fatal("upgrade lost history or reused an unbound approval", records, invalidated, historical, err)
	}
	var exports int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM sys_export_requests WHERE (reason='self' AND status='pending' AND reviewed_by IS NULL) OR (reason='independent' AND status='approved' AND reviewed_by=2)`).Scan(&exports); err != nil || exports != 2 {
		t.Fatal("export decisions were not migrated safely", exports, err)
	}
	for _, table := range []string{"sys_ssh_tunnels", "sys_proxies"} {
		if _, err = pool.Exec(ctx, "DELETE FROM "+table+" WHERE id=1"); err == nil {
			t.Fatal("metadata owner bypassed in-use route protection", table)
		}
	}
	var unsafeFKs int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='sys_connections'::regclass AND confrelid IN('sys_ssh_tunnels'::regclass,'sys_proxies'::regclass) AND confdeltype<>'r'`).Scan(&unsafeFKs); err != nil || unsafeFKs != 0 {
		t.Fatal("an original or composite SET NULL route survived", unsafeFKs, err)
	}
	if again, err := Migrate(ctx, pool); err != nil || len(again) != 0 {
		t.Fatal("upgrade is not idempotent", again, err)
	}
}
