package db

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

// The selected principal is explicit. An unconfirmed inventory entry never
// selects TARGET, and an expired TARGET entry never falls back to legacy.
func (r *UserRepository) bindIAM(ctx context.Context, w *Workspace) error {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, e = tx.Exec(ctx, `SELECT set_config('dbhub.workspace_id',$1,true),set_config('dbhub.actor_id',$2,true)`, fmt.Sprint(w.ID), fmt.Sprint(ActorID(ctx))); e != nil {
		return e
	}
	s := w.IAM
	var target bool
	var role string
	var expiry *time.Time
	var rollback bool
	var frozenCaps []string
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT target,data_role,profile_until,rollback_guard,legacy_caps,sources FROM dbhub_iam_binding($1)`, ActorID(ctx)).Scan(&target, &role, &expiry, &rollback, &frozenCaps, &raw)
	if e == pgx.ErrNoRows {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	if role == "" {
		return ErrNotFound
	}
	s.Target = target
	s.RollbackGuard = rollback
	s.LegacyDataRole = role
	w.Role = role
	w.Capabilities = frozenCaps
	s.LegacyCapabilities = append([]string{}, frozenCaps...)
	if (target || rollback) && expiry != nil && (s.ExpiresAt == nil || expiry.Before(*s.ExpiresAt)) {
		s.ExpiresAt = expiry
	}
	if !target {
		s.Mode = iam.LegacyGuarded
		if w.PermissionMode == iam.Shadow {
			s.Mode = iam.Shadow
		}
		return tx.Commit(ctx)
	}
	if expiry != nil && (s.ExpiresAt == nil || expiry.Before(*s.ExpiresAt)) {
		s.ExpiresAt = expiry
	}
	if e = json.Unmarshal(raw, &s.Sources); e != nil {
		return e
	}
	// Resolve the fresh finite source union once. SQL remains authoritative at
	// admission, each repository boundary and delivery; no permission cache is
	// added. Re-evaluating every recursive SQL capability function here repeats
	// the same qualification/source joins dozens of times per request.
	union := []string{}
	for _, source := range s.Sources {
		union = append(union, source.Actions...)
	}
	slices.Sort(union)
	union = slices.Compact(union)
	// The restriction set was just read with this authorization epoch. Admission
	// and delivery recheck that epoch and current SQL action; expiry never waits
	// for a cache refresh. Do not call a SQL function once per union element.
	e = tx.QueryRow(ctx, `SELECT ARRAY(SELECT action_id FROM sys_iam_capabilities WHERE action_id=ANY($1::text[]) AND scope='workspace' AND runtime_enabled AND NOT(action_id=ANY($3::text[])) AND (action_id<>'query.write' OR $2<>'readonly') ORDER BY action_id)`, union, role, s.DeniedActions).Scan(&s.Actions)
	if e != nil {
		return e
	}
	allowed := []string{}
	for _, action := range s.Actions {
		if iam.Allows(s, action, time.Now()) {
			allowed = append(allowed, action)
		}
	}
	s.Actions = allowed
	return tx.Commit(ctx)
}

func iamScope(ctx context.Context, tx pgx.Tx) error {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.IAM == nil {
		return ErrManagedDenied
	}
	_, e := tx.Exec(ctx, `SELECT set_config('dbhub.workspace_id',$1,true),set_config('dbhub.actor_id',$2,true),set_config('dbhub.actor_role',$3,true),set_config('dbhub.member_version',$4,true),set_config('dbhub.authorization_epoch',$5,true),set_config('dbhub.config_version',$6,true),set_config('dbhub.action_id',$7,true),set_config('dbhub.runtime_bridge',$8,true)`, fmt.Sprint(WorkspaceID(ctx)), fmt.Sprint(ActorID(ctx)), c.Role, fmt.Sprint(c.IAM.MembershipVersion), fmt.Sprint(c.IAM.AuthorizationEpoch), fmt.Sprint(c.IAM.ConfigVersion), c.CurrentAction, iam.RuntimeBridge)
	return e
}

// A revoke acknowledgement is ordered after already admitted operations. The
// lock spans target commits, result publication and pickup, across API replicas.
// Governance takes the exclusive counterpart in its own transaction.
func (r *UserRepository) IAMLease(ctx context.Context) (func(), error) {
	return r.iamLeaseOn(ctx, r.pool.leases)
}

// Cancellation/job inspection must remain reachable while execution leases are
// full. Reuse the existing short maintenance pool, with the same advisory key
// and current qualification, rather than increasing any pool's connection cap.
func (r *UserRepository) IAMControlLease(ctx context.Context) (func(), error) {
	return r.iamLeaseOn(ctx, r.pool.maintenance)
}
func (r *UserRepository) iamLeaseOn(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	if c, ok := auth.ClaimsFromContext(ctx); ok && c != nil && c.IAM != nil && c.IAM.GovernanceOnly {
		pool = r.pool.maintenance
	}
	_, release, e := r.iamFenceOn(ctx, pool)
	return release, e
}

// IAMRequestFence verifies delivery in a new READ COMMITTED statement on the
// existing admission transaction. It reuses a connection, not an earlier
// snapshot; expiry and authorization epochs are still checked after the handler.
func (r *UserRepository) IAMRequestFence(ctx context.Context, control bool) (func() error, func(), error) {
	pool := r.pool.leases
	if c, ok := auth.ClaimsFromContext(ctx); control || ok && c != nil && c.IAM != nil && c.IAM.GovernanceOnly {
		pool = r.pool.maintenance
	}
	return r.iamFenceOn(ctx, pool)
}
func (r *UserRepository) iamFenceOn(ctx context.Context, pool *pgxpool.Pool) (func() error, func(), error) {
	_, verify, release, err := r.iamFenceTxOn(ctx, pool)
	return verify, release, err
}

func (r *UserRepository) iamFenceTxOn(ctx context.Context, pool *pgxpool.Pool, readOnly ...bool) (pgx.Tx, func() error, func(), error) {
	if pool == nil {
		return nil, nil, nil, ErrWorkspaceRequired
	}
	prefix := ":iam:"
	if c, ok := auth.ClaimsFromContext(ctx); ok && c != nil && c.IAM != nil && c.IAM.Target {
		prefix = ":iam:target:"
	}
	// BeginQuery has no bind arguments. Its only substitutions are this fixed
	// internal prefix and an int64 formatted as decimal, never caller SQL/text.
	// Combine transaction start and locking, but keep every authority check in
	// a separate subsequent statement so waiting cannot preserve an old clock.
	access := ""
	if len(readOnly) > 0 && readOnly[0] {
		access = " READ ONLY"
	}
	begin := fmt.Sprintf("BEGIN ISOLATION LEVEL READ COMMITTED%s; SELECT pg_advisory_xact_lock_shared(hashtextextended(current_schema()||'%s'||'%d',0))", access, prefix, WorkspaceID(ctx))
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{BeginQuery: begin})
	if e != nil {
		return nil, nil, nil, e
	}
	done := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }
	// This statement starts after lock acquisition, never on its earlier snapshot.
	if e = iamAdmitCurrent(ctx, tx); e != nil {
		done()
		return nil, nil, nil, e
	}
	return tx, func() error { return iamCurrent(ctx, tx) }, done, nil
}

// Keep scope installation and admission in a single fresh statement. Recovery
// uses its existing restricted path; no ordinary action is borrowed from it.
func iamAdmitCurrent(ctx context.Context, tx pgx.Tx) error {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.IAM == nil {
		return ErrManagedDenied
	}
	if c.IAM.ExpiresAt != nil && !time.Now().Before(*c.IAM.ExpiresAt) {
		return ErrConnectionChanged
	}
	if c.IAM.GovernanceOnly {
		if e := iamScope(ctx, tx); e != nil {
			return e
		}
		return iamRecoveryCurrentTx(ctx, tx)
	}
	var valid bool
	e := tx.QueryRow(ctx, `SELECT dbhub_iam_runtime_admit($1,$2,$3,$4,$5,$6,$7,$8)`, WorkspaceID(ctx), ActorID(ctx), c.Role, c.IAM.MembershipVersion, c.IAM.AuthorizationEpoch, c.IAM.ConfigVersion, c.CurrentAction, iam.RuntimeBridge).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return ErrConnectionChanged
	}
	return nil
}

func iamCurrent(ctx context.Context, tx pgx.Tx) error {
	var valid bool
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.IAM == nil {
		return ErrManagedDenied
	}
	// Current database qualification cannot extend an admitted request's own
	// deadline. Keep this ceiling as well as the fresh SQL check below, so either
	// application or database expiry closes delivery when their clocks differ.
	if c.IAM.ExpiresAt != nil && !time.Now().Before(*c.IAM.ExpiresAt) {
		return ErrConnectionChanged
	}
	if c.IAM.GovernanceOnly {
		return iamRecoveryCurrentTx(ctx, tx)
	}
	e := tx.QueryRow(ctx, `SELECT dbhub_iam_runtime_current()`).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return ErrConnectionChanged
	}
	return nil
}
func (r *UserRepository) IAMCurrent(ctx context.Context) error {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	return iamAdmitCurrent(ctx, tx)
}

func iamExclusive(ctx context.Context, tx pgx.Tx) error {
	if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:target:'||$1,0))`, fmt.Sprint(WorkspaceID(ctx))); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:'||$1,0))`, fmt.Sprint(WorkspaceID(ctx)))
	return e
}

func (r *UserRepository) IAMRefreshMode(ctx context.Context, id string) (string, error) {
	var mode string
	e := r.pool.QueryRow(ctx, `SELECT mode FROM sys_dashboard_refreshes WHERE id=$1::uuid AND user_id=$2 AND expires_at>now()`, id, ActorID(ctx)).Scan(&mode)
	return mode, managedError(e)
}

func (r *UserRepository) IAMSourceTaskKind(ctx context.Context, id string) (string, error) {
	var kind string
	e := r.pool.QueryRow(ctx, `SELECT kind FROM sys_source_tasks WHERE id=$1::uuid AND actor_id=$2`, id, ActorID(ctx)).Scan(&kind)
	return kind, managedError(e)
}
