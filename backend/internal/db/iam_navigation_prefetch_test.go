package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
)

func holdNavigationPool(t *testing.T, users *UserRepository) func() {
	t.Helper()
	pool := users.pool.maintenance
	connections := make([]*pgxpool.Conn, 0, pool.Config().MaxConns)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range pool.Config().MaxConns {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			for _, held := range connections {
				held.Release()
			}
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	return func() {
		for _, conn := range connections {
			conn.Release()
		}
		connections = nil
	}
}

func TestIAMJoinedNavigationWaitsForCurrentDirectoryWithoutBlockingIdentity(t *testing.T) {
	pool, users, ctx := iamTestDatabase(t)
	release := holdNavigationPool(t, users)
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	user, joined, err := users.FindIdentityAndWorkspaceDirectory(ctx, "developer")
	if err != nil || user.ID != 2 {
		t.Fatal("current identity depends on the queued navigation read", err)
	}
	joined = auth.WithClaims(joined, &auth.Claims{Subject: 2, Role: "developer"})
	type result struct {
		spaces []Workspace
		err    error
	}
	done := make(chan result, 1)
	go func() { spaces, err := users.WorkspacesDirectory(joined); done <- result{spaces, err} }()
	select {
	case <-done:
		t.Fatal("directory returned before its current read was available")
	case <-time.After(20 * time.Millisecond):
	}
	if _, err = pool.Exec(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,2,$1::uuid,'member_freeze','queued navigation current qualification')`, dashboardUUID()); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case got := <-done:
		if got.err != nil || len(got.spaces) != 0 {
			t.Fatal("queued navigation retained qualification from the identity read", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("navigation did not finish after its connection became available")
	}
}

func TestIAMJoinedNavigationCancellationDoesNotLeaveAWaitingConsumer(t *testing.T) {
	_, users, ctx := iamTestDatabase(t)
	release := holdNavigationPool(t, users)
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	_, joined, err := users.FindIdentityAndWorkspaceDirectory(ctx, "developer")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	joined = auth.WithClaims(joined, &auth.Claims{Subject: 2, Role: "developer"})
	cancel()
	if _, err = users.WorkspacesDirectory(joined); !errors.Is(err, context.Canceled) {
		t.Fatal("queued navigation ignored request cancellation", err)
	}
}
