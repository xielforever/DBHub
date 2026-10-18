package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const managedColumns = `m.id,m.owner_id,u.username,m.draft,m.draft_version,m.latest_version,m.publication_no,m.active_release_id,m.updated_at,COALESCE((SELECT c.id FROM sys_connections c WHERE c.user_id=$2 AND c.definition_revision_id=m.definition_revision_id),0),(SELECT count(*) FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id WHERE v.report_id=m.id AND rv.status='pending'),(SELECT re.review_id FROM sys_report_releases re WHERE re.id=m.active_release_id),(SELECT v.number FROM sys_report_releases re JOIN sys_report_reviews rv ON rv.id=re.review_id JOIN sys_report_versions v ON v.id=rv.version_id WHERE re.id=m.active_release_id)`

func scanManaged(row pgx.Row) (*ManagedReport, error) {
	m := &ManagedReport{}
	var raw []byte
	e := row.Scan(&m.ID, &m.OwnerID, &m.OwnerName, &raw, &m.DraftVersion, &m.LatestVersion, &m.PublicationNo, &m.ActiveReleaseID, &m.UpdatedAt, &m.ConnectionID, &m.PendingCount, &m.ActiveReviewID, &m.ActiveVersion)
	if e != nil {
		return nil, managedError(e)
	}
	e = json.Unmarshal(raw, &m.Draft)
	return m, e
}
func (r *ManagedReportRepository) Get(ctx context.Context, id int64) (*ManagedReport, error) {
	return scanManaged(r.pool.QueryRow(ctx, `SELECT `+managedColumns+` FROM sys_managed_reports m JOIN sys_users u ON u.id=m.owner_id WHERE m.id=$1 AND (m.owner_id=$2 OR $3)`, id, ActorID(ctx), ActorAdmin(ctx)))
}
func (r *ManagedReportRepository) List(ctx context.Context, all, pending bool, q string, limit, offset int) ([]ManagedReport, int64, error) {
	all = all && ActorAdmin(ctx)
	args := []any{all, ActorID(ctx), q, pending}
	base := ` FROM sys_managed_reports m JOIN sys_users u ON u.id=m.owner_id WHERE ($1 OR m.owner_id=$2) AND ($3='' OR m.draft->>'name' ILIKE '%'||$3||'%' OR m.draft->>'description' ILIKE '%'||$3||'%') AND (NOT $4 OR EXISTS(SELECT 1 FROM sys_report_versions pv JOIN sys_report_reviews pr ON pr.version_id=pv.id WHERE pv.report_id=m.id AND pr.status='pending'))`
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*)`+base, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT `+managedColumns+base+` ORDER BY m.updated_at DESC,m.id DESC LIMIT $5 OFFSET $6`, append(args, limit, offset)...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []ManagedReport{}
	for rows.Next() {
		v, e := scanManaged(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, *v)
	}
	return out, total, rows.Err()
}
func managedDefinition(ctx context.Context, tx pgx.Tx, id int64) error {
	var valid bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_source_definitions d WHERE d.id=$1 AND d.enabled AND d.visibility='global' AND dbhub_source_visible(d.id,$2))`, id, ActorID(ctx)).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return fmt.Errorf("%w: 正式报表请选择有权使用的已发布数据源修订", ErrManagedInput)
	}
	return nil
}
func (r *ManagedReportRepository) SaveDraft(ctx context.Context, key string, id, expected int64, d ReportDefinition) (ReportCommandResult, error) {
	input := struct {
		ID, Expected int64
		Definition   ReportDefinition
	}{id, expected, d}
	return r.command(ctx, key, "draft", input, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		if e := ValidateDataContract(d.DataContract, d.ChartType); e != nil {
			return out, e
		}
		if e := ValidateContractMapping(d.DataContract, d.ChartConfig); e != nil {
			return out, e
		}
		if e := managedDefinition(ctx, tx, d.DefinitionID); e != nil {
			return out, e
		}
		raw, _ := json.Marshal(d)
		if id == 0 {
			e := tx.QueryRow(ctx, `INSERT INTO sys_managed_reports(owner_id,definition_revision_id,draft) VALUES($1,$2,$3::jsonb) RETURNING id,draft_version`, ActorID(ctx), d.DefinitionID, string(raw)).Scan(&out.ID, &out.Version)
			return out, e
		}
		var owner, version int64
		e := tx.QueryRow(ctx, `SELECT owner_id,draft_version FROM sys_managed_reports WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &version)
		if e != nil {
			return out, e
		}
		if owner != ActorID(ctx) {
			return out, ErrManagedDenied
		}
		if version != expected {
			return out, ErrManagedConflict
		}
		e = tx.QueryRow(ctx, `UPDATE sys_managed_reports SET draft=$2::jsonb,definition_revision_id=$3,draft_version=draft_version+1,updated_at=now() WHERE id=$1 RETURNING id,draft_version`, id, string(raw), d.DefinitionID).Scan(&out.ID, &out.Version)
		return out, e
	})
}
func (r *ManagedReportRepository) Submit(ctx context.Context, key string, id, expected int64) (ReportCommandResult, error) {
	return r.command(ctx, key, "submit", []int64{id, expected}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		var owner, ver, next int64
		var raw []byte
		e := tx.QueryRow(ctx, `SELECT owner_id,draft_version,latest_version+1,draft FROM sys_managed_reports WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &ver, &next, &raw)
		if e != nil {
			return out, e
		}
		if owner != ActorID(ctx) {
			return out, ErrManagedDenied
		}
		if ver != expected {
			return out, ErrManagedConflict
		}
		var d ReportDefinition
		if e = json.Unmarshal(raw, &d); e != nil {
			return out, e
		}
		if e = managedDefinition(ctx, tx, d.DefinitionID); e != nil {
			return out, e
		}
		hash := reportDigest(d)
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_report_versions v JOIN sys_report_reviews rv ON rv.version_id=v.id WHERE v.report_id=$1 AND v.content_hash=$2 AND rv.status='pending')`, id, hash).Scan(&exists); e != nil {
			return out, e
		}
		if exists {
			return out, ErrManagedConflict
		}
		var versionID int64
		e = tx.QueryRow(ctx, `INSERT INTO sys_report_versions(report_id,definition_revision_id,number,definition,content_hash,submitted_by,submitter_name) VALUES($1,$2,$3,$4::jsonb,$5,$6,(SELECT username FROM sys_users WHERE id=$6)) RETURNING id`, id, d.DefinitionID, next, string(raw), hash, ActorID(ctx)).Scan(&versionID)
		if e != nil {
			return out, e
		}
		e = tx.QueryRow(ctx, `INSERT INTO sys_report_reviews(version_id) VALUES($1) RETURNING id`, versionID).Scan(&out.ID)
		if e != nil {
			return out, e
		}
		_, e = tx.Exec(ctx, `UPDATE sys_managed_reports SET latest_version=$2,updated_at=now() WHERE id=$1`, id, next)
		out.Version = next
		return out, e
	})
}
func (r *ManagedReportRepository) Versions(ctx context.Context, id int64, limit, offset int) ([]ReportVersion, int64, error) {
	if _, e := r.Get(ctx, id); e != nil {
		return nil, 0, e
	}
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_report_versions WHERE report_id=$1`, id).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT v.id,v.report_id,v.number,v.definition,v.content_hash,v.submitted_by,v.submitter_name,v.created_at,rv.id,rv.status,rv.decider_name,rv.comment,rv.account_id,COALESCE(a.name,''),rv.manifest,EXISTS(SELECT 1 FROM sys_report_releases re WHERE re.report_id=v.report_id AND re.review_id=rv.id),(SELECT to_jsonb(ac) FROM sys_report_audience_changes ac WHERE ac.review_id=rv.id) FROM sys_report_versions v JOIN sys_report_reviews rv ON rv.version_id=v.id LEFT JOIN sys_report_accounts a ON a.id=rv.account_id WHERE v.report_id=$1 ORDER BY v.number DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []ReportVersion{}
	for rows.Next() {
		var v ReportVersion
		var raw, manifest, change []byte
		if e = rows.Scan(&v.ID, &v.ReportID, &v.Number, &raw, &v.ContentHash, &v.SubmitterID, &v.SubmitterName, &v.CreatedAt, &v.ReviewID, &v.Status, &v.DeciderName, &v.Comment, &v.AccountID, &v.AccountName, &manifest, &v.WasPublished, &change); e != nil {
			return nil, 0, e
		}
		if len(change) > 0 {
			v.AudienceChange = &ReportAudienceProposal{}
			if e = json.Unmarshal(change, v.AudienceChange); e != nil {
				return nil, 0, e
			}
		}
		if e = json.Unmarshal(raw, &v.Definition); e != nil {
			return nil, 0, e
		}
		if len(manifest) > 0 {
			v.Manifest = &ReportManifest{}
			if e = json.Unmarshal(manifest, v.Manifest); e != nil {
				return nil, 0, e
			}
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
func (r *ManagedReportRepository) Decide(ctx context.Context, key string, reviewID int64, decision, validationID, comment string, contractConfirmed bool) (ReportCommandResult, error) {
	return r.command(ctx, key, "review", []any{reviewID, decision, validationID, comment, contractConfirmed}, decision != "withdrawn", decision == "withdrawn", func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: reviewID}
		var status string
		var submitter *int64
		var owner int64
		e := tx.QueryRow(ctx, `SELECT rv.status,v.submitted_by,m.owner_id FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id JOIN sys_managed_reports m ON m.id=v.report_id WHERE rv.id=$1 FOR UPDATE OF rv`, reviewID).Scan(&status, &submitter, &owner)
		if e != nil {
			return out, e
		}
		if status != "pending" {
			return out, ErrManagedConflict
		}
		if decision == "withdrawn" {
			if owner != ActorID(ctx) {
				return out, ErrManagedDenied
			}
		} else if submitter != nil && *submitter == ActorID(ctx) {
			return out, fmt.Errorf("%w: 不得审批自己提交的版本", ErrManagedInput)
		}
		var accountID *int64
		var manifest []byte
		var hash string
		var validID *string
		if decision == "approved" {
			var account, accountVersion, generation int64
			e = tx.QueryRow(ctx, `SELECT account_id,account_version,account_generation,manifest,manifest_hash FROM sys_report_validations WHERE id=$1::uuid AND review_id=$2 AND performed_by=$3 AND created_at>now()-interval '15 minutes'`, validationID, reviewID, ActorID(ctx)).Scan(&account, &accountVersion, &generation, &manifest, &hash)
			if e != nil {
				return out, ErrManagedConflict
			}
			conn, _, a, e := r.loadAccount(ctx, tx, account)
			if e != nil {
				return out, e
			}
			_ = conn
			if a.Version != accountVersion || a.Generation != generation {
				return out, ErrManagedConflict
			}
			var m ReportManifest
			if e = json.Unmarshal(manifest, &m); e != nil {
				return out, e
			}
			if e = r.checkAudienceChange(ctx, tx, reviewID, account, m.UserIDs, m.GroupIDs, true); e != nil {
				return out, e
			}
			if m.Contract != nil && !contractConfirmed {
				return out, fmt.Errorf("%w: 请明确确认SQL、样本和业务范围符合所声明契约", ErrManagedInput)
			}
			if e = audience(ctx, tx, m.UserIDs, m.GroupIDs); e != nil {
				return out, e
			}
			accountID = &account
			validID = &validationID
		}
		_, e = tx.Exec(ctx, `UPDATE sys_report_reviews SET status=$2,decided_by=$3,decider_name=(SELECT username FROM sys_users WHERE id=$3),comment=$4,account_id=$5,manifest=$6::jsonb,manifest_hash=NULLIF($7,''),validation_id=$8::uuid,decided_at=now() WHERE id=$1`, reviewID, decision, ActorID(ctx), comment, accountID, nullableJSON(manifest), hash, validID)
		if e == nil {
			next := "approved_pending_release"
			if decision == "rejected" {
				next = "rejected"
			}
			if decision == "withdrawn" {
				next = "cancelled"
			}
			_, e = tx.Exec(ctx, `UPDATE sys_access_requests SET status=$2,decision_reason=CASE WHEN $2 IN('rejected','cancelled') THEN $3 ELSE decision_reason END,version=version+1 WHERE review_id=$1 AND expires_at>now() AND status IN('processing','approved_pending_release')`, reviewID, next, comment)
		}
		return out, e
	})
}
func nullableJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}
func (r *ManagedReportRepository) Transfer(ctx context.Context, key string, id, expected, owner int64) (ReportCommandResult, error) {
	return r.command(ctx, key, "transfer", []int64{id, expected, owner}, true, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		var valid bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_users u JOIN sys_roles r ON r.code=dbhub_member_role(u.id) WHERE u.id=$1 AND u.is_active AND r.code IN ('admin','developer'))`, owner).Scan(&valid); e != nil {
			return out, e
		}
		if !valid {
			return out, fmt.Errorf("%w: 责任人必须是有效开发者或管理员", ErrManagedInput)
		}
		e := tx.QueryRow(ctx, `UPDATE sys_managed_reports SET owner_id=$3,draft_version=draft_version+1,updated_at=now() WHERE id=$1 AND draft_version=$2 RETURNING id,draft_version`, id, expected, owner).Scan(&out.ID, &out.Version)
		if e == pgx.ErrNoRows {
			e = ErrManagedConflict
		}
		return out, e
	})
}
