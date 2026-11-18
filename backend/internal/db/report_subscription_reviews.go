package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The scheduler, manual admission and delivery share the confirmation gate.
const subscriptionReviewReady = `(s.review_stamp='' OR s.review_until>statement_timestamp() AND EXISTS (
	SELECT 1 FROM sys_managed_reports m WHERE m.workspace_id=s.workspace_id AND m.id=s.report_id
	AND m.publication_no=s.publication_no AND m.active_release_id IS NOT NULL))`

func (r *ReportSubscriptionRepository) subscriptionReviewEnrolled(ctx context.Context, q reportQueryer, x *ManagedExecution) (bool, error) {
	var allowed bool
	err := q.QueryRow(ctx, `SELECT b.isolation_id=$2 AND EXISTS (
		SELECT 1 FROM sys_iam_isolation_guard g WHERE g.isolation_id=b.isolation_id AND g.database_name=current_database())
		AND EXISTS(SELECT 1 FROM sys_report_review_checks c WHERE c.workspace_id=s.workspace_id AND c.review_id=$3
			AND (NOT c.requires_data OR c.data_reviewer_id IS NOT NULL))
		FROM sys_iam_isolated_subscription_reviews s LEFT JOIN sys_iam_isolated_business_reports b USING(workspace_id,report_id)
		WHERE s.workspace_id=dbhub_workspace() AND s.report_id=$1`, x.ReportID, r.reports.phase2ReportIsolation, x.ReviewID).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err == nil && (!allowed || x.businessBinding == "") {
		return false, ErrManagedDenied
	}
	return allowed, err
}

func subscriptionConfirmationStamp(x *ManagedExecution, params map[string]any) string {
	return reportDigest([]any{x.ReportID, x.PublicationNo, x.ReviewID, x.Hash, x.AccountID,
		x.AccountVersion, x.Generation, x.BusinessPolicyStamp(), params})
}

func (r *ReportSubscriptionRepository) checkSubscriptionConfirmation(ctx context.Context, definition ReportSubscriptionDefinition, stamp string) error {
	x, err := r.reports.ResolvePublished(ctx, definition.ReportID, definition.PublicationNo)
	if err != nil {
		return err
	}
	enrolled, err := r.subscriptionReviewEnrolled(ctx, r.pool, x)
	if err != nil {
		return err
	}
	if !enrolled {
		return ErrManagedConflict
	}
	if err = r.reports.PrepareBusinessReport(ctx, x); err != nil {
		return err
	}
	params, err := ResolveReportParameters(x.Definition.Parameters, definition.Params, time.Now())
	if err != nil || subscriptionConfirmationStamp(x, params) != stamp {
		return ErrManagedConflict
	}
	if _, err = x.BusinessPlan.BindReportParameters(params); err != nil {
		return ErrManagedConflict
	}
	return r.reports.CheckExecution(ctx, x)
}

// ConfirmPersonalSubscription is an explicit owner decision on one saved version.
func (r *ReportSubscriptionRepository) ConfirmPersonalSubscription(ctx context.Context, key string, id, version int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "subscription-confirm", []int64{id, version}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		var encrypted, stamp string
		var current int64
		err := tx.QueryRow(ctx, `SELECT encrypted_definition,review_stamp,version FROM sys_report_subscriptions
			WHERE workspace_id=dbhub_workspace() AND id=$1 AND user_id=dbhub_actor() AND NOT archived FOR UPDATE`,
			id).Scan(&encrypted, &stamp, &current)
		if err != nil {
			return out, err
		}
		if stamp == "" || current != version {
			return out, ErrAutomationConflict
		}
		var definition ReportSubscriptionDefinition
		if err = r.decode(encrypted, &definition); err != nil {
			return out, err
		}
		if err = r.checkSubscriptionConfirmation(ctx, definition, stamp); err != nil {
			if errors.Is(err, ErrNotFound) {
				return out, ErrManagedConflict
			}
			return out, err
		}
		err = tx.QueryRow(ctx, `UPDATE sys_report_subscriptions SET review_until=statement_timestamp()+interval '90 days',
			enabled=true,version=version+1,member_version=dbhub_member_epoch(),next_run_at=statement_timestamp(),
			last_status='',updated_at=statement_timestamp() WHERE id=$1 AND user_id=dbhub_actor() RETURNING version`, id).
			Scan(&out.Version)
		return out, err
	})
}
