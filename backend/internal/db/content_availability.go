package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// ContentAvailabilityCommand changes the availability of an enrolled object.
type ContentAvailabilityCommand struct {
	RequestID  string `json:"request_id"`
	Expected   int64  `json:"expected_publication_no"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	ApprovalID int64  `json:"approval_id"`
}

type availabilityObject struct {
	report, dashboard int64
}

func (o availabilityObject) family() string {
	if o.dashboard > 0 {
		return "dashboard"
	}
	return "report"
}

type availabilityState struct {
	owner, number, review, epoch int64
}

const sameAvailabilityObject = `workspace_id=dbhub_workspace()
	AND report_id IS NOT DISTINCT FROM NULLIF($1::bigint,0)
	AND dashboard_id IS NOT DISTINCT FROM NULLIF($2::bigint,0)`

func enrolledReportAvailability(ctx context.Context, tx pgx.Tx, report int64) (bool, error) {
	var enrolled bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_iam_isolated_report_reviews
		WHERE workspace_id=dbhub_workspace() AND report_id=$1)`, report).Scan(&enrolled)
	return enrolled, err
}

// ChangeAvailability applies the isolated report offline or emergency policy.
func (r *ManagedReportRepository) ChangeAvailability(ctx context.Context, id int64, cmd ContentAvailabilityCommand) (ReportCommandResult, error) {
	return r.changeAvailability(ctx, availabilityObject{report: id}, cmd)
}

// ChangeAvailability applies the isolated dashboard offline or emergency policy.
func (r *ManagedDashboardRepository) ChangeAvailability(ctx context.Context, id int64, cmd ContentAvailabilityCommand) (ReportCommandResult, error) {
	return r.reports.changeAvailability(ctx, availabilityObject{dashboard: id}, cmd)
}

func (r *ManagedReportRepository) changeAvailability(ctx context.Context, object availabilityObject, cmd ContentAvailabilityCommand) (ReportCommandResult, error) {
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	if cmd.Expected <= 0 || cmd.Reason == "" || utf8.RuneCountInString(cmd.Reason) > 1000 || strings.ContainsRune(cmd.Reason, 0) ||
		cmd.ApprovalID < 0 || (cmd.Action == "approve_offline") != (cmd.ApprovalID > 0) {
		return ReportCommandResult{}, ErrManagedInput
	}
	switch cmd.Action {
	case "request_offline", "approve_offline", "emergency_stop", "emergency_restore":
	default:
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.command(ctx, cmd.RequestID, object.family()+"-"+cmd.Action, []any{object.report, object.dashboard, cmd}, false, false,
		func(tx pgx.Tx) (ReportCommandResult, error) {
			return r.applyAvailability(ctx, tx, object, cmd)
		})
}

func (r *ManagedReportRepository) lockAvailabilityObject(ctx context.Context, tx pgx.Tx, o availabilityObject) (availabilityState, error) {
	var s availabilityState
	if o.report > 0 {
		enrolled, err := enrolledReportAvailability(ctx, tx, o.report)
		if err != nil {
			return s, err
		}
		if !enrolled {
			return s, ErrNotFound
		}
		x := ManagedExecution{ReportID: o.report}
		if err = r.resolveBusinessReportBinding(ctx, tx, &x); err != nil {
			return s, err
		}
		if x.businessBinding == "" {
			return s, ErrManagedDenied
		}
		err = tx.QueryRow(ctx, `SELECT m.owner_id,m.publication_no,COALESCE(re.review_id,0)
			FROM sys_managed_reports m LEFT JOIN sys_report_releases re ON re.id=m.active_release_id
			WHERE m.id=$1 FOR UPDATE OF m`, o.report).Scan(&s.owner, &s.number, &s.review)
		return s, err
	}
	dash := ManagedDashboardRepository{pool: r.pool, reports: r}
	enrolled, err := dash.governedDashboard(ctx, tx, o.dashboard)
	if err != nil {
		return s, err
	}
	if !enrolled {
		return s, ErrNotFound
	}
	err = tx.QueryRow(ctx, `SELECT d.owner_id,d.publication_no,COALESCE(re.review_id,0),d.lifecycle_epoch
		FROM sys_managed_dashboards d LEFT JOIN sys_dashboard_releases re ON re.id=d.active_release_id
		WHERE d.id=$1 AND d.lifecycle='active' FOR UPDATE OF d`, o.dashboard).Scan(&s.owner, &s.number, &s.review, &s.epoch)
	return s, err
}

func (r *ManagedReportRepository) applyAvailability(ctx context.Context, tx pgx.Tx, o availabilityObject, cmd ContentAvailabilityCommand) (ReportCommandResult, error) {
	var out ReportCommandResult
	s, err := r.lockAvailabilityObject(ctx, tx, o)
	if err != nil {
		return out, err
	}
	actor := ActorID(ctx)
	emergency := cmd.Action == "emergency_stop" || cmd.Action == "emergency_restore"
	var allowed bool
	if emergency {
		err = tx.QueryRow(ctx, `SELECT dbhub_isolated_content_operator(NULLIF($1::bigint,0),NULLIF($2::bigint,0),$3)
			AND ($3='emergency_stop' OR NOT dbhub_iam_action_denied(dbhub_actor(),$4))`,
			o.report, o.dashboard, cmd.Action, o.family()+".publish").Scan(&allowed)
	} else {
		err = tx.QueryRow(ctx, `SELECT dbhub_iam_has_action($1,$2||'.review')
			AND ($3='request_offline' OR dbhub_iam_has_action($1,$2||'.offline'))`, actor, o.family(), cmd.Action).Scan(&allowed)
		allowed = allowed || (cmd.Action == "request_offline" && actor == s.owner)
	}
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, ErrManagedDenied
	}
	if cmd.Expected != s.number || (s.review == 0) != (cmd.Action == "emergency_restore") {
		return out, ErrManagedConflict
	}
	switch cmd.Action {
	case "request_offline":
		stamp, count, err := availabilityDependencies(ctx, tx, o, s.review)
		if err != nil {
			return out, err
		}
		out.ID, err = appendAvailabilityEvent(ctx, tx, o, cmd, 0, stamp)
		out.Version, out.DependencyCount = s.number, &count
		return out, err
	case "approve_offline":
		var requester int64
		var reason, approvedStamp string
		err := tx.QueryRow(ctx, `SELECT actor_id,reason,basis_stamp FROM sys_content_availability_events WHERE `+sameAvailabilityObject+
			` AND action='request_offline' AND id=$3 AND base_publication_no=$4`,
			o.report, o.dashboard, cmd.ApprovalID, s.number).Scan(&requester, &reason, &approvedStamp)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrManagedConflict
		}
		if err != nil {
			return out, err
		}
		if requester == actor || s.owner == actor {
			return out, ErrManagedDenied
		}
		if err = r.checkAvailabilityReview(ctx, tx, o, s.review, requester, false); err != nil {
			return out, err
		}
		dependencies, _, err := availabilityDependencies(ctx, tx, o, s.review)
		if err != nil {
			return out, err
		}
		if dependencies != approvedStamp || reason != cmd.Reason {
			return out, fmt.Errorf("%w: 下线原因或依赖基线变化，请重新确认", ErrManagedConflict)
		}
		if _, err = appendAvailabilityEvent(ctx, tx, o, cmd, cmd.ApprovalID, dependencies); err != nil {
			return out, err
		}
	case "emergency_stop":
		stamp, err := bestEffortStopAudience(ctx, tx, o, s.review)
		if err != nil {
			return out, err
		}
		if _, err = appendAvailabilityEvent(ctx, tx, o, cmd, 0, stamp); err != nil {
			return out, err
		}
	case "emergency_restore":
		return r.restoreAvailability(ctx, tx, o, s, cmd)
	}
	return appendAvailabilityRelease(ctx, tx, o, s, "unpublish", nil)
}

