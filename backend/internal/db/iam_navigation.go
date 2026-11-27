package db

import (
	"context"
	"sync/atomic"

	"github.com/user/dbhub/internal/auth"
)

type navigationReadKey struct{}
type navigationRead struct {
	subject  int64
	ready    <-chan navigationResult
	consumed atomic.Bool
}

type navigationResult struct {
	spaces      []Workspace
	hasRecovery bool
	err         error
}

// FindIdentityAndWorkspaceDirectory reads identity first, then starts a current
// navigation read while normal session validation runs. No session read moves
// before identity, and no directory result reaches a handler before validation.
// The directory is single-use and subject-bound, never an IAM snapshot or an
// input to binding, admission, repository RLS or final delivery.
func (r *UserRepository) FindIdentityAndWorkspaceDirectory(ctx context.Context, username string) (*auth.User, context.Context, error) {
	u, err := r.FindByUsername(ctx, username)
	if err != nil {
		return nil, ctx, err
	}
	ready := make(chan navigationResult, 1)
	directory := &navigationRead{subject: u.ID, ready: ready}
	// The buffered result cannot strand a goroutine when authentication fails;
	// the SQL read also uses the original request's cancellation/deadline.
	go func() { ready <- r.readNavigation(ctx, u.ID) }()
	return u, context.WithValue(ctx, navigationReadKey{}, directory), nil
}

func (r *UserRepository) readNavigation(ctx context.Context, subject int64) navigationResult {
	spaces, recovery, err := r.workspaceDirectoryRows(ctx, subject, false)
	return navigationResult{spaces: spaces, hasRecovery: recovery, err: err}
}

func (r *UserRepository) joinedWorkspaceDirectory(ctx context.Context) ([]Workspace, bool, error) {
	read, _ := ctx.Value(navigationReadKey{}).(*navigationRead)
	if read == nil || read.subject != ActorID(ctx) || !read.consumed.CompareAndSwap(false, true) {
		return nil, false, nil
	}
	select {
	case result := <-read.ready:
		if result.err != nil {
			return nil, true, result.err
		}
		spaces, err := r.withRejoinedWorkspaces(ctx, result.spaces)
		if err == nil {
			spaces, err = r.withRecoveryWorkspaces(ctx, spaces, result.hasRecovery)
		}
		return spaces, true, err
	case <-ctx.Done():
		return nil, true, ctx.Err()
	}
}
