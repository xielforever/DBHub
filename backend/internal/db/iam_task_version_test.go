package db

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIAMTargetTaskCannotRelabelAnUnknownBridge(t *testing.T) {
	pool, users, ctx := iamTestDatabase(t)
	iamTargetFixture(t, pool, users, 3, []string{"report.page", "report.view"})
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT set_config('dbhub.runtime_bridge','phase1-bridge-2',true)`); e != nil {
		t.Fatal(e)
	}
	id := dashboardUUID()
	var bridge string
	e = tx.QueryRow(ctx, `INSERT INTO sys_query_executions(user_id,request_id,request_hash,connection_id,permission_bridge) VALUES(3,$1::uuid,$2,1,'future-test-bridge') RETURNING permission_bridge`, id, strings.Repeat("a", 64)).Scan(&bridge)
	if e != nil || bridge != "future-test-bridge" {
		t.Fatal("target insert relabeled an unsupported task", bridge, e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	for _, change := range []struct{ sql, message string }{
		{`UPDATE sys_query_executions SET permission_bridge='legacy-frozen-v1' WHERE request_id=$1::uuid`, "immutable"},
		{`UPDATE sys_query_executions SET status='running' WHERE request_id=$1::uuid`, "unsupported"},
	} {
		_, e = pool.Exec(ctx, change.sql, id)
		var pg *pgconn.PgError
		if !errors.As(e, &pg) || pg.Code != "0A000" || !strings.Contains(pg.Message, change.message) {
			t.Fatal("unknown task accepted by database guard", e)
		}
	}
}
