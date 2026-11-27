package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type TeamDeliverySchedule struct {
	ID, Version, WorkspaceID, OwnerID, MemberVersion int64
	Slot                                             time.Time
}

const teamScheduleScope = ` FROM sys_report_team_subscriptions p
	JOIN sys_iam_isolated_business_reports b ON b.workspace_id=p.workspace_id AND b.report_id=p.report_id
	JOIN sys_iam_isolation_guard i ON i.isolation_id=b.isolation_id AND i.database_name=current_database()
	WHERE b.isolation_id=$1 AND p.status='approved' AND p.review_until>statement_timestamp()
	AND NOT EXISTS(SELECT 1 FROM sys_report_team_subscriptions n WHERE n.workspace_id=p.workspace_id AND n.previous_id=p.id)`

// initializeTeamIntervals preserves active approvals made before the schedule
// column existed. The worker reads the same encrypted definition as review.
func (r *ReportSubscriptionRepository) initializeTeamIntervals(ctx context.Context) error {
	type frozen struct {
		ID, Version int64
		Definition  string
	}
	rows, err := r.pool.maintenance.Query(ctx, `SELECT p.id,p.version,p.encrypted_definition`+teamScheduleScope+
		` AND p.schedule_interval_minutes IS NULL ORDER BY p.id LIMIT 20`, r.reports.phase2ReportIsolation)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[frozen])
	if err != nil {
		return err
	}
	for _, item := range items {
		var definition ReportSubscriptionDefinition
		if err = r.decode(item.Definition, &definition); err != nil {
			return err
		}
		if _, err = r.pool.maintenance.Exec(ctx, `UPDATE sys_report_team_subscriptions SET schedule_interval_minutes=$3
			WHERE id=$1 AND version=$2 AND status='approved' AND schedule_interval_minutes IS NULL`, item.ID, item.Version, definition.IntervalMinutes); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReportSubscriptionRepository) DueTeamSchedules(ctx context.Context) ([]TeamDeliverySchedule, error) {
	if r.reports.phase2ReportIsolation == "" {
		return nil, nil
	}
	if err := r.initializeTeamIntervals(ctx); err != nil {
		return nil, err
	}
	rows, err := r.pool.maintenance.Query(ctx, `WITH slots AS (
		SELECT p.id,p.version,p.workspace_id,p.owner_id,p.owner_member_version,p.schedule_checked_at,
		date_bin(make_interval(mins=>p.schedule_interval_minutes),statement_timestamp(),p.decided_at) AS slot`+teamScheduleScope+`
		AND p.schedule_interval_minutes IS NOT NULL
		AND p.decided_at+make_interval(mins=>p.schedule_interval_minutes)<=statement_timestamp())
		SELECT id,version,workspace_id,owner_id,owner_member_version,slot FROM slots
		WHERE schedule_checked_at IS NULL OR schedule_checked_at<slot
		ORDER BY slot,id LIMIT 20`, r.reports.phase2ReportIsolation)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[TeamDeliverySchedule])
}

// OpenScheduledTeamPeriod uses the same owner, scope and recipient checks as
// explicit triggering. The unique slot also fences concurrent worker ticks.
func (r *ReportSubscriptionRepository) OpenScheduledTeamPeriod(ctx context.Context, schedule TeamDeliverySchedule) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = r.openTeamPeriod(ctx, tx, schedule.ID, schedule.Version, &schedule.Slot); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ReportSubscriptionRepository) MarkTeamScheduleChecked(ctx context.Context, schedule TeamDeliverySchedule) error {
	_, err := r.pool.maintenance.Exec(ctx, `UPDATE sys_report_team_subscriptions SET schedule_checked_at=$3
		WHERE workspace_id=$1 AND id=$2 AND version=$4 AND (schedule_checked_at IS NULL OR schedule_checked_at<$3)`,
		schedule.WorkspaceID, schedule.ID, schedule.Slot, schedule.Version)
	return err
}
