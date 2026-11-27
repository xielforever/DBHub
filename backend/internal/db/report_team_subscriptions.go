package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type TeamSubscriptionInput struct {
	Definition ReportSubscriptionDefinition `json:"definition"`
	PreviousID int64                        `json:"previous_id,omitempty"`
	UserIDs    []int64                      `json:"user_ids"`
	GroupIDs   []int64                      `json:"group_ids"`
}

type TeamSubscription struct {
	TeamSubscriptionInput
	ID          int64      `json:"id"`
	Version     int64      `json:"version"`
	OwnerID     int64      `json:"owner_id"`
	Recipients  []int64    `json:"recipient_ids"`
	Status      string     `json:"status"`
	Superseded  bool       `json:"superseded"`
	ReviewUntil *time.Time `json:"review_until,omitempty"`
	Basis       string     `json:"basis_sha256,omitempty"`
	ownerStamp  string
	shapeStamp  string
}

func (r *ReportSubscriptionRepository) setTeamSubscriptionContext(ctx context.Context, tx pgx.Tx, report int64) error {
	if r.reports.phase2ReportIsolation == "" {
		return ErrManagedDenied
	}
	_, err := tx.Exec(ctx, `SELECT set_config('dbhub.isolated_subscription_key',$1,true),
		set_config('dbhub.isolated_subscription_report',$2,true)`, r.reports.phase2ReportIsolation, strconv.FormatInt(report, 10))
	return err
}

