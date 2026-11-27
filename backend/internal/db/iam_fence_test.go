package db

import (
	"context"
	"testing"
	"time"

	"github.com/user/dbhub/internal/auth"
)

func TestIAMRequestFenceRechecksCurrentEpochAndExpiry(t *testing.T) {
	for _, change := range []string{"epoch", "expiry"} {
		t.Run(change, func(t *testing.T) {
			pool, users, ctx := iamTestDatabase(t)
			if change == "expiry" {
				if _, e := pool.Exec(ctx, `UPDATE sys_workspace_members SET expires_at=statement_timestamp()+interval '1 second' WHERE workspace_id=1 AND user_id=1`); e != nil {
					t.Fatal(e)
				}
			}
			ctx, e := users.BindWorkspace(ctx, 1)
			if e != nil {
				t.Fatal(e)
			}
			verify, release, e := users.IAMRequestFence(ctx, false)
			if e != nil {
				t.Fatal(e)
			}
			defer release()
			if e = verify(); e != nil {
				t.Fatal("unchanged current membership was denied", e)
			}
			if change == "epoch" {
				// Simulate the persisted tightening epoch while the shared fence
				// remains held. Delivery must use its next statement's snapshot.
				if _, e = pool.Exec(context.Background(), `UPDATE sys_iam_workspace_state SET authorization_epoch=authorization_epoch+1 WHERE workspace_id=1`); e != nil {
					t.Fatal(e)
				}
			} else {
				claims, _ := auth.ClaimsFromContext(ctx)
				// The fixture expiry is defined by the database clock. Wait for
				// that same deadline; a monotonic Go sleep alone cannot establish
				// this precondition if the host wall clock is corrected mid-wait.
				if _, e = pool.Exec(context.Background(), `SELECT pg_sleep(GREATEST(EXTRACT(epoch FROM $1::timestamptz-clock_timestamp()),0)+0.02)`, *claims.IAM.ExpiresAt); e != nil {
					t.Fatal(e)
				}
				var reached bool
				if e = pool.QueryRow(context.Background(), `SELECT clock_timestamp()>=$1::timestamptz`, *claims.IAM.ExpiresAt).Scan(&reached); e != nil || !reached {
					t.Fatal("database expiry precondition not reached", e)
				}
			}
			if e = verify(); e == nil {
				var databaseNow time.Time
				var memberUntil *time.Time
				if diagnostic := pool.QueryRow(context.Background(), `SELECT clock_timestamp(),expires_at FROM sys_workspace_members WHERE workspace_id=1 AND user_id=1`).Scan(&databaseNow, &memberUntil); diagnostic == nil {
					t.Log("expiry diagnostic", "go_now", time.Now().UTC(), "database_now", databaseNow, "member_until", memberUntil)
				}
				t.Fatal("delivery reused admission authorization after", change)
			}
		})
	}
}

func TestIAMCurrentRejectsExpiredAdmissionWithQualifiedMembership(t *testing.T) {
	pool, users, ctx := iamTestDatabase(t)
	ctx, err := users.BindWorkspace(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var current bool
	if err = pool.QueryRow(ctx, `SELECT active AND (expires_at IS NULL OR expires_at>clock_timestamp()) FROM sys_workspace_members WHERE workspace_id=1 AND user_id=1`).Scan(&current); err != nil || !current {
		t.Fatal("fixture must retain current database membership", err)
	}
	// A server admission may impose an earlier ceiling than current membership.
	// Neither requalification nor an in-flight renewal can extend that request.
	claims, _ := auth.ClaimsFromContext(ctx)
	copyClaims, snapshot := *claims, *claims.IAM
	ended := time.Now().Add(-time.Second)
	snapshot.ExpiresAt = &ended
	copyClaims.IAM = &snapshot
	expired := auth.WithClaims(ctx, &copyClaims)
	if err = users.IAMCurrent(expired); err == nil {
		t.Fatal("current membership extended an expired admission")
	}
	if _, release, e := users.IAMRequestFence(expired, false); e == nil {
		release()
		t.Fatal("request fence admitted an expired snapshot")
	}
}
