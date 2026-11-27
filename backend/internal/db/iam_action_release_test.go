package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
)

func iamBanFixture(t *testing.T, u *UserRepository, delegation string, uid int64) int64 {
	t.Helper()
	u.ConfigureIAMIsolation("unit-only", "action-release-test")
	for _, kind := range []string{"api", "database", "worker"} {
		if e := u.IAMHeartbeat(context.Background(), kind, "action-release-test"); e != nil {
			t.Fatal(e)
		}
	}
	ctx := iamBound(t, u, 1)
	raw, e := u.IAMRestrict(ctx, IAMRestrictionCommand{RequestID: dashboardUUID(), Baseline: iamBase(ctx), DelegationID: delegation, DelegationVersion: 1, Kind: "action.revoke", TargetID: uid, Action: "report.view", Reason: "isolated prohibition acceptance"})
	if e != nil {
		t.Fatal(e)
	}
	var receipt struct {
		Sequence int64 `json:"sequence"`
	}
	if e = json.Unmarshal(raw, &receipt); e != nil || receipt.Sequence == 0 {
		t.Fatal(string(raw), e)
	}
	return receipt.Sequence
}
func iamReleaseCommand(t *testing.T, u *UserRepository, delegation string, sequence int64, until time.Time) IAMProposalCommand {
	t.Helper()
	return IAMProposalCommand{RequestID: dashboardUUID(), Baseline: iamBase(iamBound(t, u, 1)), DelegationID: delegation, DelegationVersion: 1, ValidUntil: until, Patch: IAMChange{Kind: "action_release", SubjectID: 3, ActionID: "report.view", RevocationSequence: sequence, Reason: "isolated bounded restoration", ValidUntil: until}}
}
func iamReleaseSubmit(t *testing.T, u *UserRepository, p IAMProposalCommand, reviewDelegation string) IAMReviewCommand {
	t.Helper()
	if _, e := u.IAMPreview(iamBound(t, u, 1), p); e != nil {
		t.Fatal(e)
	}
	raw, e := u.IAMPropose(iamBound(t, u, 1), p)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := u.IAMPropose(iamBound(t, u, 1), p)
	if e != nil || string(raw) != string(replay) {
		t.Fatal("proposal replay", e)
	}
	var value struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &value)
	return IAMReviewCommand{RequestID: dashboardUUID(), ProposalID: value.ID, Version: 1, Decision: "approve", DelegationID: reviewDelegation, DelegationVersion: 1, Comment: "independent finite release"}
}
func iamReleaseAllowed(t *testing.T, pool *pgxpool.Pool, u *UserRepository, want bool) context.Context {
	t.Helper()
	ctx := iamBound(t, u, 3)
	c, _ := auth.ClaimsFromContext(ctx)
	if auth.HasAction(c, "report.view") != want || c.Role != "readonly" {
		t.Fatal("Go release/data identity mismatch", want, c.IAM)
	}
	tx, e := u.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT dbhub_iam_has_action(3,'report.view')`).Scan(&allowed); e != nil || allowed != want {
		t.Fatal("SQL release mismatch", allowed, want, e)
	}
	return ctx
}

func TestIAMActionReleaseIndependentAtomicBoundedAndMonotonic(t *testing.T) {
	for _, mode := range []string{"legacy", "target"} {
		t.Run(mode, func(t *testing.T) {
			pool, u, author, reviewer := iamGovernanceFixture(t)
			if mode == "target" {
				iamTargetFixture(t, pool, u, 3, []string{"report.page", "report.view"})
			}
			sequence := iamBanFixture(t, u, author, 3)
			iamReleaseAllowed(t, pool, u, false)
			p := iamReleaseCommand(t, u, author, sequence, time.Now().Add(time.Hour).UTC())
			review := iamReleaseSubmit(t, u, p, reviewer)
			iamReleaseAllowed(t, pool, u, false)
			self := review
			self.DelegationID = author
			if _, e := u.IAMReview(iamBound(t, u, 1), self); !errors.Is(e, ErrManagedDenied) {
				t.Fatal("release requester self review", e)
			}
			if _, e := u.IAMReview(iamBound(t, u, 3), review); !errors.Is(e, ErrManagedDenied) {
				t.Fatal("beneficiary self review", e)
			}
			outage := auditctx.WithCheckpoint(iamBound(t, u, 2), func() error { return errors.New("isolated audit unavailable") })
			if _, e := u.IAMReview(outage, review); e == nil {
				t.Fatal("release survived failed atomic audit")
			}
			var count int
			if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM sys_iam_action_releases`).Scan(&count); e != nil || count != 0 {
				t.Fatal("partial release", count, e)
			}
			approved, e := u.IAMReview(iamBound(t, u, 2), review)
			if e != nil {
				t.Fatal(e)
			}
			replay, e := u.IAMReview(iamBound(t, u, 2), review)
			if e != nil || string(replay) != string(approved) {
				t.Fatal("release review replay", e)
			}
			iamReleaseAllowed(t, pool, u, true)
			raw, e := u.IAMRead(iamBound(t, u, 1), "restrictions")
			if e != nil {
				t.Fatal(e)
			}
			var records []struct {
				Sequence     int64      `json:"sequence"`
				ReleaseUntil *time.Time `json:"release_until"`
			}
			if e = json.Unmarshal(raw, &records); e != nil || len(records) != 1 || records[0].Sequence != sequence || records[0].ReleaseUntil == nil {
				t.Fatal("release entry did not expose exact record", string(raw), e)
			}
			next := iamBanFixture(t, u, author, 3)
			if next <= sequence {
				t.Fatal("ban generation rewound")
			}
			iamReleaseAllowed(t, pool, u, false)
			p.RequestID = dashboardUUID()
			p.Baseline = iamBase(iamBound(t, u, 1))
			if _, e = u.IAMPropose(iamBound(t, u, 1), p); !errors.Is(e, ErrManagedConflict) {
				t.Fatal("old release covered a newer ban", e)
			}
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM sys_iam_action_releases`).Scan(&count); e != nil || count != 1 {
				t.Fatal("duplicate release", count, e)
			}
		})
	}
}

func TestIAMActionReleaseExpiryAndNewApprovalRenewal(t *testing.T) {
	pool, u, author, reviewer := iamGovernanceFixture(t)
	iamTargetFixture(t, pool, u, 3, []string{"report.page", "report.view"})
	sequence := iamBanFixture(t, u, author, 3)
	until := time.Now().Add(2 * time.Second).UTC()
	review := iamReleaseSubmit(t, u, iamReleaseCommand(t, u, author, sequence, until), reviewer)
	if _, e := u.IAMReview(iamBound(t, u, 2), review); e != nil {
		t.Fatal(e)
	}
	ctx := iamReleaseAllowed(t, pool, u, true)
	claims, _ := auth.ClaimsFromContext(ctx)
	copy := *claims
	copy.CurrentAction = "report.view"
	ctx = auth.WithClaims(ctx, &copy)
	verify, release, e := u.IAMRequestFence(ctx, false)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(time.Until(until) + 30*time.Millisecond)
	if e = verify(); e == nil {
		release()
		t.Fatal("delivery retained an expired ban release")
	}
	release()
	iamReleaseAllowed(t, pool, u, false)
	review = iamReleaseSubmit(t, u, iamReleaseCommand(t, u, author, sequence, time.Now().Add(time.Hour).UTC()), reviewer)
	if _, e = u.IAMReview(iamBound(t, u, 2), review); e != nil {
		t.Fatal(e)
	}
	iamReleaseAllowed(t, pool, u, true)
	var count int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM sys_iam_action_releases WHERE revocation_sequence=$1`, sequence).Scan(&count); e != nil || count != 2 {
		t.Fatal("renewal overwrote prior approval", count, e)
	}
}

