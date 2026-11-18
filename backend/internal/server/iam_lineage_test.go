package server

import (
	"context"
	"testing"

	"github.com/user/dbhub/internal/db"
)

func TestIAMLineageDiscoverySkipsIdleActorsAndRetainsActualInputs(t *testing.T) {
	a := setupAPI(t)
	// setupAPI supplies a sample metadata inventory; this case starts with no
	// lineage inputs in its own disposable schema, then adds one explicit input.
	if _, e := a.pool.Exec(context.Background(), `DELETE FROM sys_query_history; DELETE FROM sys_meta_snapshots`); e != nil {
		t.Fatal(e)
	}
	repo := db.NewHistoryRepository(a.pool)
	actors, e := repo.ActiveActors(context.Background())
	if e != nil || len(actors) != 0 {
		t.Fatal("idle members became empty metadata jobs", e, len(actors))
	}
	if _, e = a.pool.Exec(context.Background(), `INSERT INTO sys_query_history(workspace_id,connection_id,user_id,sql_text,status) VALUES(1,$1,1,'SELECT 1 /* isolated discovery input */',1)`, a.connID); e != nil {
		t.Fatal(e)
	}
	actors, e = repo.ActiveActors(context.Background())
	if e != nil || len(actors) != 1 || actors[0].Subject != 1 || actors[0].WorkspaceID != 1 {
		t.Fatal("actual original actor/space input lost", e, actors)
	}
	if _, e = a.pool.Exec(context.Background(), `UPDATE sys_users SET is_active=false WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	actors, e = repo.ActiveActors(context.Background())
	if e != nil || len(actors) != 0 {
		t.Fatal("inactive input owner rediscovered", e, actors)
	}
}
