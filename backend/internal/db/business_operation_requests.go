package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (r *BusinessOperationRepository) StorePreview(ctx context.Context, target *OperationTarget, p OperationPreview) (*OperationPreview, error) {
	if p.WorkspaceID != WorkspaceID(ctx) || p.ActorID != ActorID(ctx) || p.Kind != target.Template.Kind || p.InstallationID != target.Account.InstallationID {
		return nil, ErrManagedConflict
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	current, e := r.target(ctx, tx, target.Template.ID, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	if current.Template.Version != target.Template.Version || current.Account.Version != target.Account.Version {
		return nil, ErrManagedConflict
	}
	// Expired previews contain private before/after data and have a short lifetime.
	if _, e = tx.Exec(ctx, `DELETE FROM sys_operation_previews p WHERE p.user_id=$1 AND p.expires_at<now() AND NOT EXISTS(SELECT 1 FROM sys_operation_requests r WHERE r.preview_id=p.id)`, ActorID(ctx)); e != nil {
		return nil, e
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_operation_previews WHERE user_id=$1 AND expires_at>now()`, ActorID(ctx)).Scan(&count); e != nil {
		return nil, e
	}
	if count >= 30 {
		return nil, ErrAutomationLimit
	}
	p.ID = dashboardUUID()
	encrypted, e := r.encode(p)
	if e != nil {
		return nil, e
	}
	var expiry time.Time
	e = tx.QueryRow(ctx, `INSERT INTO sys_operation_previews(id,user_id,template_id,template_version,account_version,encrypted_preview) VALUES($1::uuid,$2,$3,$4,$5,$6) RETURNING expires_at`, p.ID, ActorID(ctx), target.Template.ID, target.Template.Version, target.Account.Version, encrypted).Scan(&expiry)
	if e != nil {
		return nil, e
	}
	p.ExpiresAt = &expiry
	return &p, tx.Commit(ctx)
}
func (r *BusinessOperationRepository) Submit(ctx context.Context, key, preview, reason string) (ReportCommandResult, error) {
	if strings.TrimSpace(reason) == "" || len([]rune(reason)) > 1000 {
		return ReportCommandResult{}, fmt.Errorf("%w: 请填写不超过1000字的业务原因", ErrManagedInput)
	}
	return r.commands.command(ctx, key, "operation-submit", []string{preview, reason}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		var template, tv, av, mv int64
		var encrypted string
		e := tx.QueryRow(ctx, `SELECT template_id,template_version,account_version,member_version,encrypted_preview FROM sys_operation_previews WHERE id=$1::uuid AND user_id=$2 AND expires_at>now() FOR UPDATE`, preview, ActorID(ctx)).Scan(&template, &tv, &av, &mv, &encrypted)
		if e != nil {
			return out, managedError(e)
		}
		target, e := r.target(ctx, tx, template, ActorID(ctx))
		if e != nil {
			return out, e
		}
		if target.Template.Version != tv || target.Account.Version != av || mv != MemberEpoch(ctx) {
			return out, ErrManagedConflict
		}
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("operation-pending:%d:%d", WorkspaceID(ctx), ActorID(ctx))); e != nil {
			return out, e
		}
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_operation_requests WHERE user_id=$1 AND status IN('pending','approved','executing','unknown') AND expires_at>now()`, ActorID(ctx)).Scan(&count); e != nil {
			return out, e
		}
		if count >= 20 {
			return out, ErrAutomationLimit
		}
		e = tx.QueryRow(ctx, `INSERT INTO sys_operation_requests(operation_id,user_id,preview_id,template_id,kind,member_version,template_version,account_version,encrypted_preview,reason) VALUES($1::uuid,$2,$3::uuid,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(preview_id) DO NOTHING RETURNING id,version`, dashboardUUID(), ActorID(ctx), preview, template, target.Template.Kind, mv, tv, av, encrypted, reason).Scan(&out.ID, &out.Version)
		if e == pgx.ErrNoRows {
			return out, ErrManagedConflict
		}
		if e != nil {
			return out, e
		}
		return out, nil
	})
}

const operationRequestColumns = `r.id,r.operation_id::text,r.user_id,u.username,r.template_id,r.kind,r.member_version,r.template_version,r.account_version,r.encrypted_preview,r.reason,r.status,r.version,r.reviewer_id,r.reviewer_member_version,r.review_comment,r.encrypted_receipt,r.receipt_sha256,r.created_at,r.expires_at,r.execution_started_at`

