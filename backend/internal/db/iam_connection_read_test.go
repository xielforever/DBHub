package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/pkg/crypto"
)

func TestIAMConnectionReadFenceUsesRestrictedRoleAndFreshRLS(t *testing.T) {
	pool, users, ctx := iamTestDatabase(t)
	cipher, e := crypto.NewCipher([]byte("workspace-test-key-32-bytes-long-1"))
	if e != nil {
		t.Fatal(e)
	}
	repo := NewConnectionRepository(pool, cipher)
	connection := &Connection{UserID: 2, Name: "restricted lease", Type: "postgres", Host: "synthetic.invalid", Port: 5432, SSLMode: "disable", Password: "private-restricted-lease-secret"}
	if e = repo.Create(iamBound(t, users, 2), connection); e != nil {
		t.Fatal(e)
	}
	iamTargetFixture(t, pool, users, 2, []string{"connection.page", "connection.use", "source.page"})
	ctx = iamBound(t, users, 2)
	claims, _ := auth.ClaimsFromContext(ctx)
	copy := *claims
	copy.CurrentAction = "connection.page"
	ctx = auth.WithClaims(ctx, &copy)
	readCtx, verify, release, e := users.IAMConnectionReadFence(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	fence := readCtx.Value(connectionReadKey{}).(*connectionReadFence)
	var current, session, isolation, readOnly string
	if e = fence.tx.QueryRow(readCtx, `SELECT current_user::text,session_user::text,current_setting('transaction_isolation'),current_setting('transaction_read_only')`).Scan(&current, &session, &isolation, &readOnly); e != nil || current != users.pool.role || session != users.pool.role || isolation != "read committed" || readOnly != "on" {
		t.Fatal("read fence used an owner connection, stale isolation or writable transaction", current, session, isolation, readOnly, e)
	}
	items, e := repo.List(readCtx, "", "")
	encoded, _ := json.Marshal(items)
	if e != nil || len(items) != 1 || items[0].UserID != 2 || !items[0].HasPassword || strings.Contains(string(encoded), connection.Password) {
		t.Fatal("restricted read changed owner/column surface", items, e)
	}
	if users.pool.runtime.Stat().AcquiredConns() != 0 || users.pool.leases.Stat().AcquiredConns() != 1 {
		t.Fatal("read opened a second data transaction")
	}
	if e = verify(); e != nil {
		t.Fatal(e)
	}
	// The cutoff commits independently of settlement. A fresh RLS statement and
	// final verification must both reject it while this shared fence is held.
	if _, e = pool.Exec(context.Background(), `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,2,$1::uuid,'member_freeze','restricted read current statement')`, dashboardUUID()); e != nil {
		t.Fatal(e)
	}
	items, e = repo.List(readCtx, "", "")
	if e != nil || len(items) != 0 {
		t.Fatal("shared read retained stale RLS", items, e)
	}
	if e = verify(); e == nil {
		t.Fatal("delivery reused earlier qualification")
	}
	release()
	if _, e = repo.List(readCtx, "", ""); e == nil {
		t.Fatal("released read fence was reused")
	}
}

func TestIAMConnectionReadFenceRejectsSubstitutedContext(t *testing.T) {
	_, users, ctx := iamTestDatabase(t)
	ctx = iamBound(t, users, 2)
	readCtx, _, release, e := users.IAMConnectionReadFence(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	for _, other := range []context.Context{WithWorkspace(readCtx, 999), auth.WithClaims(readCtx, &auth.Claims{Subject: 3, Role: "readonly"})} {
		if _, unlock, e := connectionReadTransaction(other, users.pool); e == nil {
			if unlock != nil {
				unlock()
			}
			t.Fatal("read fence accepted substituted principal/space")
		}
	}
}
