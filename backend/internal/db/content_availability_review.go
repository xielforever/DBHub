package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (r *ManagedReportRepository) restoreAvailability(ctx context.Context, tx pgx.Tx, o availabilityObject, s availabilityState, cmd ContentAvailabilityCommand) (ReportCommandResult, error) {
	var out ReportCommandResult
	var stopped, stopper int64
	var stamp string
	err := tx.QueryRow(ctx, `SELECT id,actor_id,basis_stamp FROM sys_content_availability_events WHERE `+sameAvailabilityObject+
		` AND action='emergency_stop' AND base_publication_no=$3`, o.report, o.dashboard, s.number-1).
		Scan(&stopped, &stopper, &stamp)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fmt.Errorf("%w: 没有与当前发布号匹配的应急停止", ErrManagedConflict)
	}
	if err != nil {
		return out, err
	}
	if stopper == ActorID(ctx) {
		return out, ErrManagedDenied
	}
	if stamp == "" {
		return out, fmt.Errorf("%w: 停止前的受众边界无法证明，请重新送审", ErrManagedConflict)
	}
	var review int64
	if o.report > 0 {
		err = tx.QueryRow(ctx, `SELECT review_id FROM sys_report_releases WHERE report_id=$1 AND number=$2`, o.report, s.number-1).Scan(&review)
	} else {
		err = tx.QueryRow(ctx, `SELECT review_id FROM sys_dashboard_releases WHERE dashboard_id=$1 AND number=$2 AND lifecycle_epoch=$3`,
			o.dashboard, s.number-1, s.epoch).Scan(&review)
	}
	if err != nil {
		return out, err
	}
	current, err := availabilityAudienceStamp(ctx, tx, o, review)
	if err != nil || current != stamp {
		if err != nil && !errors.Is(err, ErrManagedInput) && !errors.Is(err, ErrManagedDenied) {
			return out, err
		}
		return out, fmt.Errorf("%w: 停止前后的受众或用户组已变化，请重新送审", ErrManagedConflict)
	}
	if err = r.checkAvailabilityReview(ctx, tx, o, review, 0, true); err != nil {
		if errors.Is(err, ErrManagedInput) || errors.Is(err, ErrManagedDenied) || errors.Is(err, ErrManagedConflict) || errors.Is(err, ErrNotFound) {
			return out, fmt.Errorf("%w: 当前源、账号、数据契约、权限或依赖无法证明可恢复，请重新送审", ErrManagedConflict)
		}
		return out, err
	}
	if _, err = appendAvailabilityEvent(ctx, tx, o, cmd, stopped, stamp); err != nil {
		return out, err
	}
	return appendAvailabilityRelease(ctx, tx, o, s, "rollback", &review)
}

// Snapshot only the metadata that ordinary group changes could make differ
// from the actual audience just before the stop. A missing snapshot disables
// quick restore but cannot prevent the stop from committing.
func bestEffortStopAudience(ctx context.Context, tx pgx.Tx, o availabilityObject, review int64) (string, error) {
	bounded, err := tx.Begin(ctx)
	if err != nil {
		return "", err
	}
	var stamp string
	if _, err = bounded.Exec(ctx, `SET LOCAL statement_timeout = '500ms'`); err == nil {
		stamp, err = availabilityAudienceStamp(ctx, bounded, o, review)
	}
	if err != nil {
		stamp = ""
	}
	return stamp, bounded.Rollback(context.WithoutCancel(ctx))
}

