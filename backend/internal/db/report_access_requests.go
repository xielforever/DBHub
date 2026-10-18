package db

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type AccessRequest struct {
	TargetUserID   *int64    `json:"target_user_id"`
	TargetGroupID  *int64    `json:"target_group_id"`
	DecisionReason string    `json:"decision_reason"`
	ID             int64     `json:"id"`
	RequesterID    int64     `json:"requester_id"`
	ReportID       int64     `json:"report_id,omitempty"`
	DashboardID    *int64    `json:"dashboard_id,omitempty"`
	Reason         string    `json:"reason"`
	Status         string    `json:"status"`
	Version        int64     `json:"version"`
	AssignedTo     *int64    `json:"assigned_to"`
	ReviewID       *int64    `json:"review_id"`
	ActualNo       *int64    `json:"actual_publication_no"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func (r *ManagedReportRepository) AccessRequests(ctx context.Context) ([]AccessRequest, error) {
	if _, e := r.pool.Exec(ctx, `UPDATE sys_access_requests SET status='expired',version=version+1 WHERE id IN(SELECT id FROM sys_access_requests WHERE expires_at<=now() AND status IN('pending','processing','approved_pending_release') AND (requester_id=$1 OR $2) LIMIT 500)`, ActorID(ctx), ActorAdmin(ctx)); e != nil {
		return nil, e
	}
	rows, e := r.pool.Query(ctx, `SELECT id,requester_id,report_id,dashboard_id,reason,target_user_id,target_group_id,decision_reason,CASE WHEN expires_at<=now() AND status IN('pending','processing','approved_pending_release') THEN 'expired' ELSE status END,version,assigned_to,review_id,actual_publication_no,expires_at FROM sys_access_requests WHERE requester_id=$1 OR target_user_id=$1 OR $2 ORDER BY id DESC LIMIT 100`, ActorID(ctx), ActorAdmin(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []AccessRequest{}
	for rows.Next() {
		var a AccessRequest
		if e = rows.Scan(&a.ID, &a.RequesterID, &a.ReportID, &a.DashboardID, &a.Reason, &a.TargetUserID, &a.TargetGroupID, &a.DecisionReason, &a.Status, &a.Version, &a.AssignedTo, &a.ReviewID, &a.ActualNo, &a.ExpiresAt); e != nil {
			return nil, e
		}
		if !ActorAdmin(ctx) {
			a.ReportID = 0
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *ManagedDashboardRepository) RequestAccess(ctx context.Context, key string, id int64, tile, reason string) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "access-request", []any{id, tile, reason}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		reason = strings.TrimSpace(reason)
		if reason == "" || len([]rune(reason)) > 1000 || strings.ContainsRune(reason, 0) {
			return out, ErrManagedInput
		}
		_, d, e := r.publishedDefinition(ctx, tx, id)
		if e != nil {
			return out, e
		}
		report := int64(0)
		for _, t := range d.Tiles {
			if t.Key == tile {
				report = t.ReportID
			}
		}
		if report == 0 {
			return out, ErrNotFound
		}
		var available bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id JOIN sys_report_accounts a ON a.id=rv.account_id AND a.enabled JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.enabled WHERE m.id=$1)`, report).Scan(&available); e != nil {
			return out, e
		}
		if !available {
			return out, ErrNotFound
		}
		if _, e = tx.Exec(ctx, `UPDATE sys_access_requests SET status='expired',version=version+1 WHERE requester_id=$1 AND report_id=$2 AND expires_at<=now() AND status IN('pending','processing','approved_pending_release')`, ActorID(ctx), report); e != nil {
			return out, e
		}
		e = tx.QueryRow(ctx, `INSERT INTO sys_access_requests(requester_id,report_id,dashboard_id,reason) VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,requester_id,report_id,target_key) WHERE status IN('pending','processing','approved_pending_release') DO UPDATE SET reason=sys_access_requests.reason RETURNING id,version`, ActorID(ctx), report, id, reason).Scan(&out.ID, &out.Version)
		return out, e
	})
}
func (r *ManagedReportRepository) AccessAction(ctx context.Context, key string, id, version, assignee int64, action, reason string) (ReportCommandResult, error) {
	return r.command(ctx, key, "access-"+action, []any{id, version, assignee, reason}, action != "cancel", false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		if len([]rune(reason)) > 1000 || strings.ContainsRune(reason, 0) {
			return out, ErrManagedInput
		}
		var requester, prev int64
		var status string
		var expires time.Time
		if e := tx.QueryRow(ctx, `SELECT requester_id,version,status,expires_at FROM sys_access_requests WHERE id=$1 FOR UPDATE`, id).Scan(&requester, &prev, &status, &expires); e != nil {
			return out, e
		}
		if version != prev || !expires.After(time.Now()) || status == "fulfilled" || status == "rejected" || status == "cancelled" || status == "expired" {
			return out, ErrManagedConflict
		}
		switch action {
		case "cancel":
			if requester != ActorID(ctx) {
				return out, ErrManagedDenied
			}
			status = "cancelled"
		case "reject":
			if strings.TrimSpace(reason) == "" {
				return out, ErrManagedInput
			}
			status = "rejected"
		case "assign":
			var role *string
			if e := tx.QueryRow(ctx, `SELECT dbhub_member_role($1)`, assignee).Scan(&role); e != nil {
				return out, e
			}
			if role == nil || *role != "admin" {
				return out, ErrManagedInput
			}
		default:
			return out, ErrManagedInput
		}
		var selected *int64
		if assignee > 0 {
			selected = &assignee
		}
		e := tx.QueryRow(ctx, `UPDATE sys_access_requests SET status=$2,assigned_to=COALESCE($3,assigned_to),decision_reason=CASE WHEN $4<>'' THEN $4 ELSE decision_reason END,version=version+1 WHERE id=$1 RETURNING version`, id, status, selected, strings.TrimSpace(reason)).Scan(&out.Version)
		return out, e
	})
}