func teamSubscriptionGrant(ctx context.Context, tx pgx.Tx, report int64, action string) (int64, error) {
	var grant int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(dbhub_isolated_subscription_grant($1,$2),0)`, report, action).Scan(&grant)
	if err == nil && grant == 0 {
		err = ErrManagedDenied
	}
	return grant, err
}

func lockTeamSubscription(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		fmt.Sprintf("team-subscription:%d:%d", WorkspaceID(ctx), id))
	return err
}

func (r *ReportSubscriptionRepository) readTeamSubscription(ctx context.Context, tx pgx.Tx, id int64) (*TeamSubscription, error) {
	var raw string
	out := &TeamSubscription{}
	err := tx.QueryRow(ctx, `SELECT id,version,owner_id,COALESCE(previous_id,0),encrypted_definition,
		user_ids,group_ids,recipient_ids,status,review_until,owner_stamp,shape_stamp,
		EXISTS(SELECT 1 FROM sys_report_team_subscriptions next WHERE next.workspace_id=s.workspace_id AND next.previous_id=s.id)
		FROM sys_report_team_subscriptions s WHERE s.workspace_id=dbhub_workspace() AND s.id=$1`, id).
		Scan(&out.ID, &out.Version, &out.OwnerID, &out.PreviousID, &raw,
			&out.UserIDs, &out.GroupIDs, &out.Recipients, &out.Status, &out.ReviewUntil, &out.ownerStamp, &out.shapeStamp, &out.Superseded)
	if err != nil {
		return nil, managedError(err)
	}
	if err = r.decode(raw, &out.Definition); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *ReportSubscriptionRepository) teamSubscriptionShape(ctx context.Context, tx pgx.Tx, x *ManagedExecution, d ReportSubscriptionDefinition) (reportReviewBoundary, BusinessRowColumnPolicy, map[string]any, error) {
	enrolled, err := r.subscriptionReviewEnrolled(ctx, tx, x)
	if err != nil || !enrolled {
		if err == nil {
			err = ErrManagedDenied
		}
		return reportReviewBoundary{}, BusinessRowColumnPolicy{}, nil, err
	}
	// The active release stays fixed while a decision is written.
	var live int64
	err = tx.QueryRow(ctx, `SELECT m.id FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id
		WHERE m.workspace_id=dbhub_workspace() AND m.id=$1 AND m.publication_no=$2 AND re.review_id=$3 FOR SHARE OF m`,
		x.ReportID, x.PublicationNo, x.ReviewID).Scan(&live)
	if err != nil {
		return reportReviewBoundary{}, BusinessRowColumnPolicy{}, nil, managedError(err)
	}
	_, _, account, err := r.reports.loadAccount(ctx, tx, x.AccountID)
	if err != nil {
		return reportReviewBoundary{}, BusinessRowColumnPolicy{}, nil, err
	}
	if account.Version != x.AccountVersion || account.Generation != x.Generation {
		return reportReviewBoundary{}, BusinessRowColumnPolicy{}, nil, ErrManagedConflict
	}
	if err = lockAudienceGroups(ctx, tx, x.Manifest.GroupIDs); err != nil {
		return reportReviewBoundary{}, BusinessRowColumnPolicy{}, nil, err
	}
	if err = r.reports.useBusinessReportBinding(ctx, tx, x); err != nil {
		return reportReviewBoundary{}, BusinessRowColumnPolicy{}, nil, err
	}
	if err = lockBusinessReportPolicy(ctx, tx, x.businessBinding); err != nil {
		return reportReviewBoundary{}, BusinessRowColumnPolicy{}, nil, err
	}
	shape, row, _, err := r.reports.reportReviewShape(ctx, tx, x)
	if err != nil {
		return shape, row, nil, err
	}
	params, err := ResolveReportParameters(x.Definition.Parameters, d.Params, time.Now())
	if err != nil {
		return shape, row, nil, ErrManagedInput
	}
	if _, err = (&BusinessOutputPlan{ReportTarget: BusinessReportTarget{Filters: shape.Parameters}}).BindReportParameters(params); err != nil {
		return shape, row, nil, ErrManagedInput
	}
	return shape, row, params, nil
}

// SubmitTeamSubscription freezes selection, parameters and the active report
// before an independent reviewer sees the application.
func (r *ReportSubscriptionRepository) SubmitTeamSubscription(ctx context.Context, key string, input TeamSubscriptionInput) (ReportCommandResult, error) {
	if err := validateSubscriptionDefinition(input.Definition); err != nil {
		return ReportCommandResult{}, err
	}
	if input.PreviousID < 0 || input.Definition.Webhook || input.Definition.Alert != nil {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.reports.command(ctx, key, "team-subscription-submit", input, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		var out ReportCommandResult
		d := input.Definition
		if err := r.setTeamSubscriptionContext(ctx, tx, d.ReportID); err != nil {
			return out, err
		}
		if _, err := teamSubscriptionGrant(ctx, tx, d.ReportID, "create"); err != nil {
			return out, err
		}
		if input.PreviousID != 0 {
			if err := lockTeamSubscription(ctx, tx, input.PreviousID); err != nil {
				return out, err
			}
			previous, err := r.readTeamSubscription(ctx, tx, input.PreviousID)
			if err != nil {
				return out, err
			}
			if previous.OwnerID != ActorID(ctx) || previous.Definition.ReportID != d.ReportID || previous.Superseded {
				return out, ErrManagedConflict
			}
		}
		x, err := r.reports.ResolvePublished(ctx, d.ReportID, d.PublicationNo)
		if err != nil {
			return out, err
		}
		if err = audience(ctx, tx, input.UserIDs, input.GroupIDs); err != nil {
			return out, err
		}
		shape, row, params, err := r.teamSubscriptionShape(ctx, tx, x, d)
		if err != nil {
			return out, err
		}
		if err = r.reports.prepareBusinessReport(ctx, tx, x); err != nil {
			return out, err
		}
		owner, err := reportReviewMember(ctx, tx, row, ActorID(ctx))
		if err != nil {
			return out, err
		}
		recipients, err := reportAudienceMembers(ctx, tx, input.UserIDs, input.GroupIDs)
		if err != nil {
			return out, err
		}
		if len(recipients) == 0 || len(recipients) > 100 {
			return out, ErrManagedInput
		}
		for _, recipient := range recipients {
			if !dashboardGrant(ctx, tx, x.Manifest.UserIDs, x.Manifest.GroupIDs, recipient) {
				return out, ErrManagedDenied
			}
		}
		d.Params, d.Enabled = params, false
		raw, err := r.cipher.Encrypt(string(canonicalReportJSON(d)))
		if err != nil {
			return out, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO sys_report_team_subscriptions(workspace_id,owner_id,owner_member_version,report_id,
			publication_no,previous_id,encrypted_definition,user_ids,group_ids,recipient_ids,owner_stamp,shape_stamp)
			VALUES(dbhub_workspace(),dbhub_actor(),dbhub_member_epoch(),$1,$2,NULLIF($3::bigint,0),$4,$5,$6,$7,$8,$9)
			RETURNING id,version`, d.ReportID, d.PublicationNo, input.PreviousID, raw, normalizedIDs(input.UserIDs),
			normalizedIDs(input.GroupIDs), recipients, reportDigest(owner), reportDigest(shape)).Scan(&out.ID, &out.Version)
		return out, err
	})
}

