package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

func TestIAMAdmissionPlanMatchesOriginalScopeAndAction(t *testing.T) {
	for _, mode := range []string{"legacy", "target", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			pool, users, _ := iamTestDatabase(t)
			users.ConfigureIAMIsolation("unit-only", "admission-parity-test")
			for _, kind := range []string{"api", "database", "worker"} {
				if e := users.IAMHeartbeat(context.Background(), kind, "admission-parity-test"); e != nil {
					t.Fatal(e)
				}
			}
			if mode != "legacy" {
				iamTargetFixture(t, pool, users, 3, []string{"report.page", "report.view"})
				if mode == "rollback" {
					if _, e := pool.Exec(context.Background(), `UPDATE sys_iam_principal_modes SET mode='LEGACY_GUARDED',rollback_guard=true WHERE user_id=3`); e != nil {
						t.Fatal(e)
					}
				}
			}
			ctx := iamBound(t, users, 3)
			cases := []struct{ name, query string }{
				{"current", ""},
				{"empty-action", `SELECT set_config('dbhub.action_id','',true)`},
				{"zero-version-compatibility", `SELECT set_config('dbhub.member_version','0',true),set_config('dbhub.authorization_epoch','0',true),set_config('dbhub.config_version','0',true)`},
				{"allowed-action", `SELECT set_config('dbhub.action_id','report.view',true)`},
				{"unknown-action", `SELECT set_config('dbhub.action_id','unknown.action',true)`},
				{"missing-source", `SELECT set_config('dbhub.action_id','dashboard.view',true)`},
				{"role-mismatch", `SELECT set_config('dbhub.actor_role','admin',true)`},
				{"member-mismatch", `SELECT set_config('dbhub.member_version','999999',true)`},
				{"epoch-mismatch", `SELECT set_config('dbhub.authorization_epoch','999999',true)`},
				{"config-mismatch", `SELECT set_config('dbhub.config_version','999999',true)`},
				{"missing-bridge", `SELECT set_config('dbhub.runtime_bridge','old-bridge',true)`},
				{"other-space", `SELECT set_config('dbhub.workspace_id','999999',true)`},
				{"membership-expired", `UPDATE sys_workspace_members SET expires_at=statement_timestamp()-interval '1 second' WHERE workspace_id=1 AND user_id=3`},
				{"profile-expired", `UPDATE sys_iam_principal_modes SET valid_until=statement_timestamp()-interval '1 second' WHERE user_id=3`},
				{"source-expired", `UPDATE sys_iam_assignments SET valid_from=statement_timestamp()-interval '2 seconds',valid_until=statement_timestamp()-interval '1 second' WHERE subject_id=3`},
				{"dependency-disabled", `UPDATE sys_iam_capabilities SET runtime_enabled=false WHERE action_id='report.page'`},
				{"member-frozen", `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,3,'10000000-0000-0000-0000-000000000001','member_freeze','admission parity')`},
				{"action-prohibited", `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,action_id,reason) VALUES(1,1,3,'10000000-0000-0000-0000-000000000002','action_revoke','report.view','admission parity')`},
			}
			for _, capability := range iam.ReadCatalog().Capabilities {
				cases = append(cases, struct{ name, query string }{"catalog-" + capability.ID, ""})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					tx, e := pool.Begin(ctx)
					if e != nil {
						t.Fatal(e)
					}
					defer tx.Rollback(context.Background())
					if e = iamScope(ctx, tx); e != nil {
						t.Fatal(e)
					}
					if _, e = tx.Exec(ctx, `SELECT set_config('dbhub.action_id','report.view',true)`); e != nil {
						t.Fatal(e)
					}
					if tc.query != "" {
						if _, e = tx.Exec(ctx, tc.query); e != nil {
							t.Fatal(e)
						}
					}
					if strings.HasPrefix(tc.name, "catalog-") {
						if _, e = tx.Exec(ctx, `SELECT set_config('dbhub.action_id',$1,true)`, strings.TrimPrefix(tc.name, "catalog-")); e != nil {
							t.Fatal(e)
						}
					}
					var old, current bool
					e = tx.QueryRow(ctx, `SELECT dbhub_scope_allowed() AND
					 (COALESCE(current_setting('dbhub.action_id',true),'')='' OR NOT dbhub_iam_is_target(dbhub_actor()) OR dbhub_iam_has_action(dbhub_actor(),current_setting('dbhub.action_id',true))),
					 dbhub_iam_request_current()`).Scan(&old, &current)
					if e != nil || old != current {
						t.Fatal("admission changed original current authority", old, current, e)
					}
					if tc.name == "current" && !current {
						t.Fatal("valid member rejected")
					}
				})
			}
			// The combined admission helper must install exactly the same local
			// scope, and transaction rollback must never leak it to another user.
			conn, e := pool.Acquire(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Release()
			tx, e := conn.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if e = iamAdmitCurrent(ctx, tx); e != nil {
				t.Fatal(e)
			}
			var actor int64
			if e = tx.QueryRow(ctx, `SELECT dbhub_actor()`).Scan(&actor); e != nil || actor != 3 {
				t.Fatal("wrong admission principal", actor, e)
			}
			if e = tx.Rollback(ctx); e != nil {
				t.Fatal(e)
			}
			if e = conn.QueryRow(ctx, `SELECT COALESCE(dbhub_actor(),0)`).Scan(&actor); e != nil || actor != 0 {
				t.Fatal("admission scope leaked after rollback", actor, e)
			}
		})
	}
}

