package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const dashboardPublicationState = ` CROSS JOIN LATERAL (
	SELECT dbhub_iam_dashboard_matrix_enabled() AS matrix,
		EXISTS(SELECT 1 FROM sys_dashboard_releases re WHERE re.review_id=rv.id AND re.workspace_id=rv.workspace_id) AS was_published,
		CASE WHEN dbhub_iam_dashboard_matrix_enabled() THEN COALESCE(
			rv.decided_by=rv.reviewer_id AND rv.business_review_authority=dbhub_dashboard_review_authority(rv.reviewer_id),false)
		ELSE true END AS authority_current) state`

// Publication tasks and review actions share the same eligibility checks.
const dashboardCanPublish = `(rv.status='approved' AND v.number=d.latest_version
	AND d.lifecycle='active' AND rv.lifecycle_epoch=d.lifecycle_epoch AND NOT state.was_published
	AND state.authority_current AND rv.reviewer_id=dbhub_actor()
	AND v.submitted_by IS DISTINCT FROM dbhub_actor())`

func (r *ManagedDashboardRepository) Reviews(ctx context.Context, id, reviewID int64) ([]DashboardReview, error) {
	if _, e := r.Get(ctx, id); e != nil {
		return nil, e
	}
	rows, e := r.pool.Query(ctx, `SELECT rv.id,v.id,v.number,v.definition,v.submitted_by,v.submitter_name,rv.reviewer_id,
		dbhub_dashboard_reviewer_eligible(rv.reviewer_id),
		CASE WHEN rv.status='pending' AND rv.expires_at<=now() THEN 'expired' ELSE rv.status END,
		rv.comment,rv.expires_at,rv.validated_at,rv.lifecycle_epoch,
		state.was_published,
		COALESCE((SELECT CASE WHEN c.requires_data AND rv.status='pending' AND rv.expires_at>now() THEN 'data_review'
			WHEN rv.status='approved' THEN 'complete' ELSE '' END
			FROM sys_dashboard_review_checks c WHERE c.workspace_id=rv.workspace_id AND c.review_id=rv.id
			AND c.assignment_version=rv.assignment_version),''),
		CASE WHEN state.matrix THEN `+dashboardCanPublish+` END,
		CASE WHEN state.matrix THEN (state.was_published AND d.lifecycle='active'
			AND rv.lifecycle_epoch=d.lifecycle_epoch AND dbhub_dashboard_workflow_admin()
			AND dbhub_iam_has_action(dbhub_actor(),'dashboard.publish')
			AND dbhub_dashboard_reviewer_eligible(rv.reviewer_id)
			AND (SELECT review_id FROM sys_dashboard_releases WHERE id=d.active_release_id) IS DISTINCT FROM rv.id
			AND NOT EXISTS(SELECT 1 FROM sys_iam_isolated_dashboard_reviews g WHERE g.dashboard_id=d.id)) END,
		CASE WHEN state.matrix THEN (rv.status='pending' AND rv.expires_at>now()
			AND d.lifecycle='active' AND rv.lifecycle_epoch=d.lifecycle_epoch AND dbhub_dashboard_workflow_admin()
			AND dbhub_iam_has_action(dbhub_actor(),'dashboard.review')) END,
		CASE WHEN state.matrix THEN (rv.status='approved' AND NOT state.was_published AND NOT state.authority_current) END,
		CASE WHEN state.matrix AND rv.status='approved' THEN rv.decided_by END
		FROM sys_dashboard_versions v JOIN sys_dashboard_reviews rv ON rv.version_id=v.id AND rv.workspace_id=v.workspace_id
		JOIN sys_managed_dashboards d ON d.id=v.dashboard_id AND d.workspace_id=v.workspace_id`+dashboardPublicationState+`
	WHERE v.dashboard_id=$1 AND rv.dashboard_id=d.id AND d.lifecycle<>'deleted' AND ($2=0 OR rv.id=$2) AND (NOT state.matrix OR d.owner_id=dbhub_actor()
			OR dbhub_dashboard_workflow_admin() OR rv.reviewer_id=dbhub_actor())
		ORDER BY v.number DESC LIMIT 100`, id, reviewID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DashboardReview{}
	for rows.Next() {
		var rv DashboardReview
		var raw []byte
		if e = rows.Scan(&rv.ID, &rv.VersionID, &rv.Number, &raw, &rv.SubmitterID, &rv.Submitter, &rv.ReviewerID,
			&rv.ReviewerEligible, &rv.Status, &rv.Comment, &rv.ExpiresAt, &rv.ValidatedAt, &rv.Epoch, &rv.WasPublished, &rv.ApprovalStage,
			&rv.CanPublish, &rv.CanRollback, &rv.CanReassign, &rv.RequiresReReview, &rv.BusinessSignerID); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &rv.Definition); e != nil {
			return nil, e
		}
		out = append(out, rv)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if reviewID > 0 && len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}
func (r *ManagedDashboardRepository) ValidateReview(ctx context.Context, key string, review int64, refresh string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-validate", []any{review, refresh}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: review}
		var raw []byte
		var owner, reviewer, assignment int64
		var eligible bool
		e := tx.QueryRow(ctx, `SELECT v.definition,COALESCE(v.submitted_by,0),rv.reviewer_id,rv.assignment_version,
			dbhub_dashboard_reviewer_eligible(rv.reviewer_id)
			FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id
			JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id
			WHERE rv.id=$1 AND rv.status='pending' AND rv.expires_at>now()
			AND rv.lifecycle_epoch=d.lifecycle_epoch AND d.lifecycle='active' FOR UPDATE OF rv,d`, review).
			Scan(&raw, &owner, &reviewer, &assignment, &eligible)
		if e != nil {
			return out, e
		}
		if owner == ActorID(ctx) || reviewer != ActorID(ctx) || !eligible {
			return out, ErrManagedDenied
		}
		var d DashboardDefinition
		if e = json.Unmarshal(raw, &d); e != nil {
			return out, e
		}
		if e = r.checkReviewerBenefitForReview(ctx, tx, review, d, reviewer); e != nil {
			return out, e
		}
		refs, e := r.checkDefinition(ctx, tx, d, ActorID(ctx))
		if e != nil {
			return out, e
		}
		var complete bool
		// Reading rows is insufficient: byte checks, receipt persistence or the
		// final authorization guard may still reject delivery afterwards.
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_dashboard_refreshes f WHERE f.id=$1::uuid AND f.review_id=$2 AND f.mode='review' AND f.user_id=$3 AND f.member_version=dbhub_member_epoch() AND f.review_assignment_version=$5 AND f.expires_at>now() AND (SELECT count(*) FROM sys_dashboard_refresh_tiles t JOIN sys_query_executions q ON q.workspace_id=t.workspace_id AND q.request_id=t.execution_id AND q.user_id=f.user_id AND q.dashboard_refresh_id=f.id AND q.dashboard_tile_key=t.tile_key AND q.status='succeeded' WHERE t.refresh_id=f.id AND t.status='succeeded')=$4)`, refresh, review, ActorID(ctx), len(d.Tiles), assignment).Scan(&complete)
		if e != nil {
			return out, e
		}
		if !complete {
			return out, fmt.Errorf("%w: 请先完成该送审版本全部卡片的真实预览", ErrManagedInput)
		}
		minWatermarks, maxWatermarks := map[string]time.Time{}, map[string]time.Time{}
		for _, tile := range d.Tiles {
			if tile.ComparisonGroup == "" {
				continue
			}
			var watermark *time.Time
			if e = tx.QueryRow(ctx, `SELECT data_as_of FROM sys_dashboard_refresh_tiles WHERE refresh_id=$1::uuid AND tile_key=$2`, refresh, tile.Key).Scan(&watermark); e != nil {
				return out, e
			}
			if watermark == nil {
				continue
			}
			g := tile.ComparisonGroup
			if prev, ok := minWatermarks[g]; !ok || watermark.Before(prev) {
				minWatermarks[g] = *watermark
			}
			if prev, ok := maxWatermarks[g]; !ok || watermark.After(prev) {
				maxWatermarks[g] = *watermark
			}
			if maxWatermarks[g].Sub(minWatermarks[g]) > time.Duration(d.Quality.MaxWatermarkSkew)*time.Second {
				return out, fmt.Errorf("%w: 比较组数据水位超过允许偏差", ErrManagedInput)
			}
		}
		// Validation evidence records actual publications/account generations, no rows.
		for _, p := range refs {
			var valid bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_dashboard_refresh_tiles WHERE refresh_id=$1::uuid AND report_id=$2 AND report_publication_no=$3 AND account_version=$4 AND account_generation=$5 AND contract_hash=$6)`, refresh, p.ID, p.PublicationNo, p.AccountVersion, p.Generation, p.Contract.Hash).Scan(&valid); e != nil {
				return out, e
			}
			if !valid {
				return out, ErrManagedConflict
			}
		}
		evidence, e := dashboardEvidence(ctx, tx, d, refs)
		if e != nil {
			return out, e
		}
		s, governed, _, e := r.dashboardReviewBasis(ctx, tx, review, d)
		if e != nil {
			return out, e
		}
		var stamp string
		if governed {
			basis, _, err := r.buildDashboardReviewBoundary(ctx, tx, s, refs, refresh, 0, false)
			if err != nil {
				return out, err
			}
			stamp = reportDigest(basis)
		}
		_, e = tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET validation=$2::jsonb,validated_at=now(),governed_validation_stamp=$3 WHERE id=$1`, review, string(evidence), stamp)
		return out, e
	})
}
func (r *ManagedDashboardRepository) Decision(ctx context.Context, key string, review int64, decision, comment string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-decision", []any{review, decision, comment}, decision != "withdrawn", decision == "withdrawn", func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: review}
		var raw, evidence []byte
		var owner, reviewer int64
		var validated *time.Time
		var matrix, eligible bool
		var authority *string
		e := tx.QueryRow(ctx, `SELECT v.definition,COALESCE(v.submitted_by,0),rv.reviewer_id,rv.validated_at,rv.validation,
			dbhub_iam_dashboard_matrix_enabled(),dbhub_dashboard_reviewer_eligible(rv.reviewer_id),
			dbhub_dashboard_review_authority(rv.reviewer_id)
			FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id
			JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id
			WHERE rv.id=$1 AND rv.status='pending' AND rv.expires_at>now()
			AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch FOR UPDATE OF rv,d`, review).
			Scan(&raw, &owner, &reviewer, &validated, &evidence, &matrix, &eligible, &authority)
		if e != nil {
			return out, e
		}
		if decision == "withdrawn" {
			if owner != ActorID(ctx) {
				return out, ErrManagedDenied
			}
		} else {
			if reviewer != ActorID(ctx) || owner == ActorID(ctx) || !eligible || (matrix && authority == nil) {
				return out, ErrManagedDenied
			}
		}
		if decision == "approved" {
			if validated == nil || time.Since(*validated) > 15*time.Minute {
				return out, fmt.Errorf("%w: 请先完成15分钟内的真实预览验证", ErrManagedInput)
			}
			var d DashboardDefinition
			if e = json.Unmarshal(raw, &d); e != nil {
				return out, e
			}
			if e = r.checkReviewerBenefitForReview(ctx, tx, review, d, reviewer); e != nil {
				return out, e
			}
			refs, e := r.checkDefinition(ctx, tx, d, ActorID(ctx))
			if e != nil {
				return out, e
			}
			currentEvidence, e := dashboardEvidence(ctx, tx, d, refs)
			if e != nil {
				return out, e
			}
			if reportDigest(json.RawMessage(evidence)) != reportDigest(json.RawMessage(currentEvidence)) {
				return out, ErrManagedConflict
			}
			s, governed, stamp, err := r.dashboardReviewBasis(ctx, tx, review, d)
			if err != nil {
				return out, err
			}
			if governed {
				basis, _, err := r.buildDashboardReviewBoundary(ctx, tx, s, refs, "", 0, false)
				if err != nil {
					return out, err
				}
				if stamp == "" || stamp != reportDigest(basis) {
					return out, ErrManagedConflict
				}
				requiresData, err := signDashboardBusinessReview(ctx, tx, s, review, basis, comment)
				if err != nil {
					return out, err
				}
				if requiresData {
					out.ApprovalStage = "data_review"
					_, e = tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET business_review_authority=$2 WHERE id=$1`, review, authority)
					return out, e
				}
			}
		}
		if decision != "approved" && decision != "rejected" && decision != "withdrawn" {
			return out, ErrManagedInput
		}
		if decision == "rejected" && comment == "" {
			return out, ErrManagedInput
		}
		if decision != "approved" {
			authority = nil
		}
		_, e = tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET status=$2,comment=$3,decided_by=$4,decided_at=now(),
			business_review_authority=$5 WHERE id=$1`, review, decision, comment, ActorID(ctx), authority)
		return out, e
	})
}
func dashboardEvidence(ctx context.Context, tx pgx.Tx, d DashboardDefinition, refs []DashboardReport) ([]byte, error) {
	out := []any{}
	groups := append([]int64{}, d.GroupIDs...)
	users := append([]int64{}, d.UserIDs...)
	users = append(users, ActorID(ctx))
	for _, p := range refs {
		out = append(out, []any{p.ID, p.PublicationNo, p.AccountID, p.AccountVersion, p.Generation, p.Contract.Hash})
		groups = append(groups, p.GroupIDs...)
	}
	var groupVersions, userVersions []byte
	if e := tx.QueryRow(ctx, `SELECT COALESCE(jsonb_object_agg(id::text,version),'{}'::jsonb) FROM sys_user_groups WHERE id=ANY($1::bigint[])`, nonNilIDs(groups)).Scan(&groupVersions); e != nil {
		return nil, e
	}
	if e := tx.QueryRow(ctx, `SELECT COALESCE(jsonb_object_agg(user_id::text,version),'{}'::jsonb) FROM sys_workspace_members WHERE workspace_id=dbhub_workspace() AND user_id=ANY($1::bigint[])`, nonNilIDs(users)).Scan(&userVersions); e != nil {
		return nil, e
	}
	return canonicalReportJSON([]any{out, json.RawMessage(groupVersions), json.RawMessage(userVersions), ActorID(ctx), MemberEpoch(ctx)}), nil
}
func (r *ManagedDashboardRepository) Reassign(ctx context.Context, key string, id, reviewer int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-reassign", []int64{id, reviewer}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		var raw []byte
		var owner int64
		var eligible, canReassign bool
		if e := tx.QueryRow(ctx, `SELECT v.definition,COALESCE(v.submitted_by,0),
			NOT dbhub_iam_dashboard_matrix_enabled() OR dbhub_dashboard_workflow_admin()
			FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id
			JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id
			WHERE rv.id=$1 AND rv.status='pending' AND rv.expires_at>now() AND d.lifecycle='active'
			AND d.lifecycle_epoch=rv.lifecycle_epoch FOR UPDATE OF rv`, id).Scan(&raw, &owner, &canReassign); e != nil {
			return out, e
		}
		if !canReassign {
			return out, ErrManagedDenied
		}
		if e := tx.QueryRow(ctx, `SELECT dbhub_dashboard_reviewer_eligible($1)`, reviewer).Scan(&eligible); e != nil {
			return out, e
		}
		if owner == reviewer || !eligible {
			return out, ErrManagedInput
		}
		var d DashboardDefinition
		if e := json.Unmarshal(raw, &d); e != nil {
			return out, e
		}
		if e := r.checkReviewerBenefitForReview(ctx, tx, id, d, reviewer); e != nil {
			return out, e
		}
		if _, e := r.checkDefinition(ctx, tx, d, reviewer); e != nil {
			return out, e
		}
		_, e := tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET reviewer_id=$2,assignment_version=assignment_version+1,
			validation=NULL,validated_at=NULL,governed_validation_stamp='',business_review_authority=NULL WHERE id=$1`, id, reviewer)
		return out, e
	})
}
func (r *ManagedDashboardRepository) Release(ctx context.Context, key string, id, expected, review int64, kind string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-release", []any{id, expected, review, kind}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		var number, epoch, latest int64
		var state string
		var active *int64
		var matrix, workflowAdmin bool
		if e := tx.QueryRow(ctx, `SELECT publication_no,lifecycle_epoch,lifecycle,active_release_id,latest_version,
			dbhub_iam_dashboard_matrix_enabled(),dbhub_dashboard_workflow_admin()
			FROM sys_managed_dashboards WHERE id=$1 FOR UPDATE`, id).
			Scan(&number, &epoch, &state, &active, &latest, &matrix, &workflowAdmin); e != nil {
			return out, e
		}
		if number != expected || state != "active" {
			return out, ErrManagedConflict
		}
		if kind == "publish" {
			if err := guardCustodyPublication(ctx, tx, availabilityObject{dashboard: id}, review); err != nil {
				return out, err
			}
		}
		if kind != "publish" {
			if matrix && kind == "rollback" && !workflowAdmin {
				return out, ErrManagedDenied
			}
			enrolled, err := r.governedDashboard(ctx, tx, id)
			if err != nil {
				return out, err
			}
			if enrolled {
				return out, fmt.Errorf("%w: 已纳管仪表盘须使用下线与恢复流程", ErrManagedConflict)
			}
		}
		var selected *int64
		if kind == "unpublish" {
			if active == nil {
				return out, ErrManagedConflict
			}
		} else if kind == "publish" || kind == "rollback" {
			var raw []byte
			var rvEpoch, version, reviewer int64
			var reviewStatus string
			var eligible, authorityCurrent bool
			var submitter, signer int64
			if e := tx.QueryRow(ctx, `SELECT v.definition,rv.lifecycle_epoch,v.number,rv.reviewer_id,
				dbhub_dashboard_reviewer_eligible(rv.reviewer_id),rv.status,COALESCE(v.submitted_by,0),COALESCE(rv.decided_by,0),
				CASE WHEN dbhub_iam_dashboard_matrix_enabled() THEN COALESCE(
					rv.business_review_authority=dbhub_dashboard_review_authority(rv.reviewer_id),false)
				ELSE true END
				FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE rv.id=$1 AND rv.dashboard_id=$2`, review, id).
				Scan(&raw, &rvEpoch, &version, &reviewer, &eligible, &reviewStatus, &submitter, &signer, &authorityCurrent); e != nil {
				return out, e
			}
			if matrix && kind == "publish" && ActorID(ctx) == submitter {
				return out, ErrManagedDenied
			}
			if reviewStatus != "approved" {
				if kind == "publish" {
					governed, err := r.governedDashboard(ctx, tx, id)
					if err != nil {
						return out, err
					}
					if governed {
						return out, fmt.Errorf("%w: 仪表盘须先完成当前复核", ErrManagedConflict)
					}
				}
				return out, pgx.ErrNoRows
			}
			if matrix && kind == "publish" {
				if !eligible || signer != reviewer || !authorityCurrent || version != latest {
					return out, fmt.Errorf("%w: 当前复核资格或审批版本已变化，请重新送审", ErrManagedConflict)
				}
				if ActorID(ctx) != signer {
					return out, ErrManagedDenied
				}
			}
			if rvEpoch != epoch || !eligible {
				return out, ErrManagedConflict
			}
			var audienceBase *int64
			if e := tx.QueryRow(ctx, `SELECT (SELECT base_publication_no FROM sys_dashboard_audience_changes WHERE review_id=$1)`, review).Scan(&audienceBase); e != nil {
				return out, e
			}
			if kind != "rollback" && audienceBase != nil && *audienceBase != number {
				return out, ErrManagedConflict
			}
			var d DashboardDefinition
			if e := json.Unmarshal(raw, &d); e != nil {
				return out, e
			}
			refs, e := r.checkDefinition(ctx, tx, d, reviewer)
			if e != nil {
				return out, e
			}
			if kind == "publish" {
				if e = r.confirmDashboardPublicationPolicy(ctx, tx, review, d, refs); e != nil {
					return out, e
				}
			}
			var previous *int64
			var currentReview *int64
			var was bool
			if e := tx.QueryRow(ctx, `SELECT (SELECT v.number FROM sys_dashboard_releases re JOIN sys_dashboard_reviews rv ON rv.id=re.review_id JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE re.dashboard_id=$2 AND re.review_id IS NOT NULL ORDER BY re.number DESC LIMIT 1),(SELECT review_id FROM sys_dashboard_releases WHERE id=$1),EXISTS(SELECT 1 FROM sys_dashboard_releases WHERE dashboard_id=$2 AND review_id=$3)`, active, id, review).Scan(&previous, &currentReview, &was); e != nil {
				return out, e
			}
			if !was {
				if e := r.checkReviewerBenefit(ctx, tx, id, d, reviewer); e != nil {
					return out, e
				}
			}
			if currentReview != nil && *currentReview == review || kind == "rollback" && !was || kind == "publish" && previous != nil && (version < *previous || active != nil && version == *previous) {
				return out, ErrManagedConflict
			}
			selected = &review
		} else {
			return out, ErrManagedInput
		}
		return appendDashboardRelease(ctx, tx, id, number, epoch, kind, selected)
	})
}
func appendDashboardRelease(ctx context.Context, tx pgx.Tx, id, number, epoch int64, kind string, selected *int64) (ReportCommandResult, error) {
	out := ReportCommandResult{}
	out.Version = number + 1
	if e := tx.QueryRow(ctx, `INSERT INTO sys_dashboard_releases(dashboard_id,number,kind,review_id,lifecycle_epoch,performed_by,performer_name) VALUES($1,$2,$3,$4,$5,$6,(SELECT username FROM sys_users WHERE id=$6)) RETURNING id`, id, out.Version, kind, selected, epoch, ActorID(ctx)).Scan(&out.ID); e != nil {
		return out, e
	}
	var next *int64
	if selected != nil {
		next = &out.ID
	}
	_, e := tx.Exec(ctx, `UPDATE sys_managed_dashboards SET publication_no=$2,active_release_id=$3,updated_at=now() WHERE id=$1`, id, out.Version, next)
	if e == nil && kind == "unpublish" {
		_, e = tx.Exec(ctx, `UPDATE sys_access_requests SET status=CASE WHEN expires_at<=now() THEN 'expired' ELSE 'cancelled' END,decision_reason='看板已下线',version=version+1 WHERE dashboard_id=$1 AND status IN('pending','processing','approved_pending_release')`, id)
	}
	return out, e
}