func TestIAMActionReleaseFiniteDelegationAndExactRecord(t *testing.T) {
	pool, u, author, reviewer := iamGovernanceFixture(t)
	sequence := iamBanFixture(t, u, author, 3)
	original := iamReleaseCommand(t, u, author, sequence, time.Now().Add(time.Hour).UTC())
	for _, field := range []string{"sequence", "subject", "action", "reason", "sources", "scope"} {
		t.Run(field, func(t *testing.T) {
			p := original
			switch field {
			case "sequence":
				p.Patch.RevocationSequence++
			case "subject":
				p.Patch.SubjectID = 2
			case "action":
				p.Patch.ActionID = "dashboard.view"
			case "reason":
				p.Patch.Reason = ""
			case "sources":
				p.Patch.SourceIDs = []string{dashboardUUID()}
			case "scope":
				p.Patch.ActionID = "platform.identity.manage"
			}
			if _, e := u.IAMPreview(iamBound(t, u, 1), p); e == nil {
				t.Fatal("invalid release accepted", field)
			}
		})
	}
	if _, e := pool.Exec(context.Background(), `UPDATE sys_iam_delegations SET grantable_actions=array_remove(grantable_actions,'report.view') WHERE delegation_id=$1::uuid`, author); e != nil {
		t.Fatal(e)
	}
	if _, e := u.IAMPropose(iamBound(t, u, 1), original); !errors.Is(e, ErrManagedDenied) {
		t.Fatal("release exceeded action ceiling", e)
	}
	if _, e := pool.Exec(context.Background(), `UPDATE sys_iam_delegations SET grantable_actions=array_append(grantable_actions,'report.view') WHERE delegation_id=$1::uuid`, author); e != nil {
		t.Fatal(e)
	}
	review := iamReleaseSubmit(t, u, original, reviewer)
	if _, e := pool.Exec(context.Background(), `UPDATE sys_iam_delegations SET revoked_at=now() WHERE delegation_id=$1::uuid`, author); e != nil {
		t.Fatal(e)
	}
	if _, e := u.IAMReview(iamBound(t, u, 2), review); !errors.Is(e, ErrManagedDenied) {
		t.Fatal("revoked author delegation released prohibition", e)
	}
	// Owner-level fixtures cannot fabricate a release from an unrelated proposal.
	_, e := pool.Exec(context.Background(), `INSERT INTO sys_iam_action_releases(workspace_id,user_id,action_id,revocation_sequence,proposal_id,valid_until) VALUES(1,3,'report.view',$1,$2::uuid,now()+interval '1 hour')`, sequence, review.ProposalID)
	if pg, ok := e.(*pgconn.PgError); !ok || pg.Code != "42501" {
		t.Fatal("unapproved release row accepted", e)
	}
	iamReleaseAllowed(t, pool, u, false)
}