func appendAvailabilityEvent(ctx context.Context, tx pgx.Tx, o availabilityObject, cmd ContentAvailabilityCommand, related int64, stamp string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO sys_content_availability_events(workspace_id,report_id,dashboard_id,
		base_publication_no,actor_id,action,reason,related_event_id,basis_stamp)
		VALUES(dbhub_workspace(),NULLIF($1::bigint,0),NULLIF($2::bigint,0),$3,$4,$5,$6,NULLIF($7::bigint,0),$8) RETURNING id`,
		o.report, o.dashboard, cmd.Expected, ActorID(ctx), cmd.Action, cmd.Reason, related, stamp).Scan(&id)
	return id, err
}

func appendAvailabilityRelease(ctx context.Context, tx pgx.Tx, o availabilityObject, s availabilityState, kind string, review *int64) (ReportCommandResult, error) {
	if o.report > 0 {
		return appendReportRelease(ctx, tx, o.report, s.number, kind, review, false)
	}
	return appendDashboardRelease(ctx, tx, o.dashboard, s.number, s.epoch, kind, review)
}

func availabilityDependencies(ctx context.Context, tx pgx.Tx, o availabilityObject, review int64) (string, int64, error) {
	var raw json.RawMessage
	var count int64
	var err error
	if o.report > 0 {
		err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_array(d.id,d.publication_no) ORDER BY d.id),'[]'),count(*)
			FROM sys_managed_dashboards d JOIN sys_dashboard_releases re ON re.id=d.active_release_id
			JOIN sys_dashboard_reviews rv ON rv.id=re.review_id JOIN sys_dashboard_versions v ON v.id=rv.version_id
			WHERE d.lifecycle='active' AND EXISTS(SELECT 1 FROM jsonb_array_elements(v.definition->'tiles') t WHERE (t->>'report_id')::bigint=$1)`,
			o.report).Scan(&raw, &count)
	} else {
		err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_array(m.id,m.publication_no) ORDER BY m.id),'[]'),count(*)
			FROM sys_managed_reports m WHERE m.id IN (
				SELECT (t->>'report_id')::bigint FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id
				CROSS JOIN LATERAL jsonb_array_elements(v.definition->'tiles') t WHERE rv.id=$1)`, review).Scan(&raw, &count)
	}
	return reportDigest(raw), count, err
}