func (r *ManagedDashboardRepository) Lifecycle(ctx context.Context, key string, id, expected int64, action string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-lifecycle", []any{id, expected, action}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		var owner, version, latest, epoch, publication int64
		var state string
		var published, canArchiveActive bool
		if e := tx.QueryRow(ctx, `SELECT owner_id,draft_version,latest_version,lifecycle_epoch,publication_no,lifecycle,active_release_id IS NOT NULL,
			NOT dbhub_iam_dashboard_matrix_enabled() OR dbhub_dashboard_workflow_admin()
			FROM sys_managed_dashboards WHERE id=$1 FOR UPDATE`, id).
			Scan(&owner, &version, &latest, &epoch, &publication, &state, &published, &canArchiveActive); e != nil {
			return out, e
		}
		if owner != ActorID(ctx) && !ActorAdmin(ctx) {
			return out, ErrManagedDenied
		}
		if version != expected {
			return out, ErrManagedConflict
		}
		next := ""
		switch action {
		case "delete":
			if latest > 0 {
				return out, fmt.Errorf("%w: 已送审的看板请归档", ErrManagedInput)
			}
			next = "deleted"
		case "archive":
			if state != "active" {
				return out, ErrManagedConflict
			}
			if published {
				if !canArchiveActive {
					return out, ErrManagedDenied
				}
				enrolled, err := r.governedDashboard(ctx, tx, id)
				if err != nil {
					return out, err
				}
				if enrolled {
					return out, fmt.Errorf("%w: 请先按独立流程下线已纳管仪表盘", ErrManagedConflict)
				}
			}
			next = "archived"
		case "restore":
			if state != "archived" {
				return out, ErrManagedConflict
			}
			next = "active"
		default:
			return out, ErrManagedInput
		}
		if action == "archive" {
			if _, e := tx.Exec(ctx, `INSERT INTO sys_dashboard_releases(dashboard_id,number,kind,lifecycle_epoch,performed_by,performer_name) VALUES($1,$2,'archive',$3,$4,(SELECT username FROM sys_users WHERE id=$4))`, id, publication+1, epoch, ActorID(ctx)); e != nil {
				return out, e
			}
			publication++
		}
		if _, e := tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET status='withdrawn',comment='看板生命周期已变化' WHERE dashboard_id=$1 AND status='pending'`, id); e != nil {
			return out, e
		}
		if _, e := tx.Exec(ctx, `UPDATE sys_access_requests SET status=CASE WHEN expires_at<=now() THEN 'expired' ELSE 'cancelled' END,decision_reason='看板生命周期已变化',version=version+1 WHERE dashboard_id=$1 AND status IN ('pending','processing','approved_pending_release')`, id); e != nil {
			return out, e
		}
		e := tx.QueryRow(ctx, `UPDATE sys_managed_dashboards SET lifecycle=$2,lifecycle_epoch=lifecycle_epoch+1,draft_version=draft_version+1,publication_no=$3,active_release_id=NULL,updated_at=now() WHERE id=$1 RETURNING draft_version`, id, next, publication).Scan(&out.Version)
		return out, e
	})
}
func (r *ManagedDashboardRepository) Transfer(ctx context.Context, key string, id, version, owner int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-transfer", []int64{id, version, owner}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		if err := guardCustodyTransfer(ctx, tx, availabilityObject{dashboard: id}); err != nil {
			return out, err
		}
		var role *string
		if e := tx.QueryRow(ctx, `SELECT dbhub_member_role($1)`, owner).Scan(&role); e != nil {
			return out, e
		}
		if role == nil || *role == "readonly" {
			return out, ErrManagedInput
		}
		e := tx.QueryRow(ctx, `UPDATE sys_managed_dashboards SET owner_id=$3,draft_version=draft_version+1,updated_at=now() WHERE id=$1 AND draft_version=$2 AND lifecycle='active' RETURNING draft_version`, id, version, owner).Scan(&out.Version)
		return out, e
	})
}

func (r *ManagedDashboardRepository) ReviewIdentity(ctx context.Context, id int64) (int64, int64, error) {
	var dashboard, version int64
	e := r.pool.QueryRow(ctx, `SELECT rv.dashboard_id,v.number FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE rv.id=$1`, id).Scan(&dashboard, &version)
	return dashboard, version, managedError(e)
}
