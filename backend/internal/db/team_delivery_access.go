package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type TeamRecipientCommand struct {
	RecipientID int64  `json:"recipient_id"`
	Version     int64  `json:"version"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
}

func lockTeamRecipient(ctx context.Context, tx pgx.Tx, plan, recipient int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		fmt.Sprintf("team-recipient:%d:%d:%d", WorkspaceID(ctx), plan, recipient))
	return err
}

// ChangeTeamRecipient records a plan-local grant or revocation without
// granting report or data access. A replacement grant advances the version.
func (r *ReportSubscriptionRepository) ChangeTeamRecipient(ctx context.Context, key string, plan int64, in TeamRecipientCommand) (ReportCommandResult, error) {
	grant := in.Action == "grant"
	if in.RecipientID < 1 || in.Version < 0 || !grant && in.Action != "revoke" ||
		strings.TrimSpace(in.Reason) == "" || len([]rune(in.Reason)) > 500 {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.reports.command(ctx, key, "team-recipient-access", []any{plan, in}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: in.RecipientID}
		if err := r.setTeamSubscriptionContext(ctx, tx, 0); err != nil {
			return out, err
		}
		var auth bool
		if err := tx.QueryRow(ctx, `SELECT dbhub_team_delivery_authorizer($1)`, plan).Scan(&auth); err != nil {
			return out, err
		}
		if !auth {
			return out, ErrManagedDenied
		}
		if err := lockTeamRecipient(ctx, tx, plan, in.RecipientID); err != nil {
			return out, err
		}
		item, err := r.readTeamSubscription(ctx, tx, plan)
		if err != nil {
			return out, err
		}
		if !containsID(item.Recipients, in.RecipientID) {
			return out, ErrManagedDenied
		}
		var version, member int64
		var needsReview bool
		err = tx.QueryRow(ctx, `SELECT version,member_version,needs_review FROM sys_report_team_recipient_access
			WHERE workspace_id=dbhub_workspace() AND plan_id=$1 AND user_id=$2 FOR UPDATE`,
			plan, in.RecipientID).Scan(&version, &member, &needsReview)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if version != in.Version || !grant && version == 0 || grant && needsReview {
			return out, ErrManagedConflict
		}
		if grant {
			err = tx.QueryRow(ctx, `SELECT version FROM sys_workspace_members WHERE workspace_id=dbhub_workspace() AND user_id=$2
				AND dbhub_team_delivery_live($1) AND dbhub_team_delivery_selected($1,$2)`, plan, in.RecipientID).Scan(&member)
			if errors.Is(err, pgx.ErrNoRows) {
				return out, ErrManagedConflict
			}
			if err != nil {
				return out, err
			}
		}
		if version == 0 {
			err = tx.QueryRow(ctx, `INSERT INTO sys_report_team_recipient_access(workspace_id,plan_id,user_id,version,member_version,enabled,changed_by,reason)
				VALUES(dbhub_workspace(),$1,$2,1,$3,$4,dbhub_actor(),$5)
				RETURNING version`, plan, in.RecipientID, member, grant, in.Reason).Scan(&out.Version)
		} else {
			err = tx.QueryRow(ctx, `UPDATE sys_report_team_recipient_access SET version=version+1,member_version=$3,
				enabled=$4,changed_by=dbhub_actor(),reason=$5,changed_at=statement_timestamp()
				WHERE workspace_id=dbhub_workspace() AND plan_id=$1 AND user_id=$2 AND version=$6 RETURNING version`,
				plan, in.RecipientID, member, grant, in.Reason, version).Scan(&out.Version)
		}
		return out, err
	})
}

// OpenTeamPeriod creates independent queue receipts for the frozen roster.
func (r *ReportSubscriptionRepository) OpenTeamPeriod(ctx context.Context, key string, plan, version int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "team-period-open", []int64{plan, version}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		return r.openTeamPeriod(ctx, tx, plan, version, nil)
	})
}

func (r *ReportSubscriptionRepository) openTeamPeriod(ctx context.Context, tx pgx.Tx, plan, version int64, slot *time.Time) (ReportCommandResult, error) {
	var out ReportCommandResult
	if err := r.setTeamSubscriptionContext(ctx, tx, 0); err != nil {
		return out, err
	}
	if err := lockTeamSubscription(ctx, tx, plan); err != nil {
		return out, err
	}
	var encrypted string
	err := tx.QueryRow(ctx, `SELECT encrypted_approval FROM sys_report_team_subscriptions
			WHERE workspace_id=dbhub_workspace() AND id=$1 AND version=$2 AND owner_id=dbhub_actor()
			AND dbhub_team_delivery_live(id)`, plan, version).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrManagedConflict
	}
	if err != nil {
		return out, err
	}
	var approval teamSubscriptionApproval
	if err = r.decode(encrypted, &approval); err != nil {
		return out, err
	}
	var ownerCurrent bool
	if err = tx.QueryRow(ctx, `SELECT dbhub_team_delivery_owner_scope($1,$2,$3,$4)`, plan,
		approval.Owner.Version, approval.Owner.IdentityVersion, approval.Shape.DatasetVersion).Scan(&ownerCurrent); err != nil {
		return out, err
	}
	if !ownerCurrent {
		return out, ErrManagedConflict
	}
	err = tx.QueryRow(ctx, `INSERT INTO sys_report_team_periods(workspace_id,plan_id,plan_version,scheduled_for)
			SELECT p.workspace_id,p.id,p.version,$3::timestamptz FROM sys_report_team_subscriptions p
			WHERE p.workspace_id=dbhub_workspace() AND p.id=$1 AND p.version=$2
			AND p.owner_id=dbhub_actor() AND dbhub_team_delivery_live(p.id)
			AND ($3::timestamptz IS NULL OR $3=date_bin(make_interval(mins=>p.schedule_interval_minutes),statement_timestamp(),p.decided_at))
			ON CONFLICT (workspace_id,plan_id,scheduled_for) WHERE scheduled_for IS NOT NULL DO NOTHING
			RETURNING id,plan_version`, plan, version, slot).Scan(&out.ID, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrManagedConflict
	}
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sys_report_team_deliveries(id,workspace_id,plan_id,period_id,user_id,grant_version,member_version,status)
			SELECT gen_random_uuid(),p.workspace_id,p.id,$2,roster.uid,COALESCE(a.version,0),COALESCE(a.member_version,0),
			CASE WHEN a.enabled AND a.member_version=m.version AND dbhub_team_delivery_selected(p.id,roster.uid) THEN 'queued' ELSE 'skipped' END
			FROM sys_report_team_subscriptions p CROSS JOIN LATERAL unnest(p.recipient_ids) roster(uid)
			LEFT JOIN sys_report_team_recipient_access a ON a.workspace_id=p.workspace_id AND a.plan_id=p.id AND a.user_id=roster.uid
			LEFT JOIN sys_workspace_members m ON m.workspace_id=p.workspace_id AND m.user_id=roster.uid
			WHERE p.workspace_id=dbhub_workspace() AND p.id=$1`, plan, out.ID)
	return out, err
}

type TeamDeliveryStatus struct {
	ID       string `json:"id"`
	PlanID   int64  `json:"plan_id"`
	PeriodID int64  `json:"period_id"`
	UserID   int64  `json:"user_id"`
	Status   string `json:"status"`
}

func (r *ReportSubscriptionRepository) TeamDeliveryStatuses(ctx context.Context, plan, period int64) ([]TeamDeliveryStatus, error) {
	if plan > 0 {
		var found bool
		err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_report_team_periods p
			WHERE p.workspace_id=dbhub_workspace() AND p.id=$2 AND p.plan_id=$1 AND dbhub_team_delivery_owner($1))`,
			plan, period).Scan(&found)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNotFound
		}
		rows, err := r.pool.Query(ctx, `SELECT * FROM dbhub_team_period_status($1,$2)`, plan, period)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowToStructByPos[TeamDeliveryStatus])
	}
	rows, err := r.pool.Query(ctx, `SELECT d.id::text,d.plan_id,d.period_id,d.user_id,d.status
		FROM sys_report_team_deliveries d WHERE d.workspace_id=dbhub_workspace() AND d.user_id=dbhub_actor()
		ORDER BY d.created_at DESC,d.user_id,d.id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[TeamDeliveryStatus])
}
