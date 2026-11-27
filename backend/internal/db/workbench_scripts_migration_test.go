package db

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/pkg/crypto"
)

func TestWorkbenchScriptMigrationPreservesHistoryAndReceipts(t *testing.T) {
	const version = "0098_workbench_scripts.sql"
	pool, _, ctx := iamTestDatabasePrepared(t, func(ctx context.Context, pool *pgxpool.Pool) {
		// Stage the pre-upgrade schema by holding back just the migration under test.
		if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (
			version VARCHAR(255) PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			t.Fatal(err)
		}
	})
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	cipher, err := crypto.NewCipher([]byte("local-script-migration-fixture-key"))
	if err != nil {
		t.Fatal(err)
	}
	conn := &Connection{UserID: 1, Name: "fixture", Type: "postgres", Host: "fixture", Port: 5432, Username: "fixture", SSLMode: "disable"}
	if err = NewConnectionRepository(pool, cipher).Create(ctx, conn); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO sys_query_history(connection_id,user_id,sql_text) VALUES($1,1,'SELECT 1')`, conn.ID)
	exec(`INSERT INTO sys_query_executions(user_id,request_id,request_hash,connection_id,status)
	VALUES (1,'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee',repeat('a',64),$1,'succeeded')`, conn.ID)
	exec(`INSERT INTO sys_sql_snippets(user_id,name,sql_text,visibility) VALUES
	(1,'old private','SELECT 1','private'),(1,'old shared','SELECT 2','shared')`)
	exec(`DELETE FROM schema_migrations WHERE version=$1`, version)
	CloseScopedRuntime(pool)
	if applied, err := Migrate(ctx, pool); err != nil || !slices.Equal(applied, []string{version}) {
		t.Fatalf("script migration mismatch: %v %v", applied, err)
	}
	var scripts, histories, receipts, connections int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM sys_sql_snippets),
		(SELECT count(*) FROM sys_query_history WHERE sql_text='SELECT 1'),
		(SELECT count(*) FROM sys_query_executions WHERE status='succeeded'),
		(SELECT count(*) FROM sys_connections WHERE id=$1)`, conn.ID).Scan(&scripts, &histories, &receipts, &connections); err != nil {
		t.Fatal(err)
	}
	if scripts != 0 || histories != 1 || receipts != 1 || connections != 1 {
		t.Fatalf("migration changed protected data: scripts=%d histories=%d receipts=%d connections=%d", scripts, histories, receipts, connections)
	}
	content := "SELECT 3"
	repo := NewSnippetRepository(pool)
	saved, err := repo.Save(ctx, Script{Name: "new", Type: "postgres", Content: &content})
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := Migrate(ctx, pool); err != nil || len(applied) != 0 {
		t.Fatalf("repeated migration changed applied versions: %v %v", applied, err)
	}
	if saved, err = repo.Get(ctx, saved.ID); err != nil || saved.Content == nil || *saved.Content != content {
		t.Fatalf("new script did not survive repeated migration: %v %v", saved.ID, err)
	}
}
