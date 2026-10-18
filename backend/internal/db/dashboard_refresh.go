package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/user/dbhub/internal/connector"
)

func dashboardUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	v := hex.EncodeToString(b)
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
}

type DashboardViewTile struct {
	DashboardTile
	Status      string           `json:"status"`
	Report      *DashboardReport `json:"report,omitempty"`
	ExecutionID string           `json:"execution_id,omitempty"`
}
type DashboardView struct {
	Version       int64               `json:"version"`
	ID            int64               `json:"id"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	PublicationNo int64               `json:"publication_no"`
	Epoch         int64               `json:"lifecycle_epoch"`
	Tiles         []DashboardViewTile `json:"tiles"`
	Quality       DashboardQuality    `json:"quality"`
}
type DashboardRefresh struct {
	DashboardView
	ID         string    `json:"refresh_id"`
	Mode       string    `json:"mode"`
	ServerTime time.Time `json:"server_time"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Viewer contracts omit source/account identities while preserving all business semantics.
func (p DashboardReport) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID            int64           `json:"id"`
		Name          string          `json:"name"`
		PublicationNo int64           `json:"publication_no"`
		Version       int64           `json:"version"`
		ChartType     string          `json:"chart_type"`
		ChartConfig   json.RawMessage `json:"chart_config"`
		Contract      any             `json:"data_contract"`
	}{p.ID, p.Name, p.PublicationNo, p.Version, p.ChartType, p.ChartConfig, struct {
		Declaration DataContract `json:"declaration"`
		Hash        string       `json:"hash"`
	}{p.Contract.Declaration, p.Contract.Hash}})
}
func (r *ManagedDashboardRepository) publishedDefinition(ctx context.Context, q reportQueryer, id int64) (*DashboardView, *DashboardDefinition, error) {
	var raw []byte
	view := &DashboardView{ID: id}
	e := q.QueryRow(ctx, `SELECT v.definition,d.publication_no,d.lifecycle_epoch,v.number FROM sys_managed_dashboards d JOIN sys_dashboard_releases re ON re.id=d.active_release_id JOIN sys_dashboard_reviews rv ON rv.id=re.review_id AND rv.status='approved' JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE d.id=$1 AND d.lifecycle='active'`, id).Scan(&raw, &view.PublicationNo, &view.Epoch, &view.Version)
	if e != nil {
		return nil, nil, managedError(e)
	}
	var d DashboardDefinition
	if e = json.Unmarshal(raw, &d); e != nil {
		return nil, nil, e
	}
	if !dashboardGrant(ctx, q, d.UserIDs, d.GroupIDs, ActorID(ctx)) {
		return nil, nil, ErrNotFound
	}
	view.Name = d.Name
	view.Description = d.Description
	view.Quality = d.Quality
	view.Quality.Reason = ""
	return view, &d, nil
}
func (r *ManagedDashboardRepository) Published(ctx context.Context, keyword string, limit, offset int) ([]DashboardView, int64, error) {
	base := ` FROM sys_managed_dashboards d JOIN sys_dashboard_releases re ON re.id=d.active_release_id JOIN sys_dashboard_reviews rv ON rv.id=re.review_id AND rv.status='approved' JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE d.lifecycle='active' AND dbhub_member_role($1) IS NOT NULL AND (v.definition->'user_ids' @> jsonb_build_array($1::bigint) OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(v.definition->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=$1)) AND ($2='' OR v.definition->>'name' ILIKE '%'||$2||'%' OR v.definition->>'description' ILIKE '%'||$2||'%')`
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*)`+base, ActorID(ctx), keyword).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT d.id`+base+` ORDER BY d.updated_at DESC,d.id DESC LIMIT $3 OFFSET $4`, ActorID(ctx), keyword, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, 0, e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, 0, e
	}
	out := []DashboardView{}
	for _, id := range ids {
		view, _, e := r.publishedDefinition(ctx, r.pool, id)
		if e == nil {
			out = append(out, *view)
		}
	}
	return out, total, nil
}
func (r *ManagedDashboardRepository) PublishedGet(ctx context.Context, id int64) (*DashboardView, error) {
	tx, e := r.pool.BeginSnapshot(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	view, d, e := r.publishedDefinition(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	view.Tiles = r.viewTiles(ctx, tx, *d)
	return view, tx.Commit(ctx)
}
func (r *ManagedDashboardRepository) viewTiles(ctx context.Context, q reportQueryer, d DashboardDefinition) []DashboardViewTile {
	out := []DashboardViewTile{}
	for _, t := range d.Tiles {
		tile := DashboardViewTile{DashboardTile: t, Status: "ready"}
		p, e := loadDashboardReport(ctx, q, t.ReportID, ActorID(ctx))
		if e != nil {
			tile.Status = "restricted"
			tile.ReportID = 0
			tile.ContractHash = ""
			tile.ComparisonGroup = ""
		} else if p.Contract.Hash != t.ContractHash {
			tile.Status = "contract_changed"
			tile.Report = p
		} else {
			tile.Report = p
		}
		out = append(out, tile)
	}
	return out
}
func (r *ManagedDashboardRepository) Refresh(ctx context.Context, id int64, mode string, review int64) (*DashboardRefresh, error) {
	// Context metadata expires independently of durable execution receipts.
	if _, e := r.pool.Exec(ctx, `DELETE FROM sys_dashboard_refreshes WHERE id IN(SELECT id FROM sys_dashboard_refreshes WHERE expires_at<now()-interval '1 day' LIMIT 500)`); e != nil {
		return nil, e
	}
	tx, e := r.pool.BeginSnapshot(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var d DashboardDefinition
	view := &DashboardView{ID: id}
	var version, epoch, assignment int64
	if mode == "published" {
		var def *DashboardDefinition
		view, def, e = r.publishedDefinition(ctx, tx, id)
		if e != nil {
			return nil, e
		}
		d = *def
		epoch = view.Epoch
		version = view.Version
	} else if mode == "draft" || mode == "review" {
		var raw []byte
		var owner, reviewer int64
		if mode == "draft" {
			e = tx.QueryRow(ctx, `SELECT draft,draft_version,lifecycle_epoch,owner_id FROM sys_managed_dashboards WHERE id=$1 AND lifecycle='active'`, id).Scan(&raw, &version, &epoch, &owner)
			if e == nil && owner != ActorID(ctx) {
				return nil, ErrManagedDenied
			}
		} else {
			e = tx.QueryRow(ctx, `SELECT v.definition,v.number,d.lifecycle_epoch,COALESCE(v.submitted_by,0),rv.reviewer_id,rv.assignment_version FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id WHERE rv.id=$1 AND d.id=$2 AND rv.status='pending' AND rv.expires_at>now() AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch`, review, id).Scan(&raw, &version, &epoch, &owner, &reviewer, &assignment)
			if e == nil && (reviewer != ActorID(ctx) || owner == ActorID(ctx) || !ActorAdmin(ctx)) {
				return nil, ErrManagedDenied
			}
		}
		if e != nil {
			return nil, managedError(e)
		}
		if e = json.Unmarshal(raw, &d); e != nil {
			return nil, e
		}
		view.Name = d.Name
		view.Description = d.Description
		view.Epoch = epoch
		view.Version = version
		view.Quality = d.Quality
	} else {
		return nil, ErrManagedInput
	}
	out := &DashboardRefresh{DashboardView: *view, ID: dashboardUUID(), Mode: mode}
	var reviewID *int64
	if mode == "review" {
		reviewID = &review
	}
	e = tx.QueryRow(ctx, `INSERT INTO sys_dashboard_refreshes(id,user_id,dashboard_id,publication_no,lifecycle_epoch,mode,draft_version,review_id,definition,review_assignment_version) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10) RETURNING created_at,expires_at`, out.ID, ActorID(ctx), id, view.PublicationNo, epoch, mode, version, reviewID, string(canonicalReportJSON(d)), assignment).Scan(&out.ServerTime, &out.ExpiresAt)
	if e != nil {
		return nil, e
	}
	out.Tiles = r.viewTiles(ctx, tx, d)
	for i := range out.Tiles {
		t := &out.Tiles[i]
		if t.Status != "ready" {
			continue
		}
		t.ExecutionID = dashboardUUID()
		p := t.Report
		_, e = tx.Exec(ctx, `INSERT INTO sys_dashboard_refresh_tiles(refresh_id,tile_key,report_id,report_publication_no,account_version,account_generation,contract_hash,execution_id,status) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8::uuid,'ready')`, out.ID, t.Key, p.ID, p.PublicationNo, p.AccountVersion, p.Generation, p.Contract.Hash, t.ExecutionID)
		if e != nil {
			return nil, e
		}
	}
	return out, tx.Commit(ctx)
}

type DashboardExecution struct {
	Mode                                          string
	DashboardVersion                              int64
	RefreshID, TileKey, ExecutionID, ContractHash string
	DashboardID, DashboardPublicationNo, Epoch    int64
	ExpiresAt                                     time.Time
	Report                                        *ManagedExecution
}

func (r *ManagedDashboardRepository) checkRefresh(ctx context.Context, id string) error {
	var valid bool
	e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_dashboard_refreshes f JOIN sys_managed_dashboards d ON d.id=f.dashboard_id WHERE f.id=$1::uuid AND f.user_id=$2 AND f.member_version=dbhub_member_epoch() AND f.expires_at>now() AND (NOT COALESCE((f.definition->'quality'->>'allow_unknown_freshness')::boolean,false) OR EXISTS(SELECT 1 FROM sys_workspaces WHERE id=dbhub_workspace() AND allow_unknown_freshness)) AND d.lifecycle='active' AND d.lifecycle_epoch=f.lifecycle_epoch AND (
 (f.mode='published' AND d.active_release_id IS NOT NULL AND d.publication_no=f.publication_no AND dbhub_member_role($2) IS NOT NULL AND ((f.definition->'user_ids') @> jsonb_build_array($2::bigint) OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(f.definition->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=$2))) OR
 (f.mode='draft' AND d.owner_id=$2 AND d.draft_version=f.draft_version) OR
 (f.mode='review' AND EXISTS(SELECT 1 FROM sys_dashboard_reviews rv WHERE rv.id=f.review_id AND rv.status='pending' AND rv.reviewer_id=$2 AND rv.assignment_version=f.review_assignment_version AND rv.expires_at>now() AND dbhub_member_role($2)='admin'))))`, id, ActorID(ctx)).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return ErrManagedConflict
	}
	return nil
}
func (r *ManagedDashboardRepository) ResolveTile(ctx context.Context, refresh, tile string) (*DashboardExecution, error) {
	if e := r.checkRefresh(ctx, refresh); e != nil {
		return nil, e
	}
	x := &DashboardExecution{RefreshID: refresh, TileKey: tile}
	var id, no, version, generation int64
	e := r.pool.QueryRow(ctx, `SELECT f.mode,f.dashboard_id,f.publication_no,f.lifecycle_epoch,f.draft_version,f.expires_at,t.report_id,t.report_publication_no,t.account_version,t.account_generation,t.contract_hash,t.execution_id::text FROM sys_dashboard_refreshes f JOIN sys_dashboard_refresh_tiles t ON t.refresh_id=f.id WHERE f.id=$1::uuid AND f.user_id=$2 AND t.tile_key=$3`, refresh, ActorID(ctx), tile).Scan(&x.Mode, &x.DashboardID, &x.DashboardPublicationNo, &x.Epoch, &x.DashboardVersion, &x.ExpiresAt, &id, &no, &version, &generation, &x.ContractHash, &x.ExecutionID)
	if e != nil {
		return nil, managedError(e)
	}
	x.Report, e = r.reports.ResolvePublished(ctx, id, no)
	if e != nil {
		return nil, e
	}
	if x.Report.Manifest.Contract == nil || x.Report.Manifest.Contract.Hash != x.ContractHash || x.Report.AccountVersion != version || x.Report.Generation != generation {
		return nil, ErrManagedConflict
	}
	x.Report.AdditionalCheck = func(ctx context.Context) error { return r.checkRefresh(ctx, refresh) }
	return x, nil
}
func (r *ManagedDashboardRepository) CompleteTile(ctx context.Context, x *DashboardExecution, result *connector.ExecResult) (ContractResult, error) {
	out, e := ValidateContractResult(x.Report.Manifest.Contract, result, time.Now())
	if e != nil {
		return out, e
	}
	var allowUnknown bool
	if e = r.pool.QueryRow(ctx, `SELECT COALESCE((definition->'quality'->>'allow_unknown_freshness')::boolean,false) FROM sys_dashboard_refreshes WHERE id=$1::uuid AND user_id=$2`, x.RefreshID, ActorID(ctx)).Scan(&allowUnknown); e != nil {
		return out, e
	}
	if out.Freshness == "unknown" && !allowUnknown {
		return out, fmt.Errorf("%w: 业务水位未知，未获批允许未知时效", ErrManagedInput)
	}
	if e = r.reports.CheckExecution(ctx, x.Report); e != nil {
		return out, e
	}
	_, e = r.pool.Exec(ctx, `UPDATE sys_dashboard_refresh_tiles SET status='succeeded',queried_at=$3,data_as_of=$4 WHERE refresh_id=$1::uuid AND tile_key=$2`, x.RefreshID, x.TileKey, out.QueryCompletedAt, out.DataAsOf)
	return out, e
}
func (r *ManagedDashboardRepository) AcquireLease(ctx context.Context, x *DashboardExecution) (func(), error) {
	if e := r.checkRefresh(ctx, x.RefreshID); e != nil {
		return nil, e
	}
	return r.reports.AcquireExecutionLease(ctx, x.Report)
}
