package db

import (
	"context"
	"fmt"
	"time"
)

func (r *ManagedReportRepository) AcquireExecutionLease(ctx context.Context, x *ManagedExecution) (func(), error) {
	if e := requireCurrentActor(ctx, r.pool); e != nil {
		return nil, e
	}
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(742651001)`); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM sys_dashboard_query_leases WHERE expires_at<now()`); e != nil {
		return nil, e
	}
	var global, workspace, user, account int
	e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE workspace_id=$1),count(*) FILTER(WHERE workspace_id=$1 AND user_id=$2),count(*) FILTER(WHERE workspace_id=$1 AND account_id=$3) FROM sys_dashboard_query_leases`, WorkspaceID(ctx), ActorID(ctx), x.AccountID).Scan(&global, &workspace, &user, &account)
	if e != nil {
		return nil, e
	}
	if global >= 64 || workspace >= 16 || user >= 4 || account >= 4 {
		return nil, fmt.Errorf("看板查询并发已满，请稍后重试")
	}
	lease := dashboardUUID()
	_, e = tx.Exec(ctx, `INSERT INTO sys_dashboard_query_leases(id,workspace_id,user_id,account_id) VALUES($1::uuid,$2,$3,$4)`, lease, WorkspaceID(ctx), ActorID(ctx), x.AccountID)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = r.pool.maintenance.Exec(cleanup, `DELETE FROM sys_dashboard_query_leases WHERE id=$1::uuid AND workspace_id=$2 AND user_id=$3`, lease, WorkspaceID(ctx), ActorID(ctx))
	}, nil
}