func TestIAMRequestFenceChecksExpiryAfterWaitingForLock(t *testing.T) {
	for _, expiry := range []string{"membership", "source"} {
		t.Run(expiry, func(t *testing.T) { iamFenceExpiryAfterLock(t, expiry) })
	}
}

func iamFenceExpiryAfterLock(t *testing.T, expiry string) {
	t.Helper()
	pool, users, _ := iamTestDatabase(t)
	prefix := ":iam:"
	query := `UPDATE sys_workspace_members SET expires_at=statement_timestamp()+interval '1 second' WHERE workspace_id=1 AND user_id=3 RETURNING expires_at`
	if expiry == "source" {
		iamTargetFixture(t, pool, users, 3, []string{"report.page", "report.view"})
		prefix = ":iam:target:"
		query = `UPDATE sys_iam_assignments SET valid_until=statement_timestamp()+interval '1 second' WHERE workspace_id=1 AND subject_id=3 RETURNING valid_until`
	}
	var deadline time.Time
	if e := pool.QueryRow(context.Background(), query).Scan(&deadline); e != nil {
		t.Fatal(e)
	}
	ctx := iamBound(t, users, 3)
	claims, _ := auth.ClaimsFromContext(ctx)
	copyClaims := *claims
	copyClaims.CurrentAction = "report.view"
	ctx = auth.WithClaims(ctx, &copyClaims)
	if expiry == "source" && claims.IAM.ExpiresAt != nil {
		t.Fatal("source test must exercise current SQL qualification without an earlier Go admission ceiling")
	}
	// The old statement_timestamp would still be valid when lock acquisition
	// starts. Admission has to begin a separate current statement after the wait.
	lock, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Rollback(context.Background())
	if _, e = lock.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||$1||'1',0))`, prefix); e != nil {
		t.Fatal(e)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, release, err := users.IAMRequestFence(waitCtx, false)
		if release != nil {
			release()
		}
		result <- err
	}()
	// Establish actual blocked admission, rather than assuming a sleep means
	// another goroutine reached the lock, before crossing the original deadline.
	for {
		var blocked bool
		if e = pool.QueryRow(waitCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE '%pg_advisory_xact_lock_shared%')`).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			break
		}
		select {
		case err := <-result:
			t.Fatal("fence did not wait", err)
		case <-waitCtx.Done():
			t.Fatal(waitCtx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if _, e = pool.Exec(waitCtx, `SELECT pg_sleep(GREATEST(EXTRACT(epoch FROM $1::timestamptz-clock_timestamp()),0)+0.02)`, deadline); e != nil {
		t.Fatal(e)
	}
	if e = lock.Rollback(waitCtx); e != nil {
		t.Fatal(e)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("fence retained pre-lock expiry")
		}
	case <-waitCtx.Done():
		t.Fatal(waitCtx.Err())
	}
}
