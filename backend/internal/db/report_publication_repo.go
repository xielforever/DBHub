package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A viewer grant delegates only an approved query. It does not grant source
// browsing or personal connection rights, even to another report's author.
const reportAudience = `dbhub_member_role($2) IS NOT NULL AND (
 rv.manifest->'user_ids' @> jsonb_build_array($2::bigint) OR EXISTS(
 SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g
 JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=$2))`

func (r *ManagedReportRepository) PublishedGet(ctx context.Context, id int64) (*PublishedReport, error) {
	p := &PublishedReport{}
	e := r.pool.QueryRow(ctx, `SELECT m.id,v.definition->>'name',v.definition->>'description',v.number,m.publication_no,v.definition->>'chart_type',v.definition->'chart_config'
 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id
 JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved' JOIN sys_report_versions v ON v.id=rv.version_id
 JOIN sys_report_accounts a ON a.id=rv.account_id AND a.enabled JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.enabled AND d.visibility='global'
 WHERE m.id=$1 AND `+reportAudience, id, ActorID(ctx)).Scan(&p.ID, &p.Name, &p.Description, &p.Version, &p.PublicationNo, &p.ChartType, &p.ChartConfig)
	return p, managedError(e)
}

func (r *ManagedReportRepository) ValidationCandidate(ctx context.Context, reviewID, accountID int64, users, groups []int64) (*ManagedExecution, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = reportActor(ctx, tx, true, false); e != nil {
		return nil, e
	}
	if e = audience(ctx, tx, users, groups); e != nil {
		return nil, e
	}
	out := &ManagedExecution{Validation: true, ReviewID: reviewID, AccountID: accountID, UserIDs: normalizedIDs(users), GroupIDs: normalizedIDs(groups)}
	var raw []byte
	var hash string
	var submitter *int64
	e = tx.QueryRow(ctx, `SELECT v.report_id,v.number,v.definition,v.content_hash,v.submitted_by,v.id,v.submitter_name FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id WHERE rv.id=$1 AND rv.status='pending'`, reviewID).Scan(&out.ReportID, &out.Version, &raw, &hash, &submitter, &out.Manifest.VersionID, &out.Author)
	if e != nil {
		return nil, managedError(e)
	}
	if submitter != nil && *submitter == ActorID(ctx) {
		return nil, fmt.Errorf("%w: 不得审阅验证自己提交的版本", ErrManagedInput)
	}
	if e = json.Unmarshal(raw, &out.Definition); e != nil {
		return nil, e
	}
	if e = r.checkAudienceChange(ctx, tx, reviewID, accountID, users, groups, true); e != nil {
		return nil, e
	}
	c, p, a, e := r.loadAccount(ctx, tx, accountID)
	if e != nil {
		return nil, e
	}
	if a.DefinitionID != out.Definition.DefinitionID {
		return nil, fmt.Errorf("%w: 执行账号必须绑定同一数据源修订", ErrManagedInput)
	}
	out.Connection, out.Password, out.AccountVersion, out.Generation = c, p, a.Version, a.Generation
	out.Manifest.Contract = approvedContract(out.Definition.DataContract, WorkspaceID(ctx), a.DefinitionID, accountID, out.Definition.Database)
	out.Manifest.ContentHash = hash
	out.Manifest.AccountID = accountID
	out.Manifest.DefinitionID = a.DefinitionID
	out.Manifest.UserIDs = out.UserIDs
	out.Manifest.GroupIDs = out.GroupIDs
	out.Manifest.MaxRows = 1000
	out.Manifest.TimeoutMS = 30000
	out.Hash = reportDigest(out.Manifest)
	return out, nil
}
func (r *ManagedReportRepository) RegisterValidation(ctx context.Context, id string, x *ManagedExecution) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = reportActor(ctx, tx, true, false); e != nil {
		return e
	}
	var status string
	if e = tx.QueryRow(ctx, `SELECT status FROM sys_report_reviews WHERE id=$1 FOR UPDATE`, x.ReviewID).Scan(&status); e != nil {
		return managedError(e)
	}
	if status != "pending" {
		return ErrManagedConflict
	}
	_, _, a, e := r.loadAccount(ctx, tx, x.AccountID)
	if e != nil {
		return e
	}
	if a.Version != x.AccountVersion || a.Generation != x.Generation {
		return ErrManagedConflict
	}
	if e = audience(ctx, tx, x.UserIDs, x.GroupIDs); e != nil {
		return e
	}
	raw, _ := json.Marshal(x.Manifest)
	_, e = tx.Exec(ctx, `INSERT INTO sys_report_validations(id,review_id,account_id,account_generation,account_version,manifest,manifest_hash,performed_by) VALUES($1::uuid,$2,$3,$4,$5,$6::jsonb,$7,$8)`, id, x.ReviewID, x.AccountID, x.Generation, x.AccountVersion, string(raw), x.Hash, ActorID(ctx))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (r *ManagedReportRepository) ResolvePublished(ctx context.Context, id, publicationNo int64) (*ManagedExecution, error) {
	out := &ManagedExecution{ReportID: id, PublicationNo: publicationNo}
	var raw, manifest []byte
	e := r.pool.QueryRow(ctx, `SELECT v.number,v.definition,rv.id,rv.account_id,rv.manifest,rv.manifest_hash,m.publication_no,v.submitter_name,rv.decider_name,re.performer_name
 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id
 JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved' JOIN sys_report_versions v ON v.id=rv.version_id
 WHERE m.id=$1 AND `+reportAudience, id, ActorID(ctx)).Scan(&out.Version, &raw, &out.ReviewID, &out.AccountID, &manifest, &out.Hash, &out.PublicationNo, &out.Author, &out.Approver, &out.Publisher)
	if e != nil {
		return nil, managedError(e)
	}
	if out.PublicationNo != publicationNo {
		return nil, ErrManagedConflict
	}
	if e = json.Unmarshal(raw, &out.Definition); e != nil {
		return nil, e
	}
	if e = json.Unmarshal(manifest, &out.Manifest); e != nil {
		return nil, e
	}
	c, p, a, e := r.loadAccount(ctx, r.pool, out.AccountID)
	if e != nil {
		return nil, e
	}
	if a.DefinitionID != out.Definition.DefinitionID || out.Manifest.AccountID != a.ID || out.Manifest.ContentHash != reportDigest(out.Definition) || out.Hash != reportDigest(out.Manifest) {
		return nil, ErrManagedDenied
	}
	out.Connection, out.Password, out.AccountVersion, out.Generation = c, p, a.Version, a.Generation
	return out, nil
}
func (r *ManagedReportRepository) CheckExecution(ctx context.Context, x *ManagedExecution) error {
	var latest *ManagedExecution
	var e error
	if x.Validation {
		latest, e = r.ValidationCandidate(ctx, x.ReviewID, x.AccountID, x.UserIDs, x.GroupIDs)
	} else {
		latest, e = r.ResolvePublished(ctx, x.ReportID, x.PublicationNo)
	}
	if e != nil {
		return e
	}
	if latest.Hash != x.Hash || latest.AccountVersion != x.AccountVersion || latest.Generation != x.Generation {
		return ErrManagedConflict
	}
	if x.AdditionalCheck != nil {
		return x.AdditionalCheck(ctx)
	}
	return nil
}
func (r *ManagedReportRepository) Release(ctx context.Context, key string, id, expected, reviewID int64, kind string, expectedDashboards int64) (ReportCommandResult, error) {
	return r.command(ctx, key, "release", []any{id, expected, reviewID, kind, expectedDashboards}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		var number int64
		var active *int64
		e := tx.QueryRow(ctx, `SELECT publication_no,active_release_id FROM sys_managed_reports WHERE id=$1 FOR UPDATE`, id).Scan(&number, &active)
		if e != nil {
			return out, e
		}
		if number != expected {
			return out, ErrManagedConflict
		}
		var selected *int64
		if kind != "unpublish" {
			var accountID, targetVersion int64
			var raw []byte
			e = tx.QueryRow(ctx, `SELECT rv.account_id,rv.manifest,v.number FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id WHERE rv.id=$1 AND v.report_id=$2 AND rv.status='approved'`, reviewID, id).Scan(&accountID, &raw, &targetVersion)
			if e != nil {
				return out, ErrManagedConflict
			}
			if _, _, _, e = r.loadAccount(ctx, tx, accountID); e != nil {
				return out, e
			}
			var m ReportManifest
			if e = json.Unmarshal(raw, &m); e != nil {
				return out, e
			}
			contractHash := ""
			if m.Contract != nil {
				contractHash = m.Contract.Hash
			}
			impact, e := dashboardImpact(ctx, tx, id, contractHash)
			if e != nil {
				return out, e
			}
			if impact.Blocked != expectedDashboards {
				return out, fmt.Errorf("%w: 请确认将因口径变化被阻断的当前仪表盘数量（%d）", ErrManagedConflict, impact.Blocked)
			}
			var wasPublished bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_report_releases WHERE report_id=$1 AND review_id=$2)`, id, reviewID).Scan(&wasPublished); e != nil {
				return out, e
			}
			// Proposal bases and unfinished access requests are first-release
			// conditions. A historical restore still validates the current
			// account, audience, impact and publication number above/below.
			if !wasPublished {
				if e = r.checkAudienceChange(ctx, tx, reviewID, accountID, m.UserIDs, m.GroupIDs, false); e != nil {
					return out, e
				}
			}
			if e = audience(ctx, tx, m.UserIDs, m.GroupIDs); e != nil {
				return out, e
			}
			if kind == "rollback" && !wasPublished {
				return out, ErrManagedConflict
			}
			if active == nil && kind == "publish" {
				var previous *int64
				if e = tx.QueryRow(ctx, `SELECT (SELECT v.number FROM sys_report_releases re JOIN sys_report_reviews rv ON rv.id=re.review_id JOIN sys_report_versions v ON v.id=rv.version_id WHERE re.report_id=$1 AND re.review_id IS NOT NULL ORDER BY re.number DESC LIMIT 1)`, id).Scan(&previous); e != nil {
					return out, e
				}
				if previous != nil && targetVersion < *previous {
					return out, ErrManagedConflict
				}
			}
			if active != nil {
				var currentReview, currentVersion int64
				if e = tx.QueryRow(ctx, `SELECT re.review_id,v.number FROM sys_report_releases re JOIN sys_report_reviews rv ON rv.id=re.review_id JOIN sys_report_versions v ON v.id=rv.version_id WHERE re.id=$1`, *active).Scan(&currentReview, &currentVersion); e != nil {
					return out, e
				}
				if currentReview == reviewID || kind == "publish" && targetVersion < currentVersion {
					return out, ErrManagedConflict
				}
			}
			selected = &reviewID
		} else if active == nil {
			return out, ErrManagedConflict
		}
		out.Version = number + 1
		e = tx.QueryRow(ctx, `INSERT INTO sys_report_releases(report_id,number,kind,review_id,performed_by,performer_name) VALUES($1,$2,$3,$4,$5,(SELECT username FROM sys_users WHERE id=$5)) RETURNING id`, id, out.Version, kind, selected, ActorID(ctx)).Scan(&out.ID)
		if e != nil {
			return out, e
		}
		var next *int64
		if selected != nil {
			next = &out.ID
		}
		_, e = tx.Exec(ctx, `UPDATE sys_managed_reports SET publication_no=$2,active_release_id=$3,updated_at=now() WHERE id=$1`, id, out.Version, next)
		if e == nil && selected != nil {
			_, e = tx.Exec(ctx, `UPDATE sys_access_requests ar SET status='fulfilled',actual_publication_no=$2,version=version+1 WHERE ar.review_id=$1 AND ar.status='approved_pending_release' AND ar.expires_at>now() AND (ar.target_user_id IS NULL OR dbhub_member_role(ar.target_user_id) IS NOT NULL) AND EXISTS(SELECT 1 FROM sys_report_reviews rv WHERE rv.id=$1 AND ((rv.manifest->'user_ids') @> jsonb_build_array(ar.target_user_id) OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=ar.target_user_id) OR rv.manifest->'group_ids' @> jsonb_build_array(ar.target_group_id)))`, reviewID, out.Version)
		}
		if e == nil && kind == "unpublish" {
			_, e = tx.Exec(ctx, `UPDATE sys_access_requests SET status=CASE WHEN expires_at<=now() THEN 'expired' ELSE 'cancelled' END,decision_reason='报表已下线',version=version+1 WHERE report_id=$1 AND status IN('pending','processing','approved_pending_release')`, id)
		}
		return out, e
	})
}
func (r *ManagedReportRepository) Releases(ctx context.Context, id int64, limit, offset int) ([]ReportRelease, int64, error) {
	if _, e := r.Get(ctx, id); e != nil {
		return nil, 0, e
	}
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_report_releases WHERE report_id=$1`, id).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT re.id,re.number,re.kind,re.review_id,v.number,re.performer_name,re.created_at FROM sys_report_releases re LEFT JOIN sys_report_reviews rv ON rv.id=re.review_id LEFT JOIN sys_report_versions v ON v.id=rv.version_id WHERE re.report_id=$1 ORDER BY re.number DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []ReportRelease{}
	for rows.Next() {
		var re ReportRelease
		if e = rows.Scan(&re.ID, &re.Number, &re.Kind, &re.ReviewID, &re.Version, &re.Performer, &re.CreatedAt); e != nil {
			return nil, 0, e
		}
		out = append(out, re)
	}
	return out, total, rows.Err()
}
func (r *ManagedReportRepository) Published(ctx context.Context, q string, limit, offset int) ([]PublishedReport, int64, error) {
	base := ` FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id
 JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved' JOIN sys_report_versions v ON v.id=rv.version_id
 JOIN sys_report_accounts a ON a.id=rv.account_id AND a.enabled JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.enabled AND d.visibility='global'
 WHERE ($1='' OR v.definition->>'name' ILIKE '%'||$1||'%' OR v.definition->>'description' ILIKE '%'||$1||'%') AND ` + reportAudience
	args := []any{q, ActorID(ctx)}
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*)`+base, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT m.id,v.definition->>'name',v.definition->>'description',v.number,m.publication_no,v.definition->>'chart_type',v.definition->'chart_config'`+base+` ORDER BY re.created_at DESC,m.id DESC LIMIT $3 OFFSET $4`, append(args, limit, offset)...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []PublishedReport{}
	for rows.Next() {
		var p PublishedReport
		if e = rows.Scan(&p.ID, &p.Name, &p.Description, &p.Version, &p.PublicationNo, &p.ChartType, &p.ChartConfig); e != nil {
			return nil, 0, e
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}
