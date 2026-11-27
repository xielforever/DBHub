package db

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIAMGenericReadPlansRecheckPrincipalsAndRestoreCallerSettings(t *testing.T) {
	pool, _, ctx := iamTestDatabase(t)
	conn, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Release()
	tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SET LOCAL plan_cache_mode=force_custom_plan`); e != nil {
		t.Fatal(e)
	}
	read := func(name string, uid int64, expected int) {
		t.Helper()
		var actual int64
		var raw []byte
		if e := tx.QueryRow(ctx, `SELECT id,directory FROM dbhub_iam_identity_directory($1)`, name).Scan(&actual, &raw); e != nil {
			t.Fatal(e)
		}
		var spaces []Workspace
		if e := json.Unmarshal(raw, &spaces); e != nil || actual != uid || len(spaces) != expected {
			t.Fatal("cached plan retained another principal or stale qualification", actual, len(spaces), e)
		}
		var mode string
		var bound int
		var navigationSubject int64
		var navigationCount int
		if e := tx.QueryRow(ctx, `SELECT min(id),count(nav_id) FROM dbhub_iam_identity_navigation($1)`, name).Scan(&navigationSubject, &navigationCount); e != nil || navigationSubject != uid || navigationCount != expected {
			t.Fatal("navigation plan retained an earlier principal/qualification", navigationSubject, navigationCount, expected, e)
		}
		for _, detailed := range []bool{false, true} {
			if e := tx.QueryRow(ctx, `SELECT count(*) FROM dbhub_iam_workspace_directory($1,$2)`, uid, detailed).Scan(&navigationCount); e != nil || navigationCount != expected {
				t.Fatal("directory plan retained an earlier principal/qualification", uid, detailed, navigationCount, expected, e)
			}
		}
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM dbhub_iam_bind_workspace($1,1)`, uid).Scan(&bound); e != nil || bound != expected {
			t.Fatal("parameterized binding retained an earlier principal/qualification", uid, bound, expected, e)
		}
		if e := tx.QueryRow(ctx, `SHOW plan_cache_mode`).Scan(&mode); e != nil || mode != "force_custom_plan" {
			t.Fatal("private helper leaked its planning mode", mode, e)
		}
	}
	for range 8 {
		read("developer", 2, 1)
		read("readonly", 3, 1)
	}
	if _, e = pool.Exec(ctx, `UPDATE sys_workspace_members SET active=false WHERE workspace_id=1 AND user_id=2`); e != nil {
		t.Fatal(e)
	}
	read("developer", 2, 0)
	read("readonly", 3, 1)
}
