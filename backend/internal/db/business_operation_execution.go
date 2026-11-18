package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Claim is committed before any target write. An executing/unknown request can
// only be reconciled by a read of the target ledger, never automatically sent again.
func (r *BusinessOperationRepository) Claim(ctx context.Context, id, version int64) (*OperationRequest, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	x, e := r.request(ctx, tx, id, true)
	if e != nil {
		return nil, e
	}
	if x.UserID != ActorID(ctx) {
		return nil, ErrManagedDenied
	}
	if x.Status == "executed" {
		return x, nil
	}
	if x.Status != "approved" || x.Version != version {
		return nil, ErrManagedConflict
	}
	if _, e = r.checkRequest(ctx, tx, x, false); e != nil {
		return nil, e
	}
	if e = r.checkReviewer(ctx, tx, x); e != nil {
		return nil, e
	}
	e = tx.QueryRow(ctx, `UPDATE sys_operation_requests SET status='executing',execution_started_at=now(),version=version+1 WHERE id=$1 RETURNING version,execution_started_at`, id).Scan(&x.Version, &x.StartedAt)
	if e != nil {
		return nil, e
	}
	x.Status = "executing"
	return x, tx.Commit(ctx)
}
func (r *BusinessOperationRepository) checkReviewer(ctx context.Context, tx pgx.Tx, x *OperationRequest) error {
	if x.ReviewerID == nil || x.ReviewerMemberVersion == nil || *x.ReviewerID == x.UserID {
		return ErrManagedDenied
	}
	var valid bool
	if e := tx.QueryRow(ctx, `SELECT dbhub_operation_lock_member($1,$2,true)`, *x.ReviewerID, *x.ReviewerMemberVersion).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return ErrManagedConflict
	}
	return nil
}

type OperationExecutionGuard struct {
	repo    *BusinessOperationRepository
	Tx      pgx.Tx
	Request *OperationRequest
	Target  *OperationTarget
}

func (g *OperationExecutionGuard) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = g.Tx.Rollback(ctx)
}
func (r *BusinessOperationRepository) ExecutionGuard(ctx context.Context, id int64, reconcile bool) (*OperationExecutionGuard, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	x, e := r.request(ctx, tx, id, true)
	if e != nil {
		return nil, e
	}
	if x.UserID != ActorID(ctx) {
		return nil, ErrManagedDenied
	}
	if reconcile {
		if x.Status != "unknown" && x.Status != "executing" {
			return nil, ErrManagedConflict
		}
		if x.Status == "executing" && (x.StartedAt == nil || time.Since(*x.StartedAt) < 15*time.Second) {
			return nil, ErrManagedConflict
		}
	} else if x.Status != "executing" {
		return nil, ErrManagedConflict
	}
	// Reconciliation proves a historical outcome under current access, and is
	// allowed after the approval deadline. It never revives an expired approval.
	expiry := x.ExpiresAt
	if reconcile {
		x.ExpiresAt = time.Now().Add(time.Minute)
	}
	target, e := r.checkRequest(ctx, tx, x, false)
	x.ExpiresAt = expiry
	if e != nil {
		return nil, e
	}
	if !reconcile {
		if e = r.checkReviewer(ctx, tx, x); e != nil {
			return nil, e
		}
	}
	ok = true
	return &OperationExecutionGuard{r, tx, x, target}, nil
}
func (g *OperationExecutionGuard) Finish(ctx context.Context, receipt OperationReceipt) error {
	x := g.Request
	if receipt.OperationID != x.OperationID || receipt.ReviewerID == x.UserID || x.ReviewerID == nil || receipt.ReviewerID != *x.ReviewerID || receipt.AffectedRows != 1 || !sameOperationPreview(receipt.OperationPreview, x.Preview) || receipt.ExecutedAt.IsZero() {
		return ErrManagedConflict
	}
	encrypted, e := g.repo.encode(receipt)
	if e != nil {
		return e
	}
	hash := reportDigest(receipt)
	_, e = g.Tx.Exec(ctx, `UPDATE sys_operation_requests SET status='executed',encrypted_receipt=$2,receipt_sha256=$3,finished_at=now(),version=version+1 WHERE id=$1`, x.ID, encrypted, hash)
	if e != nil {
		return e
	}
	return g.Tx.Commit(ctx)
}
func (r *BusinessOperationRepository) RecordUncertain(ctx context.Context, id int64, definite bool) error {
	status := "unknown"
	if definite {
		status = "stale"
	}
	tag, e := r.pool.Exec(ctx, `UPDATE sys_operation_requests SET status=$3,version=version+1 WHERE id=$1 AND user_id=$2 AND status='executing'`, id, ActorID(ctx), status)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrManagedConflict
	}
	return nil
}
func (r *BusinessOperationRepository) MissingReceipt(ctx context.Context, id int64) error {
	// A delayed or disconnected transaction can still commit. Absence is not
	// proof of rollback, and must never enable the execute action again.
	_, e := r.pool.Exec(ctx, `UPDATE sys_operation_requests SET status='unknown',version=version+1 WHERE id=$1 AND user_id=$2 AND status='executing'`, id, ActorID(ctx))
	return e
}

var ErrOperationUncertain = errors.New("执行结果尚未核实，请刷新并核实目标库回执；不要重复提交同一业务变更")

func OperationFailure(err error) error {
	return fmt.Errorf("%w: 当前记录、权限或业务条件已变化，请重新预览并审批", ErrManagedConflict)
}
