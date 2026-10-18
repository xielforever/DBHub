package db

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
)

func (r *ManagedDashboardRepository) AudienceChange(ctx context.Context, key string, id, base, reviewer int64, users, groups []int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-audience", []any{id, base, reviewer, users, groups}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		var owner, no, next, epoch int64
		var raw []byte
		e := tx.QueryRow(ctx, `SELECT d.owner_id,d.publication_no,d.latest_version+1,d.lifecycle_epoch,v.definition FROM sys_managed_dashboards d JOIN sys_dashboard_releases re ON re.id=d.active_release_id JOIN sys_dashboard_reviews rv ON rv.id=re.review_id JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE d.id=$1 AND d.lifecycle='active' FOR UPDATE OF d`, id).Scan(&owner, &no, &next, &epoch, &raw)
		if e != nil {
			return out, e
		}
		if owner != ActorID(ctx) && !ActorAdmin(ctx) {
			return out, ErrManagedDenied
		}
		if base != no {
			return out, ErrManagedConflict
		}
		var role *string
		if e = tx.QueryRow(ctx, `SELECT dbhub_member_role($1)`, reviewer).Scan(&role); e != nil {
			return out, e
		}
		if reviewer == ActorID(ctx) || role == nil || *role != "admin" {
			return out, ErrManagedInput
		}
		if _, e = tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET status='expired' WHERE dashboard_id=$1 AND status='pending' AND expires_at<=now()`, id); e != nil {
			return out, e
		}
		var pending bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_dashboard_reviews WHERE dashboard_id=$1 AND status='pending')`, id).Scan(&pending); e != nil {
			return out, e
		}
		if pending {
			return out, ErrManagedConflict
		}
		var d DashboardDefinition
		if e = json.Unmarshal(raw, &d); e != nil {
			return out, e
		}
		wasRecipient := dashboardGrant(ctx, tx, d.UserIDs, d.GroupIDs, reviewer)
		d.UserIDs = normalizedIDs(users)
		d.GroupIDs = normalizedIDs(groups)
		if !wasRecipient && dashboardGrant(ctx, tx, d.UserIDs, d.GroupIDs, reviewer) {
			return out, fmt.Errorf("%w: 新增受众的受益人不能审批本次授权变更", ErrManagedInput)
		}
		if _, e = r.checkDefinition(ctx, tx, d, ActorID(ctx)); e != nil {
			return out, e
		}
		if _, e = r.checkDefinition(ctx, tx, d, reviewer); e != nil {
			return out, e
		}
		var vid int64
		if e = tx.QueryRow(ctx, `INSERT INTO sys_dashboard_versions(dashboard_id,number,definition,content_hash,submitted_by,submitter_name) VALUES($1,$2,$3::jsonb,$4,$5,(SELECT username FROM sys_users WHERE id=$5)) RETURNING id`, id, next, string(canonicalReportJSON(d)), reportDigest(d), ActorID(ctx)).Scan(&vid); e != nil {
			return out, e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO sys_dashboard_reviews(version_id,dashboard_id,lifecycle_epoch,reviewer_id) VALUES($1,$2,$3,$4) RETURNING id`, vid, id, epoch, reviewer).Scan(&out.ID); e != nil {
			return out, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO sys_dashboard_audience_changes(review_id,base_publication_no) VALUES($1,$2)`, out.ID, base); e != nil {
			return out, e
		}
		_, e = tx.Exec(ctx, `UPDATE sys_managed_dashboards SET latest_version=$2,updated_at=now() WHERE id=$1`, id, next)
		out.Version = next
		return out, e
	})
}

func (r *ManagedDashboardRepository) CurrentAudience(ctx context.Context, id int64) (*ReportAudienceProposal, error) {
	if _, e := r.Get(ctx, id); e != nil {
		return nil, e
	}
	out := &ReportAudienceProposal{}
	var raw []byte
	e := r.pool.QueryRow(ctx, `SELECT d.publication_no,v.definition FROM sys_managed_dashboards d JOIN sys_dashboard_releases re ON re.id=d.active_release_id JOIN sys_dashboard_reviews rv ON rv.id=re.review_id JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE d.id=$1 AND d.lifecycle='active'`, id).Scan(&out.BaseNo, &raw)
	if e != nil {
		return nil, managedError(e)
	}
	var def DashboardDefinition
	if e = json.Unmarshal(raw, &def); e != nil {
		return nil, e
	}
	out.UserIDs = def.UserIDs
	out.GroupIDs = def.GroupIDs
	out.RequestIDs = []int64{}
	return out, nil
}
