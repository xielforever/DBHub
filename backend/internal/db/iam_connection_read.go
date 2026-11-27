package db

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
)

type connectionReadKey struct{}
type connectionReadFence struct {
	mu                                  sync.Mutex
	pool                                *ScopedPool
	tx                                  pgx.Tx
	actor, space, member, epoch, config int64
	role, action                        string
	closed                              bool
}

// IAMConnectionReadFence keeps admission, tenant RLS reads and final delivery
// on one restricted connection. The existing lease pool and its limit are
// reused. Every read and final check still starts a fresh READ COMMITTED
// statement, and the advisory lock remains held through response publication.
func (r *UserRepository) IAMConnectionReadFence(ctx context.Context) (context.Context, func() error, func(), error) {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.IAM == nil || c.IAM.GovernanceOnly || r.pool.runtime == nil ||
		(c.CurrentAction != "connection.page" && (c.IAM.Target || c.CurrentAction != "")) {
		return ctx, nil, nil, ErrManagedDenied
	}
	tx, verify, release, err := r.iamFenceTxOn(ctx, r.pool.leases, true)
	if err != nil {
		return ctx, nil, nil, err
	}
	f := &connectionReadFence{pool: r.pool, tx: tx, actor: ActorID(ctx), space: WorkspaceID(ctx), role: c.Role, action: c.CurrentAction,
		member: c.IAM.MembershipVersion, epoch: c.IAM.AuthorizationEpoch, config: c.IAM.ConfigVersion}
	check := func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.closed {
			return ErrConnectionChanged
		}
		return verify()
	}
	done := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.closed {
			f.closed = true
			release()
		}
	}
	return context.WithValue(ctx, connectionReadKey{}, f), check, done, nil
}

func connectionReadTransaction(ctx context.Context, pool *ScopedPool) (pgx.Tx, func(), error) {
	f, _ := ctx.Value(connectionReadKey{}).(*connectionReadFence)
	if f == nil {
		return nil, nil, nil
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil, nil, ErrConnectionChanged
	}
	c, _ := auth.ClaimsFromContext(ctx)
	if f.pool != pool || f.actor != ActorID(ctx) || f.space != WorkspaceID(ctx) || c == nil || c.IAM == nil ||
		f.role != c.Role || f.action != c.CurrentAction || f.member != c.IAM.MembershipVersion ||
		f.epoch != c.IAM.AuthorizationEpoch || f.config != c.IAM.ConfigVersion {
		f.mu.Unlock()
		return nil, nil, ErrManagedDenied
	}
	return f.tx, f.mu.Unlock, nil
}