func TestIAMLegacyGovernanceDenialCannotBorrowAnotherAction(t *testing.T) {
	for _, blocked := range []string{"space.assign.manage", "space.assign.view", "space.page"} {
		for _, who := range []string{"requester", "reviewer"} {
			t.Run(blocked+"/"+who, func(t *testing.T) {
				_, u, author, reviewer := iamGovernanceFixture(t)
				sequence := iamBanFixture(t, u, author, 3)
				p := iamReleaseCommand(t, u, author, sequence, time.Now().Add(time.Hour).UTC())
				pending := iamReleaseSubmit(t, u, p, reviewer)
				actor, target, delegation := int64(2), int64(1), reviewer
				if who == "reviewer" {
					actor, target, delegation = 1, 2, author
				}
				ctx := iamBound(t, u, actor)
				if _, e := u.IAMRestrict(ctx, IAMRestrictionCommand{RequestID: dashboardUUID(), Baseline: iamBase(ctx), DelegationID: delegation, DelegationVersion: 1, Kind: "action.revoke", TargetID: target, Action: blocked, Reason: "isolated exact governance denial"}); e != nil {
					t.Fatal(e)
				}
				// A generic route can be entered through another action. The actual
				// governance operation must enforce its own cap and all dependencies.
				otherAction := func(uid int64) context.Context {
					ctx := iamBound(t, u, uid)
					c, _ := auth.ClaimsFromContext(ctx)
					copy := *c
					copy.CurrentAction = "space.roles.manage"
					return auth.WithClaims(ctx, &copy)
				}
				if who == "requester" {
					p = iamReleaseCommand(t, u, author, sequence, time.Now().Add(time.Hour).UTC())
					if _, e := u.IAMPreview(otherAction(1), p); !errors.Is(e, ErrManagedDenied) {
						t.Fatal("legacy admin borrowed another action for forbidden preview", blocked, e)
					}
					if _, e := u.IAMPropose(otherAction(1), p); !errors.Is(e, ErrManagedDenied) {
						t.Fatal("legacy admin submitted a forbidden release", blocked, e)
					}
				}
				if _, e := u.IAMReview(otherAction(2), pending); !errors.Is(e, ErrManagedDenied) {
					t.Fatal("approval ignored current requester/reviewer cap or dependency denial", blocked, who, e)
				}
			})
		}
	}
}