type teamSubscriptionApproval struct {
	Shape      reportReviewBoundary
	Params     map[string]any
	Owner      reportReviewAudience
	Reviewer   reportReviewAudience
	Recipients []reportReviewAudience
}

func (r *ReportSubscriptionRepository) teamSubscriptionBasis(ctx context.Context, tx pgx.Tx, item *TeamSubscription) (int64, teamSubscriptionApproval, error) {
	var approval teamSubscriptionApproval
	if item.Status != "pending" || item.Superseded {
		return 0, approval, ErrManagedConflict
	}
	if item.OwnerID == ActorID(ctx) || slices.Contains(item.Recipients, ActorID(ctx)) {
		return 0, approval, ErrManagedDenied
	}
	if err := r.setTeamSubscriptionContext(ctx, tx, item.Definition.ReportID); err != nil {
		return 0, approval, err
	}
	grant, err := teamSubscriptionGrant(ctx, tx, item.Definition.ReportID, "review")
	if err != nil {
		return 0, approval, err
	}
	x, err := r.reports.ResolvePublished(ctx, item.Definition.ReportID, item.Definition.PublicationNo)
	if errors.Is(err, ErrNotFound) {
		return 0, approval, ErrManagedConflict
	}
	if err != nil {
		return 0, approval, err
	}
	if err = lockAudienceGroups(ctx, tx, item.GroupIDs); err != nil {
		return 0, approval, err
	}
	recipients, err := reportAudienceMembers(ctx, tx, item.UserIDs, item.GroupIDs)
	if err != nil {
		return 0, approval, err
	}
	if !slices.Equal(recipients, item.Recipients) {
		return 0, approval, ErrManagedConflict
	}
	shape, row, params, err := r.teamSubscriptionShape(ctx, tx, x, item.Definition)
	if err != nil {
		return 0, approval, err
	}
	if reportDigest(shape) != item.shapeStamp || reportDigest(params) != reportDigest(item.Definition.Params) {
		return 0, approval, ErrManagedConflict
	}
	var validOwner bool
	err = tx.QueryRow(ctx, `SELECT dbhub_isolated_subscription_owner_current($1)`, item.ID).Scan(&validOwner)
	if err != nil {
		return 0, approval, err
	}
	if !validOwner || !dashboardGrant(ctx, tx, x.Manifest.UserIDs, x.Manifest.GroupIDs, item.OwnerID) {
		return 0, approval, ErrManagedConflict
	}
	owner, err := reportReviewMember(ctx, tx, row, item.OwnerID)
	if err != nil || reportDigest(owner) != item.ownerStamp {
		if err == nil {
			err = ErrManagedConflict
		}
		return 0, approval, err
	}
	approval.Shape, approval.Params, approval.Owner = shape, params, owner
	for _, recipient := range recipients {
		if !dashboardGrant(ctx, tx, x.Manifest.UserIDs, x.Manifest.GroupIDs, recipient) {
			return 0, approval, ErrManagedDenied
		}
		member, err := reportReviewMember(ctx, tx, row, recipient)
		if err != nil {
			return 0, approval, err
		}
		approval.Recipients = append(approval.Recipients, member)
	}
	coverage := shape
	coverage.Audience = approval.Recipients
	published, err := readReportReviewPolicy(ctx, tx, x.ReviewID)
	if err != nil {
		return 0, approval, err
	}
	if reportReviewExpanded(published.boundary, coverage) || published.boundary.DatasetVersion != shape.DatasetVersion ||
		published.boundary.RowPolicy != shape.RowPolicy || published.boundary.OutputPolicy != shape.OutputPolicy ||
		published.boundary.Version != shape.Version || published.boundary.Generation != shape.Generation {
		return 0, approval, ErrManagedConflict
	}
	approval.Reviewer, err = reportReviewCoverage(ctx, tx, coverage, row, ActorID(ctx), item.OwnerID, false)
	return grant, approval, err
}

