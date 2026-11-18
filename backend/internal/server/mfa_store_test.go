package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
)

func TestMFAStoreAtomicityAndSessionRevocation(t *testing.T) {
	a := setupAPI(t)
	repo := a.deps.UserRepo
	ctx := context.Background()
	u, err := repo.FindByUsername(ctx, "readonly")
	if err != nil {
		t.Fatal(err)
	}
	pending := testRequestID()
	hashes := []string{}
	for i := 1; i <= 10; i++ {
		hashes = append(hashes, fmt.Sprintf("%064x", i))
	}
	if err = repo.StartMFASetup(ctx, u, pending, "encrypted-test-fixture", time.Now().Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	state, err := repo.MFAState(ctx, u.ID)
	if err != nil || state.Enabled || state.PendingID != pending {
		t.Fatal("enrollment state unavailable")
	}
	if err = repo.EnableMFA(ctx, u, testRequestID(), state.Version, 1000, hashes); !errors.Is(err, auth.ErrMFAInvalid) {
		t.Fatal("wrong enrollment accepted")
	}
	if err = repo.EnableMFA(ctx, u, pending, state.Version, 1000, hashes); err != nil {
		t.Fatal(err)
	}
	a.call("GET", "/auth/me", a.readonly, nil, 401)
	if err = repo.CreateMFAChallenge(ctx, u, strings.Repeat("a", 64), time.Now().Add(time.Minute)); !errors.Is(err, auth.ErrMFAInvalid) {
		t.Fatal("old password/MFA snapshot accepted")
	}
	u, err = repo.FindByUsername(ctx, "readonly")
	if err != nil || u.AuthVersion != 1 {
		t.Fatal("MFA enable did not rotate identity version")
	}
	if err = repo.CreateMFAChallenge(ctx, u, strings.Repeat("a", 64), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err = repo.AttemptMFAChallenge(ctx, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = repo.AttemptMFAChallenge(ctx, strings.Repeat("a", 64)); !errors.Is(err, auth.ErrMFAInvalid) {
		t.Fatal("challenge budget not enforced")
	}
	challenge := func(symbol string) string {
		hash := strings.Repeat(symbol, 64)
		if e := repo.CreateMFAChallenge(ctx, u, hash, time.Now().Add(time.Minute)); e != nil {
			t.Fatal(e)
		}
		if _, e := repo.AttemptMFAChallenge(ctx, hash); e != nil {
			t.Fatal(e)
		}
		return hash
	}
	state, _ = repo.MFAState(ctx, u.ID)
	token := challenge("b")
	if err = repo.ConsumeMFAChallenge(ctx, u, token, state.Version, 1001, ""); err != nil {
		t.Fatal(err)
	}
	if err = repo.ConsumeMFAChallenge(ctx, u, token, state.Version, 1001, ""); !errors.Is(err, auth.ErrMFAInvalid) {
		t.Fatal("challenge reused")
	}
	state, _ = repo.MFAState(ctx, u.ID)
	token = challenge("c")
	if err = repo.ConsumeMFAChallenge(ctx, u, token, state.Version, 1001, ""); !errors.Is(err, auth.ErrMFAInvalid) {
		t.Fatal("TOTP counter reused")
	}
	if err = repo.ConsumeMFAChallenge(ctx, u, token, state.Version, -1, hashes[0]); err != nil {
		t.Fatal(err)
	}
	state, _ = repo.MFAState(ctx, u.ID)
	if state.RecoveryCount != 9 {
		t.Fatal("recovery code was not consumed")
	}
	if err = repo.ConsumeMFAChallenge(ctx, u, challenge("d"), state.Version, -1, hashes[0]); !errors.Is(err, auth.ErrMFAInvalid) {
		t.Fatal("recovery code reused")
	}
	left, right := challenge("e"), challenge("f")
	state, _ = repo.MFAState(ctx, u.ID)
	var group sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, hash := range []string{left, right} {
		group.Add(1)
		go func(token string) {
			defer group.Done()
			outcomes <- repo.ConsumeMFAChallenge(ctx, u, token, state.Version, -1, hashes[1])
		}(hash)
	}
	group.Wait()
	close(outcomes)
	successes := 0
	for result := range outcomes {
		if result == nil {
			successes++
		} else if !errors.Is(result, auth.ErrMFAInvalid) {
			t.Fatal(result)
		}
	}
	if successes != 1 {
		t.Fatal("concurrent recovery consumption did not have exactly one winner")
	}
	// Even a metadata table reader cannot read identity secrets through RLS.
	role := "mfa_scope_" + strings.ReplaceAll(testRequestID(), "-", "")[:12]
	if _, err = a.pool.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` NOLOGIN NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = a.pool.Exec(ctx, `DROP OWNED BY `+pgx.Identifier{role}.Sanitize())
		_, _ = a.pool.Exec(ctx, `DROP ROLE `+pgx.Identifier{role}.Sanitize())
	})
	var schema string
	if err = a.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pool.Exec(ctx, `GRANT USAGE ON SCHEMA `+pgx.Identifier{schema}.Sanitize()+` TO `+pgx.Identifier{role}.Sanitize()+`;GRANT SELECT ON sys_user_mfa TO `+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sys_user_mfa`).Scan(&visible); err != nil || visible != 0 {
		t.Fatal("identity secrets visible to a runtime table reader")
	}
	_ = tx.Rollback(ctx)
	state, _ = repo.MFAState(ctx, u.ID)
	if err = repo.DisableMFA(ctx, u, state.Version, 1002, ""); err != nil {
		t.Fatal(err)
	}
	state, _ = repo.MFAState(ctx, u.ID)
	fresh, err := repo.FindByUsername(ctx, "readonly")
	if err != nil || fresh.AuthVersion != 2 || state.Enabled || state.EncryptedSecret != "" || state.RecoveryCount != 0 {
		t.Fatal("MFA disable did not revoke and clear the factors")
	}
}