type AudienceChange struct {
	BaseNo     int64   `json:"base_publication_no"`
	UserIDs    []int64 `json:"user_ids"`
	GroupIDs   []int64 `json:"group_ids"`
	RequestIDs []int64 `json:"request_ids"`
}
type ReportAudienceProposal struct {
	AudienceChange
	AccountID int64 `json:"account_id"`
}

func (r *ManagedReportRepository) CurrentAudience(ctx context.Context, id int64) (*ReportAudienceProposal, error) {
	if _, e := r.Get(ctx, id); e != nil {
		return nil, e
	}
	out := &ReportAudienceProposal{}
	var raw []byte
	e := r.pool.QueryRow(ctx, `SELECT m.publication_no,rv.account_id,rv.manifest FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id WHERE m.id=$1`, id).Scan(&out.BaseNo, &out.AccountID, &raw)
	if e != nil {
		return nil, managedError(e)
	}
	var m ReportManifest
	if e = json.Unmarshal(raw, &m); e != nil {
		return nil, e
	}
	out.UserIDs = m.UserIDs
	out.GroupIDs = m.GroupIDs
	out.RequestIDs = []int64{}
	return out, nil
}

func groupVersions(ctx context.Context, tx pgx.Tx, groups []int64) (map[string]int64, error) {
	out := map[string]int64{}
	for _, gid := range groups {
		var ver int64
		if e := tx.QueryRow(ctx, `SELECT version FROM sys_user_groups WHERE id=$1`, gid).Scan(&ver); e != nil {
			return nil, e
		}
		out[fmt.Sprint(gid)] = ver
	}
	return out, nil
}
func (r *ManagedReportRepository) AudienceChange(ctx context.Context, key string, id int64, in AudienceChange) (ReportCommandResult, error) {
	return r.command(ctx, key, "report-audience", []any{id, in}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		if e := audience(ctx, tx, in.UserIDs, in.GroupIDs); e != nil {
			return out, e
		}
		if !validateSubjects(in.RequestIDs) {
			return out, ErrManagedInput
		}
		var raw, manifest []byte
		var owner, no, next, account int64
		e := tx.QueryRow(ctx, `SELECT m.owner_id,m.publication_no,m.latest_version+1,v.definition,rv.manifest,rv.account_id FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id JOIN sys_report_versions v ON v.id=rv.version_id WHERE m.id=$1 FOR UPDATE OF m`, id).Scan(&owner, &no, &next, &raw, &manifest, &account)
		if e != nil {
			return out, e
		}
		if owner != ActorID(ctx) && !ActorAdmin(ctx) {
			return out, ErrManagedDenied
		}
		if no != in.BaseNo {
			return out, ErrManagedConflict
		}
		var d ReportDefinition
		if e = json.Unmarshal(raw, &d); e != nil {
			return out, e
		}
		if _, _, _, e = r.loadAccount(ctx, tx, account); e != nil {
			return out, e
		}
		groups, e := groupVersions(ctx, tx, in.GroupIDs)
		if e != nil {
			return out, e
		}
		for _, request := range in.RequestIDs {
			var ok bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_access_requests WHERE id=$1 AND report_id=$2 AND status IN('pending','processing','approved_pending_release') AND expires_at>now() AND ((target_user_id IS NOT NULL AND (target_user_id=ANY($3::bigint[]) OR target_user_id IN(SELECT user_id FROM sys_user_group_members WHERE group_id=ANY($4::bigint[])))) OR target_group_id=ANY($4::bigint[])))`, request, id, nonNilIDs(in.UserIDs), nonNilIDs(in.GroupIDs)).Scan(&ok)
			if e != nil {
				return out, e
			}
			if !ok {
				return out, ErrManagedConflict
			}
		}
		var vid int64
		if e = tx.QueryRow(ctx, `INSERT INTO sys_report_versions(report_id,definition_revision_id,number,definition,content_hash,submitted_by,submitter_name) VALUES($1,$2,$3,$4::jsonb,$5,$6,(SELECT username FROM sys_users WHERE id=$6)) RETURNING id`, id, d.DefinitionID, next, string(raw), reportDigest(d), ActorID(ctx)).Scan(&vid); e != nil {
			return out, e
		}
		if e = tx.QueryRow(ctx, `INSERT INTO sys_report_reviews(version_id) VALUES($1) RETURNING id`, vid).Scan(&out.ID); e != nil {
			return out, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO sys_report_audience_changes(review_id,report_id,base_publication_no,account_id,user_ids,group_ids,group_versions,request_ids) VALUES($1,$2,$3,$4,$5::bigint[],$6::bigint[],$7::jsonb,$8::bigint[])`, out.ID, id, no, account, nonNilIDs(in.UserIDs), nonNilIDs(in.GroupIDs), string(canonicalReportJSON(groups)), nonNilIDs(in.RequestIDs)); e != nil {
			return out, e
		}
		if _, e = tx.Exec(ctx, `UPDATE sys_access_requests SET status='processing',review_id=$1,version=version+1 WHERE id=ANY($2::bigint[])`, out.ID, nonNilIDs(in.RequestIDs)); e != nil {
			return out, e
		}
		_, e = tx.Exec(ctx, `UPDATE sys_managed_reports SET latest_version=$2,updated_at=now() WHERE id=$1`, id, next)
		out.Version = next
		return out, e
	})
}

