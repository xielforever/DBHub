package db

import (
	"context"
	"encoding/json"
)

type DashboardImpact struct {
	Current     int64 `json:"current_dashboards"`
	Blocked     int64 `json:"blocked_dashboards"`
	Development int64 `json:"development_dashboards"`
	History     int64 `json:"historical_versions"`
}

func dashboardImpact(ctx context.Context, q reportQueryer, report int64, hash string) (DashboardImpact, error) {
	var out DashboardImpact
	query := `WITH current_refs AS (
 SELECT DISTINCT d.id,v.id AS version_id,t->>'contract_hash' AS contract_hash FROM sys_managed_dashboards d
 JOIN sys_dashboard_releases re ON re.id=d.active_release_id JOIN sys_dashboard_reviews rv ON rv.id=re.review_id
 JOIN sys_dashboard_versions v ON v.id=rv.version_id CROSS JOIN LATERAL jsonb_array_elements(v.definition->'tiles') t
 WHERE d.lifecycle='active' AND (t->>'report_id')::bigint=$1
 ),development_versions AS (
 SELECT v.id,d.id AS dashboard_id FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id
 JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id WHERE ` + developmentReview + `
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(v.definition->'tiles') t WHERE (t->>'report_id')::bigint=$1)
 ),development_boards AS (
 SELECT d.id FROM sys_managed_dashboards d WHERE d.lifecycle='active' AND EXISTS(SELECT 1 FROM jsonb_array_elements(d.draft->'tiles') t WHERE (t->>'report_id')::bigint=$1)
 UNION SELECT dashboard_id FROM development_versions
 ) SELECT (SELECT count(DISTINCT id) FROM current_refs),
 (SELECT count(DISTINCT id) FROM current_refs WHERE COALESCE(contract_hash,'')<>$2),
 (SELECT count(*) FROM development_boards),
 (SELECT count(*) FROM sys_dashboard_versions v WHERE EXISTS(SELECT 1 FROM jsonb_array_elements(v.definition->'tiles') t WHERE (t->>'report_id')::bigint=$1)
 AND NOT EXISTS(SELECT 1 FROM current_refs c WHERE c.version_id=v.id) AND NOT EXISTS(SELECT 1 FROM development_versions dv WHERE dv.id=v.id))`
	e := q.QueryRow(ctx, query, report, hash).Scan(&out.Current, &out.Blocked, &out.Development, &out.History)
	return out, e
}
func (r *ManagedReportRepository) DashboardImpact(ctx context.Context, id, review int64) (DashboardImpact, error) {
	out := DashboardImpact{}
	if _, e := r.Get(ctx, id); e != nil {
		return out, e
	}
	var raw []byte
	if e := r.pool.QueryRow(ctx, `SELECT rv.manifest FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id WHERE rv.id=$1 AND v.report_id=$2 AND rv.status='approved'`, review, id).Scan(&raw); e != nil {
		return out, managedError(e)
	}
	var m ReportManifest
	if e := json.Unmarshal(raw, &m); e != nil {
		return out, e
	}
	hash := ""
	if m.Contract != nil {
		hash = m.Contract.Hash
	}
	return dashboardImpact(ctx, r.pool, id, hash)
}