func availabilityAudienceStamp(ctx context.Context, tx pgx.Tx, o availabilityObject, review int64) (string, error) {
	var scope struct {
		UserIDs  []int64 `json:"user_ids"`
		GroupIDs []int64 `json:"group_ids"`
	}
	var err error
	if o.report > 0 {
		err = tx.QueryRow(ctx, `SELECT manifest FROM sys_report_reviews WHERE id=$1 AND status='approved'`, review).Scan(&scope)
	} else {
		err = tx.QueryRow(ctx, `SELECT v.definition FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id
			WHERE rv.id=$1 AND rv.status='approved'`, review).Scan(&scope)
	}
	if err != nil {
		return "", err
	}
	if err = audience(ctx, tx, scope.UserIDs, scope.GroupIDs); err != nil {
		return "", err
	}
	members, err := reportAudienceMembers(ctx, tx, scope.UserIDs, scope.GroupIDs)
	if err != nil {
		return "", err
	}
	var groups json.RawMessage
	err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_object_agg(id::text,version),'{}') FROM sys_user_groups WHERE id=ANY($1::bigint[])`,
		nonNilIDs(scope.GroupIDs)).Scan(&groups)
	return reportDigest([]any{members, groups}), err
}

func (r *ManagedReportRepository) checkAvailabilityReview(ctx context.Context, tx pgx.Tx, o availabilityObject, review, requester int64, restoring bool) error {
	if o.dashboard > 0 {
		dash := ManagedDashboardRepository{pool: r.pool, reports: r}
		return dash.checkDashboardAvailabilityReview(ctx, tx, review, requester, restoring)
	}
	var accountID int64
	var hash string
	var manifest ReportManifest
	err := tx.QueryRow(ctx, `SELECT account_id,manifest,manifest_hash FROM sys_report_reviews
		WHERE id=$1 AND status='approved'`, review).Scan(&accountID, &manifest, &hash)
	if err != nil {
		return err
	}
	conn, _, account, err := r.loadAccount(ctx, tx, accountID)
	if err != nil {
		return err
	}
	x, submitter, err := reportReviewExecution(ctx, tx, review, manifest, hash, conn, account)
	if err != nil {
		return err
	}
	if !restoring {
		x.PublicationNo--
	}
	enrolled, err := r.useReportReviewPolicy(ctx, tx, x)
	if err != nil {
		return err
	}
	if !enrolled {
		return ErrManagedDenied
	}
	basis, row, _, err := r.reportReviewBoundary(ctx, tx, x)
	if err != nil {
		return err
	}
	if !restoring {
		_, err = reportReviewCoverage(ctx, tx, basis, row, ActorID(ctx), requester, false)
		return err
	}
	approved, err := readReportReviewPolicy(ctx, tx, review)
	if err != nil {
		return err
	}
	// The release number changes. Every other checked boundary must retain
	// its approved value, including the account and row/field policy versions.
	basis.Publication = approved.boundary.Publication
	if reportDigest(basis) != reportDigest(approved.boundary) {
		return ErrManagedConflict
	}
	_, err = reportReviewCoverage(ctx, tx, basis, row, ActorID(ctx), submitter, true)
	return err
}

func (r *ManagedDashboardRepository) checkDashboardAvailabilityReview(ctx context.Context, tx pgx.Tx, review, requester int64, restoring bool) error {
	var d DashboardDefinition
	err := tx.QueryRow(ctx, `SELECT v.definition FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id
		JOIN sys_managed_dashboards m ON m.id=rv.dashboard_id AND m.lifecycle_epoch=rv.lifecycle_epoch
		WHERE rv.id=$1 AND rv.status='approved'`, review).Scan(&d)
	if err != nil {
		return err
	}
	s, _, _, err := r.dashboardReviewBasis(ctx, tx, review, d)
	if err != nil {
		return err
	}
	if !restoring {
		s.reviewer, s.submitter = ActorID(ctx), requester
	} else {
		var eligible bool
		if err = tx.QueryRow(ctx, `SELECT dbhub_iam_can_review($1,'dashboard')`, s.reviewer).Scan(&eligible); err != nil {
			return err
		}
		if !eligible {
			return ErrManagedConflict
		}
	}
	refs, err := r.checkDefinition(ctx, tx, d, s.reviewer)
	if err != nil {
		return err
	}
	basis, _, err := r.buildDashboardReviewBoundary(ctx, tx, s, refs, "", 0, restoring)
	if err != nil || !restoring {
		return err
	}
	approved, err := readDashboardReviewPolicy(ctx, tx, review, s.assignment)
	if err != nil {
		return err
	}
	basis.Publication = approved.boundary.Publication
	if reportDigest(basis) != reportDigest(approved.boundary) {
		return ErrManagedConflict
	}
	return nil
}
