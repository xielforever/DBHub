package db

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
)

func (r *ManagedDashboardRepository) RequestAudienceAccess(ctx context.Context, key string, id, version int64, tile, reason string, users, groups []int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-audience-access", []any{id, version, tile, reason, users, groups}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		reason = strings.TrimSpace(reason)
		if reason == "" || len([]rune(reason)) > 1000 || strings.ContainsRune(reason, 0) || !validateSubjects(users) || !validateSubjects(groups) || len(users)+len(groups) == 0 || len(users)+len(groups) > 50 {
			return out, fmt.Errorf("%w: 请填写用途，每次选择1至50个当前受众", ErrManagedInput)
		}
		var raw []byte
		var owner, actual int64
		e := tx.QueryRow(ctx, `SELECT draft,owner_id,draft_version FROM sys_managed_dashboards WHERE id=$1 AND lifecycle='active' FOR SHARE`, id).Scan(&raw, &owner, &actual)
		if e != nil {
			return out, e
		}
		if owner != ActorID(ctx) && !ActorAdmin(ctx) {
			return out, ErrManagedDenied
		}
		if actual != version {
			return out, ErrManagedConflict
		}
		var d DashboardDefinition
		if e = json.Unmarshal(raw, &d); e != nil {
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
		for _, user := range users {
			if !containsID(d.UserIDs, user) {
				return out, ErrManagedDenied
			}
		}
		for _, group := range groups {
			if !containsID(d.GroupIDs, group) {
				return out, ErrManagedDenied
			}
		}
		if e = audience(ctx, tx, users, groups); e != nil {
			return out, e
		}
		var publication int64
		var manifest []byte
		e = tx.QueryRow(ctx, `SELECT m.publication_no,rv.manifest FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id WHERE m.id=$1`, report).Scan(&publication, &manifest)
		if e != nil {
			return out, ErrNotFound
		}
		var m ReportManifest
		if e = json.Unmarshal(manifest, &m); e != nil {
			return out, e
		}
		if _, e = tx.Exec(ctx, `UPDATE sys_access_requests SET status='expired',version=version+1 WHERE report_id=$1 AND expires_at<=now() AND status IN('pending','processing','approved_pending_release')`, report); e != nil {
			return out, e
		}
		insert := func(user, group *int64, granted bool) error {
			status := "pending"
			var released *int64
			if granted {
				status = "fulfilled"
				released = &publication
			}
			var created int64
			e := tx.QueryRow(ctx, `INSERT INTO sys_access_requests(requester_id,report_id,dashboard_id,reason,target_user_id,target_group_id,status,actual_publication_no) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(workspace_id,requester_id,report_id,target_key) WHERE status IN('pending','processing','approved_pending_release') DO UPDATE SET reason=sys_access_requests.reason RETURNING id`, ActorID(ctx), report, id, reason, user, group, status, released).Scan(&created)
			if e == nil {
				out.IDs = append(out.IDs, created)
			}
			return e
		}
		for _, user := range users {
			if e = insert(&user, nil, dashboardGrant(ctx, tx, m.UserIDs, m.GroupIDs, user)); e != nil {
				return out, e
			}
		}
		for _, group := range groups {
			if e = insert(nil, &group, containsID(m.GroupIDs, group)); e != nil {
				return out, e
			}
		}
		out.ID = out.IDs[0]
		return out, nil
	})
}