func (r *BusinessOperationRepository) request(ctx context.Context, q reportQueryer, id int64, lock bool) (*OperationRequest, error) {
	query := `SELECT ` + operationRequestColumns + ` FROM sys_operation_requests r JOIN sys_users u ON u.id=r.user_id WHERE r.id=$1 AND (r.user_id=$2 OR dbhub_has_capability($2,'operation_review'))`
	if lock {
		query += ` FOR UPDATE OF r`
	}
	out := &OperationRequest{}
	var preview, receipt string
	e := q.QueryRow(ctx, query, id, ActorID(ctx)).Scan(&out.ID, &out.OperationID, &out.UserID, &out.UserName, &out.TemplateID, &out.Kind, &out.MemberVersion, &out.TemplateVersion, &out.AccountVersion, &preview, &out.Reason, &out.Status, &out.Version, &out.ReviewerID, &out.ReviewerMemberVersion, &out.ReviewComment, &receipt, &out.ReceiptHash, &out.CreatedAt, &out.ExpiresAt, &out.StartedAt)
	if e != nil {
		return nil, managedError(e)
	}
	if e = r.decode(preview, &out.Preview); e != nil {
		return nil, e
	}
	if receipt != "" {
		out.Receipt = &OperationReceipt{}
		if e = r.decode(receipt, out.Receipt); e != nil {
			return nil, e
		}
		if reportDigest(out.Receipt) != out.ReceiptHash {
			return nil, ErrManagedConflict
		}
	}
	return out, nil
}
func (r *BusinessOperationRepository) Request(ctx context.Context, id int64) (*OperationRequest, error) {
	return r.request(ctx, r.pool, id, false)
}
func (r *BusinessOperationRepository) Requests(ctx context.Context, all bool, limit, offset int) ([]OperationRequest, int64, error) {
	if all && !ActorCapability(ctx, "operation_review") {
		return nil, 0, ErrManagedDenied
	}
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_operation_requests WHERE ($1 OR user_id=$2)`, all, ActorID(ctx)).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT id FROM sys_operation_requests WHERE ($1 OR user_id=$2) ORDER BY id DESC LIMIT $3 OFFSET $4`, all, ActorID(ctx), limit, offset)
	if e != nil {
		return nil, 0, e
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, 0, e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, 0, e
	}
	out := []OperationRequest{}
	for _, id := range ids {
		x, e := r.Request(ctx, id)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, *x)
	}
	return out, total, nil
}
func (r *BusinessOperationRepository) checkRequest(ctx context.Context, tx pgx.Tx, x *OperationRequest, review bool) (*OperationTarget, error) {
	var valid bool
	if e := tx.QueryRow(ctx, `SELECT dbhub_operation_lock_member($1,$2,false)`, x.UserID, x.MemberVersion).Scan(&valid); e != nil {
		return nil, e
	}
	if !valid {
		return nil, ErrManagedConflict
	}
	if review {
		if e := operationCapability(ctx, tx, "operation_review"); e != nil {
			return nil, e
		}
		if x.UserID == ActorID(ctx) {
			return nil, fmt.Errorf("%w: 不得审批自己的业务操作", ErrManagedDenied)
		}
	}
	target, e := r.target(ctx, tx, x.TemplateID, x.UserID)
	if e != nil {
		return nil, e
	}
	if target.Template.Version != x.TemplateVersion || target.Account.Version != x.AccountVersion || !x.ExpiresAt.After(time.Now()) {
		return nil, ErrManagedConflict
	}
	return target, nil
}
func (r *BusinessOperationRepository) ReviewTarget(ctx context.Context, id, version int64) (*OperationRequest, *OperationTarget, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	x, e := r.request(ctx, tx, id, false)
	if e != nil {
		return nil, nil, e
	}
	if x.Status != "pending" || x.Version != version {
		return nil, nil, ErrManagedConflict
	}
	target, e := r.checkRequest(ctx, tx, x, true)
	return x, target, e
}
func (r *BusinessOperationRepository) StoreValidation(ctx context.Context, id, version int64, p OperationPreview) (string, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	x, e := r.request(ctx, tx, id, true)
	if e != nil {
		return "", e
	}
	if x.Version != version || x.Status != "pending" || !sameOperationPreview(x.Preview, p) {
		return "", ErrManagedConflict
	}
	if _, e = r.checkRequest(ctx, tx, x, true); e != nil {
		return "", e
	}
	key := dashboardUUID()
	_, e = tx.Exec(ctx, `INSERT INTO sys_operation_validations(id,request_id,reviewer_id,request_version) VALUES($1::uuid,$2,$3,$4)`, key, id, ActorID(ctx), version)
	if e != nil {
		return "", e
	}
	return key, tx.Commit(ctx)
}
func (r *BusinessOperationRepository) Decide(ctx context.Context, key string, id, version int64, decision, validation, comment string, confirmed bool) (ReportCommandResult, error) {
	if (decision != "approved" && decision != "rejected") || strings.TrimSpace(comment) == "" || len([]rune(comment)) > 1000 {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.commands.command(ctx, key, "operation-decision", []any{id, version, decision, validation, comment, confirmed}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		x, e := r.request(ctx, tx, id, true)
		if e != nil {
			return out, e
		}
		if x.Status != "pending" || x.Version != version {
			return out, ErrManagedConflict
		}
		if e = operationCapability(ctx, tx, "operation_review"); e != nil {
			return out, e
		}
		if x.UserID == ActorID(ctx) {
			return out, ErrManagedDenied
		}
		if decision == "approved" {
			if !confirmed {
				return out, fmt.Errorf("%w: 请确认冻结的变更前后值", ErrManagedInput)
			}
			if _, e = r.checkRequest(ctx, tx, x, true); e != nil {
				return out, e
			}
			var valid bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_operation_validations WHERE id=$1::uuid AND request_id=$2 AND request_version=$3 AND reviewer_id=$4 AND created_at>now()-interval '1 minute')`, validation, id, version, ActorID(ctx)).Scan(&valid); e != nil {
				return out, e
			}
			if !valid {
				return out, ErrManagedConflict
			}
		}
		e = tx.QueryRow(ctx, `UPDATE sys_operation_requests SET status=$2,reviewer_id=$3,reviewer_member_version=dbhub_member_epoch(),review_comment=$4,version=version+1 WHERE id=$1 RETURNING version`, id, decision, ActorID(ctx), comment).Scan(&out.Version)
		if e != nil {
			return out, e
		}
		return out, nil
	})
}
