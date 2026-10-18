package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (r *ManagedDashboardRepository) Reviews(ctx context.Context, id int64) ([]DashboardReview, error) {
	if _, e := r.Get(ctx, id); e != nil {
		return nil, e
	}
	rows, e := r.pool.Query(ctx, `SELECT rv.id,v.id,v.number,v.definition,v.submitted_by,v.submitter_name,rv.reviewer_id,CASE WHEN rv.status='pending' AND rv.expires_at<=now() THEN 'expired' ELSE rv.status END,rv.comment,rv.expires_at,rv.validated_at,rv.lifecycle_epoch,EXISTS(SELECT 1 FROM sys_dashboard_releases re WHERE re.review_id=rv.id) FROM sys_dashboard_versions v JOIN sys_dashboard_reviews rv ON rv.version_id=v.id WHERE v.dashboard_id=$1 ORDER BY v.number DESC LIMIT 100`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DashboardReview{}
	for rows.Next() {
		var rv DashboardReview
		var raw []byte
		if e = rows.Scan(&rv.ID, &rv.VersionID, &rv.Number, &raw, &rv.SubmitterID, &rv.Submitter, &rv.ReviewerID, &rv.Status, &rv.Comment, &rv.ExpiresAt, &rv.ValidatedAt, &rv.Epoch, &rv.WasPublished); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &rv.Definition); e != nil {
			return nil, e
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}
func (r *ManagedDashboardRepository) ValidateReview(ctx context.Context, key string, review int64, refresh string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-validate", []any{review, refresh}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: review}
		var raw []byte
		var owner, reviewer, assignment int64
		e := tx.QueryRow(ctx, `SELECT v.definition,COALESCE(v.submitted_by,0),rv.reviewer_id,rv.assignment_version FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id WHERE rv.id=$1 AND rv.status='pending' AND rv.expires_at>now() AND rv.lifecycle_epoch=d.lifecycle_epoch AND d.lifecycle='active' FOR UPDATE OF rv,d`, review).Scan(&raw, &owner, &reviewer, &assignment)
		if e != nil {
			return out, e
		}
		if owner == ActorID(ctx) || reviewer != ActorID(ctx) {
			return out, ErrManagedDenied
		}
		var d DashboardDefinition
		if e = json.Unmarshal(raw, &d); e != nil {
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
		_, e = tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET validation=$2::jsonb,validated_at=now() WHERE id=$1`, review, string(evidence))
		return out, e
	})
}
func (r *ManagedDashboardRepository) Decision(ctx context.Context, key string, review int64, decision, comment string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-decision", []any{review, decision, comment}, decision != "withdrawn", true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: review}
		var raw, evidence []byte
		var owner, reviewer int64
		var validated *time.Time
		e := tx.QueryRow(ctx, `SELECT v.definition,COALESCE(v.submitted_by,0),rv.reviewer_id,rv.validated_at,rv.validation FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id WHERE rv.id=$1 AND rv.status='pending' AND rv.expires_at>now() AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch FOR UPDATE OF rv,d`, review).Scan(&raw, &owner, &reviewer, &validated, &evidence)
		if e != nil {
			return out, e
		}
		if decision == "withdrawn" {
			if owner != ActorID(ctx) {
				return out, ErrManagedDenied
			}
		} else {
			if reviewer != ActorID(ctx) || owner == ActorID(ctx) {
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
		}
		if decision != "approved" && decision != "rejected" && decision != "withdrawn" {
			return out, ErrManagedInput
		}
		if decision == "rejected" && comment == "" {
			return out, ErrManagedInput
		}
		_, e = tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET status=$2,comment=$3,decided_by=$4,decided_at=now() WHERE id=$1`, review, decision, comment, ActorID(ctx))
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
		var role *string
		if e := tx.QueryRow(ctx, `SELECT v.definition,COALESCE(v.submitted_by,0) FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id WHERE rv.id=$1 AND rv.status='pending' AND rv.expires_at>now() AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch FOR UPDATE OF rv`, id).Scan(&raw, &owner); e != nil {
			return out, e
		}
		if e := tx.QueryRow(ctx, `SELECT dbhub_member_role($1)`, reviewer).Scan(&role); e != nil {
			return out, e
		}
		if owner == reviewer || role == nil || *role != "admin" {
			return out, ErrManagedInput
		}
		var d DashboardDefinition
		if e := json.Unmarshal(raw, &d); e != nil {
			return out, e
		}
		if _, e := r.checkDefinition(ctx, tx, d, reviewer); e != nil {
			return out, e
		}
		_, e := tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET reviewer_id=$2,assignment_version=assignment_version+1,validation=NULL,validated_at=NULL WHERE id=$1`, id, reviewer)
		return out, e
	})
}
func (r *ManagedDashboardRepository) Release(ctx context.Context, key string, id, expected, review int64, kind string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-release", []any{id, expected, review, kind}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		var number, epoch int64
		var state string
		var active *int64
		if e := tx.QueryRow(ctx, `SELECT publication_no,lifecycle_epoch,lifecycle,active_release_id FROM sys_managed_dashboards WHERE id=$1 FOR UPDATE`, id).Scan(&number, &epoch, &state, &active); e != nil {
			return out, e
		}
		if number != expected || state != "active" {
			return out, ErrManagedConflict
		}
		var selected *int64
		if kind == "unpublish" {
			if active == nil {
				return out, ErrManagedConflict
			}
		} else if kind == "publish" || kind == "rollback" {
			var raw []byte
			var rvEpoch, version, reviewer int64
			var role *string
			if e := tx.QueryRow(ctx, `SELECT v.definition,rv.lifecycle_epoch,v.number,rv.reviewer_id,dbhub_member_role(rv.reviewer_id) FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE rv.id=$1 AND rv.dashboard_id=$2 AND rv.status='approved'`, review, id).Scan(&raw, &rvEpoch, &version, &reviewer, &role); e != nil {
				return out, e
			}
			if rvEpoch != epoch || role == nil || *role != "admin" {
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
			if _, e := r.checkDefinition(ctx, tx, d, reviewer); e != nil {
				return out, e
			}
			var previous *int64
			var currentReview *int64
			var was bool
			if e := tx.QueryRow(ctx, `SELECT (SELECT v.number FROM sys_dashboard_releases re JOIN sys_dashboard_reviews rv ON rv.id=re.review_id JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE re.dashboard_id=$2 AND re.review_id IS NOT NULL ORDER BY re.number DESC LIMIT 1),(SELECT review_id FROM sys_dashboard_releases WHERE id=$1),EXISTS(SELECT 1 FROM sys_dashboard_releases WHERE dashboard_id=$2 AND review_id=$3)`, active, id, review).Scan(&previous, &currentReview, &was); e != nil {
				return out, e
			}
			if currentReview != nil && *currentReview == review || kind == "rollback" && !was || kind == "publish" && previous != nil && (version < *previous || active != nil && version == *previous) {
				return out, ErrManagedConflict
			}
			selected = &review
		} else {
			return out, ErrManagedInput
		}
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
	})
}
func (r *ManagedDashboardRepository) Lifecycle(ctx context.Context, key string, id, expected int64, action string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-lifecycle", []any{id, expected, action}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		var owner, version, latest, epoch, publication int64
		var state string
		if e := tx.QueryRow(ctx, `SELECT owner_id,draft_version,latest_version,lifecycle_epoch,publication_no,lifecycle FROM sys_managed_dashboards WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &version, &latest, &epoch, &publication, &state); e != nil {
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
