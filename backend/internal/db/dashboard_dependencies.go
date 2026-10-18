package db

import (
	"context"
	"encoding/json"
)

type DashboardDependency struct {
	ID        int64  `json:"dashboard_id"`
	Name      string `json:"name"`
	Version   int64  `json:"version"`
	Kind      string `json:"kind"`
	Lifecycle string `json:"lifecycle"`
}

const developmentReview = `d.lifecycle='active' AND rv.lifecycle_epoch=d.lifecycle_epoch AND ((rv.status='pending' AND rv.expires_at>now()) OR (rv.status='approved' AND NOT EXISTS(SELECT 1 FROM sys_dashboard_releases rr WHERE rr.review_id=rv.id)))`

func (r *ManagedReportRepository) DashboardDependencies(ctx context.Context, id int64, kind string, limit, offset int) ([]DashboardDependency, int64, error) {
	if _, e := r.Get(ctx, id); e != nil {
		return nil, 0, e
	}
	base := ""
	switch kind {
	case "current":
		base = `SELECT d.id,d.owner_id,v.definition AS definition,v.number AS version,d.lifecycle FROM sys_managed_dashboards d JOIN sys_dashboard_releases re ON re.id=d.active_release_id JOIN sys_dashboard_reviews rv ON rv.id=re.review_id JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE d.lifecycle='active'`
	case "development":
		base = `SELECT d.id,d.owner_id,d.draft AS definition,d.draft_version AS version,d.lifecycle FROM sys_managed_dashboards d WHERE d.lifecycle='active' UNION ALL SELECT d.id,d.owner_id,v.definition,v.number,d.lifecycle FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id WHERE ` + developmentReview
	case "history":
		base = `SELECT d.id,d.owner_id,v.definition,v.number AS version,d.lifecycle FROM sys_dashboard_versions v JOIN sys_managed_dashboards d ON d.id=v.dashboard_id JOIN sys_dashboard_reviews rv ON rv.version_id=v.id WHERE NOT EXISTS(SELECT 1 FROM sys_dashboard_releases re WHERE re.id=d.active_release_id AND re.review_id=rv.id) AND NOT (` + developmentReview + `)`
	default:
		return nil, 0, ErrManagedInput
	}
	query := ` WITH refs AS (` + base + `) SELECT id,owner_id,definition,version,lifecycle FROM refs WHERE EXISTS(SELECT 1 FROM jsonb_array_elements(definition->'tiles') t WHERE (t->>'report_id')::bigint=$1)`
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM (`+query+`) counted`, id).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, query+` ORDER BY id DESC,version DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []DashboardDependency{}
	for rows.Next() {
		var item DashboardDependency
		var owner int64
		var raw []byte
		if e = rows.Scan(&item.ID, &owner, &raw, &item.Version, &item.Lifecycle); e != nil {
			return nil, 0, e
		}
		var d DashboardDefinition
		if e = json.Unmarshal(raw, &d); e != nil {
			return nil, 0, e
		}
		item.Kind = kind
		item.Name = d.Name
		if owner != ActorID(ctx) && !ActorAdmin(ctx) {
			item.ID = 0
			item.Name = "受限仪表盘"
			item.Version = 0
			item.Lifecycle = ""
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}