// Every gate rechecks the linked proposal, group versions and terminal requests.
func (r *ManagedReportRepository) checkAudienceChange(ctx context.Context, tx pgx.Tx, review, account int64, users, groups []int64, approver bool) error {
	var base, no, fixed int64
	var us, gs, requests []int64
	var snapshots []byte
	e := tx.QueryRow(ctx, `SELECT c.base_publication_no,m.publication_no,c.account_id,c.user_ids,c.group_ids,c.request_ids,c.group_versions FROM sys_report_audience_changes c JOIN sys_managed_reports m ON m.id=c.report_id WHERE c.review_id=$1`, review).Scan(&base, &no, &fixed, &us, &gs, &requests, &snapshots)
	if e == pgx.ErrNoRows {
		if approver {
			return reportReviewerBenefit(ctx, tx, review, users, groups, ActorID(ctx))
		}
		return nil
	}
	if e != nil {
		return e
	}
	if base != no || account != fixed || reportDigest(normalizedIDs(users)) != reportDigest(normalizedIDs(us)) || reportDigest(normalizedIDs(groups)) != reportDigest(normalizedIDs(gs)) {
		return ErrManagedConflict
	}
	current, e := groupVersions(ctx, tx, gs)
	if e != nil {
		return ErrManagedConflict
	}
	var frozen map[string]int64
	if json.Unmarshal(snapshots, &frozen) != nil || reportDigest(current) != reportDigest(frozen) {
		return ErrManagedConflict
	}
	for _, id := range requests {
		var linked *int64
		var status string
		var expires time.Time
		e = tx.QueryRow(ctx, `SELECT review_id,status,expires_at FROM sys_access_requests WHERE id=$1 FOR UPDATE`, id).Scan(&linked, &status, &expires)
		if e != nil {
			return e
		}
		if linked == nil || *linked != review || !expires.After(time.Now()) || (status != "processing" && status != "approved_pending_release") {
			return ErrManagedConflict
		}
	}

	if approver {
		if e = reportReviewerBenefit(ctx, tx, review, users, groups, ActorID(ctx)); e != nil {
			return e
		}
		var requester bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_access_requests WHERE id=ANY($1::bigint[]) AND requester_id=$2)`, nonNilIDs(requests), ActorID(ctx)).Scan(&requester); e != nil {
			return e
		}
		if requester {
			return ErrManagedDenied
		}
	}
	return nil
}