func teamSubscriptionBasisHash(item *TeamSubscription, grant int64, approval teamSubscriptionApproval) string {
	return reportDigest([]any{item.ID, item.Version, item.OwnerID, item.Recipients, grant, approval})
}

// TeamSubscription returns either owner metadata or the currently reviewable basis.
func (r *ReportSubscriptionRepository) TeamSubscription(ctx context.Context, id int64, review bool) (*TeamSubscription, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = r.setTeamSubscriptionContext(ctx, tx, 0); err != nil {
		return nil, err
	}
	item, err := r.readTeamSubscription(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if item.OwnerID != ActorID(ctx) {
		if _, err = teamSubscriptionGrant(ctx, tx, item.Definition.ReportID, "review"); err != nil {
			return nil, ErrNotFound
		}
	}
	if review {
		grant, approval, err := r.teamSubscriptionBasis(ctx, tx, item)
		if err != nil {
			return nil, err
		}
		item.Basis = teamSubscriptionBasisHash(item, grant, approval)
	}
	return item, tx.Commit(ctx)
}

func (r *ReportSubscriptionRepository) DecideTeamSubscription(ctx context.Context, key string, id, version int64, basis, decision, comment string) (ReportCommandResult, error) {
	if id < 1 || version < 1 || len(basis) != 64 || decision != "approved" && decision != "rejected" ||
		len([]rune(comment)) > 500 || decision == "rejected" && strings.TrimSpace(comment) == "" {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.reports.command(ctx, key, "team-subscription-review", []any{id, version, basis, decision, comment}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		if err := lockTeamSubscription(ctx, tx, id); err != nil {
			return out, err
		}
		if err := r.setTeamSubscriptionContext(ctx, tx, 0); err != nil {
			return out, err
		}
		item, err := r.readTeamSubscription(ctx, tx, id)
		if err != nil {
			return out, err
		}
		if item.Version != version {
			return out, ErrManagedConflict
		}
		grant, approval, err := r.teamSubscriptionBasis(ctx, tx, item)
		if err != nil {
			return out, err
		}
		if basis != teamSubscriptionBasisHash(item, grant, approval) {
			return out, ErrManagedConflict
		}
		var encrypted *string
		if decision == "approved" {
			value, err := r.cipher.Encrypt(string(canonicalReportJSON(approval)))
			if err != nil {
				return out, err
			}
			encrypted = &value
		}
		err = tx.QueryRow(ctx, `UPDATE sys_report_team_subscriptions SET status=$3,version=version+1,reviewer_id=dbhub_actor(),
			review_grant_id=$4,basis_sha256=$5,encrypted_approval=$6,comment=$7,decided_at=statement_timestamp(),
			schedule_interval_minutes=CASE WHEN $3='approved' THEN $8::integer END,
			review_until=CASE WHEN $3='approved' THEN statement_timestamp()+interval '30 days' END
			WHERE workspace_id=dbhub_workspace() AND id=$1 AND version=$2 AND status='pending' RETURNING version`,
			id, version, decision, grant, basis, encrypted, comment, item.Definition.IntervalMinutes).Scan(&out.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrManagedConflict
		}
		return out, err
	})
}
