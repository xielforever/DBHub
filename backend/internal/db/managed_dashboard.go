package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type DashboardTile struct {
	Key             string `json:"key"`
	ReportID        int64  `json:"report_id"`
	Required        bool   `json:"required"`
	Width           string `json:"width"`
	ComparisonGroup string `json:"comparison_group"`
	ContractHash    string `json:"contract_hash"`
	BindingPolicy   string `json:"binding_policy"`
}
type DashboardQuality struct {
	AllowUnknown     bool   `json:"allow_unknown_freshness"`
	Reason           string `json:"reason"`
	MaxWatermarkSkew int64  `json:"max_watermark_skew_seconds"`
}
type DashboardDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Tiles       []DashboardTile  `json:"tiles"`
	UserIDs     []int64          `json:"user_ids"`
	GroupIDs    []int64          `json:"group_ids"`
	Quality     DashboardQuality `json:"quality"`
}
type ManagedDashboard struct {
	ReviewStatus       string              `json:"review_status"`
	ReviewID           int64               `json:"review_id"`
	ReviewerID         int64               `json:"reviewer_id"`
	ReviewWasPublished bool                `json:"review_was_published"`
	ActiveReleaseID    *int64              `json:"active_release_id"`
	ID                 int64               `json:"id"`
	OwnerID            int64               `json:"owner_id"`
	OwnerName          string              `json:"owner_name"`
	Draft              DashboardDefinition `json:"draft"`
	DraftVersion       int64               `json:"draft_version"`
	LatestVersion      int64               `json:"latest_version"`
	PublicationNo      int64               `json:"publication_no"`
	Lifecycle          string              `json:"lifecycle"`
	Epoch              int64               `json:"lifecycle_epoch"`
	UpdatedAt          time.Time           `json:"updated_at"`
}
type DashboardReview struct {
	ID           int64               `json:"id"`
	VersionID    int64               `json:"version_id"`
	Number       int64               `json:"number"`
	Definition   DashboardDefinition `json:"definition"`
	SubmitterID  *int64              `json:"submitted_by"`
	Submitter    string              `json:"submitter_name"`
	ReviewerID   int64               `json:"reviewer_id"`
	Status       string              `json:"status"`
	Comment      string              `json:"comment"`
	ExpiresAt    time.Time           `json:"expires_at"`
	ValidatedAt  *time.Time          `json:"validated_at"`
	Epoch        int64               `json:"lifecycle_epoch"`
	WasPublished bool                `json:"was_published"`
}
type DashboardReport struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	PublicationNo  int64             `json:"publication_no"`
	Version        int64             `json:"version"`
	ChartType      string            `json:"chart_type"`
	ChartConfig    json.RawMessage   `json:"chart_config"`
	Contract       *ApprovedContract `json:"data_contract"`
	AccountID      int64             `json:"-"`
	AccountVersion int64             `json:"-"`
	Generation     int64             `json:"-"`
	UserIDs        []int64           `json:"-"`
	GroupIDs       []int64           `json:"-"`
}
type ManagedDashboardRepository struct {
	pool    *ScopedPool
	reports *ManagedReportRepository
}

func (r *ConnectionRepository) ManagedDashboards() *ManagedDashboardRepository {
	return &ManagedDashboardRepository{r.pool, r.ManagedReports()}
}
func dashboardInput(d DashboardDefinition, submit bool) error {
	if strings.TrimSpace(d.Name) == "" || len([]rune(d.Name)) > 128 || len([]rune(d.Description)) > 512 || len(d.Tiles) > 8 {
		return fmt.Errorf("%w: 看板名称、说明或卡片数量无效（最多8张）", ErrManagedInput)
	}
	seen := map[int64]bool{}
	keys := map[string]bool{}
	required := 0
	for _, t := range d.Tiles {
		if t.ReportID < 1 || seen[t.ReportID] || keys[t.Key] || len(t.Key) == 0 || len(t.Key) > 64 || strings.ContainsAny(t.Key, "/\x00") {
			return fmt.Errorf("%w: 卡片标识重复或同一报表重复引用", ErrManagedInput)
		}
		seen[t.ReportID] = true
		keys[t.Key] = true
		switch t.Width {
		case "full", "half", "wide", "narrow":
		default:
			return ErrManagedInput
		}
		if t.Required {
			required++
		}
		if t.BindingPolicy != "" && t.BindingPolicy != "follow_published" {
			return ErrManagedInput
		}
	}
	if submit && (len(d.Tiles) == 0 || required == 0) {
		return fmt.Errorf("%w: 送审需要1至8张卡片，且至少一张必需卡片", ErrManagedInput)
	}
	if d.Quality.MaxWatermarkSkew < 0 || d.Quality.MaxWatermarkSkew > 3600 || len([]rune(d.Quality.Reason)) > 1000 {
		return ErrManagedInput
	}
	if d.Quality.AllowUnknown && strings.TrimSpace(d.Quality.Reason) == "" {
		return fmt.Errorf("%w: 允许未知时效必须填写审阅理由", ErrManagedInput)
	}
	return nil
}
func loadDashboardReport(ctx context.Context, q reportQueryer, id, viewer int64) (*DashboardReport, error) {
	p := &DashboardReport{}
	var manifest []byte
	query := `SELECT m.id,v.definition->>'name',m.publication_no,v.number,v.definition->>'chart_type',v.definition->'chart_config',rv.manifest,a.id,a.version,a.credential_generation
 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id
 JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved' JOIN sys_report_versions v ON v.id=rv.version_id
 JOIN sys_report_accounts a ON a.id=rv.account_id AND a.enabled JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.enabled AND d.visibility='global'
 WHERE m.id=$1 AND (COALESCE(d.config->>'type','')<>'jdbc' OR EXISTS(SELECT 1 FROM sys_datasource_templates dt JOIN sys_jdbc_drivers jd ON jd.id=dt.driver_id AND jd.enabled WHERE dt.id=(d.config->>'template_revision_id')::bigint AND dt.enabled)) AND ` + reportAudience
	if _, ok := q.(pgx.Tx); ok {
		query += ` FOR SHARE OF m,a,d`
	}
	e := q.QueryRow(ctx, query, id, viewer).Scan(&p.ID, &p.Name, &p.PublicationNo, &p.Version, &p.ChartType, &p.ChartConfig, &manifest, &p.AccountID, &p.AccountVersion, &p.Generation)
	if e != nil {
		return nil, managedError(e)
	}
	var m ReportManifest
	if e = json.Unmarshal(manifest, &m); e != nil {
		return nil, e
	}
	p.Contract = m.Contract
	p.UserIDs = m.UserIDs
	p.GroupIDs = m.GroupIDs
	if p.Contract == nil {
		return nil, fmt.Errorf("%w: 此报表尚未获批数据契约，请先补充口径并发布", ErrManagedInput)
	}
	return p, nil
}
func (r *ManagedDashboardRepository) Catalog(ctx context.Context) ([]DashboardReport, error) {
	rows, e := r.pool.Query(ctx, `SELECT m.id FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id WHERE m.id>$1 AND rv.manifest->'data_contract' IS NOT NULL AND `+reportAudience, 0, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	out := []DashboardReport{}
	for _, id := range ids {
		p, e := loadDashboardReport(ctx, r.pool, id, ActorID(ctx))
		if e == nil {
			out = append(out, *p)
		}
	}
	return out, nil
}

const dashboardColumns = `d.id,d.owner_id,COALESCE(u.username,''),d.draft,d.draft_version,d.latest_version,d.publication_no,d.lifecycle,d.lifecycle_epoch,d.updated_at,d.active_release_id,
COALESCE((SELECT CASE WHEN rv.status='pending' AND rv.expires_at<=now() THEN 'expired' ELSE rv.status END FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE v.dashboard_id=d.id AND rv.lifecycle_epoch=d.lifecycle_epoch ORDER BY v.number DESC LIMIT 1),''),
COALESCE((SELECT rv.id FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE v.dashboard_id=d.id AND rv.lifecycle_epoch=d.lifecycle_epoch ORDER BY v.number DESC LIMIT 1),0),
COALESCE((SELECT rv.reviewer_id FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE v.dashboard_id=d.id AND rv.lifecycle_epoch=d.lifecycle_epoch ORDER BY v.number DESC LIMIT 1),0),
EXISTS(SELECT 1 FROM sys_dashboard_releases re JOIN sys_dashboard_reviews rv ON rv.id=re.review_id JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE v.dashboard_id=d.id AND v.number=d.latest_version)`

func scanManagedDashboard(row pgx.Row) (*ManagedDashboard, error) {
	d := &ManagedDashboard{}
	var b []byte
	e := row.Scan(&d.ID, &d.OwnerID, &d.OwnerName, &b, &d.DraftVersion, &d.LatestVersion, &d.PublicationNo, &d.Lifecycle, &d.Epoch, &d.UpdatedAt, &d.ActiveReleaseID, &d.ReviewStatus, &d.ReviewID, &d.ReviewerID, &d.ReviewWasPublished)
	if e != nil {
		return nil, managedError(e)
	}
	e = json.Unmarshal(b, &d.Draft)
	return d, e
}
func (r *ManagedDashboardRepository) Get(ctx context.Context, id int64) (*ManagedDashboard, error) {
	return scanManagedDashboard(r.pool.QueryRow(ctx, `SELECT `+dashboardColumns+` FROM sys_managed_dashboards d LEFT JOIN sys_users u ON u.id=d.owner_id WHERE d.id=$1 AND d.lifecycle<>'deleted' AND (d.owner_id=$2 OR $3)`, id, ActorID(ctx), ActorAdmin(ctx)))
}
func (r *ManagedDashboardRepository) List(ctx context.Context, scope, keyword string, limit, offset int) ([]ManagedDashboard, int64, error) {
	all := ActorAdmin(ctx) && scope != "mine"
	pending := scope == "pending"
	base := ` FROM sys_managed_dashboards d LEFT JOIN sys_users u ON u.id=d.owner_id WHERE d.lifecycle<>'deleted' AND (d.owner_id=$1 OR $2) AND ($3='' OR d.draft->>'name' ILIKE '%'||$3||'%' OR d.draft->>'description' ILIKE '%'||$3||'%') AND (NOT $4 OR EXISTS(SELECT 1 FROM sys_dashboard_reviews rv WHERE rv.dashboard_id=d.id AND rv.status='pending' AND rv.expires_at>now()))`
	args := []any{ActorID(ctx), all, keyword, pending}
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*)`+base, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT `+dashboardColumns+base+` ORDER BY d.updated_at DESC,d.id DESC LIMIT $5 OFFSET $6`, append(args, limit, offset)...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []ManagedDashboard{}
	for rows.Next() {
		d, e := scanManagedDashboard(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, *d)
	}
	return out, total, rows.Err()
}
func (r *ManagedDashboardRepository) Save(ctx context.Context, key string, id, expected int64, d DashboardDefinition) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-draft", []any{id, expected, d}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		d.UserIDs = normalizedIDs(d.UserIDs)
		d.GroupIDs = normalizedIDs(d.GroupIDs)
		if e := dashboardInput(d, false); e != nil {
			return out, e
		}
		old := DashboardDefinition{}
		if id > 0 {
			var owner, version int64
			var lifecycle string
			var raw []byte
			if e := tx.QueryRow(ctx, `SELECT owner_id,draft_version,lifecycle,draft FROM sys_managed_dashboards WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &version, &lifecycle, &raw); e != nil {
				return out, e
			}
			if owner != ActorID(ctx) {
				return out, ErrManagedDenied
			}
			if version != expected || lifecycle != "active" {
				return out, ErrManagedConflict
			}
			if e := json.Unmarshal(raw, &old); e != nil {
				return out, e
			}
		}
		for i := range d.Tiles {
			t := &d.Tiles[i]
			oldHash := ""
			for _, p := range old.Tiles {
				if p.ReportID == t.ReportID {
					oldHash = p.ContractHash
				}
			}
			if oldHash != "" {
				if t.ContractHash != "" && t.ContractHash != oldHash {
					return out, fmt.Errorf("%w: 口径变更请使用明确采纳新契约操作", ErrManagedInput)
				}
				t.ContractHash = oldHash
			} else {
				p, e := loadDashboardReport(ctx, tx, t.ReportID, ActorID(ctx))
				if e != nil {
					return out, e
				}
				t.ContractHash = p.Contract.Hash
			}
			t.BindingPolicy = "follow_published"
		}
		raw := canonicalReportJSON(d)
		var e error
		if id == 0 {
			e = tx.QueryRow(ctx, `INSERT INTO sys_managed_dashboards(owner_id,draft) VALUES($1,$2::jsonb) RETURNING id,draft_version`, ActorID(ctx), string(raw)).Scan(&out.ID, &out.Version)
		} else {
			e = tx.QueryRow(ctx, `UPDATE sys_managed_dashboards SET draft=$2::jsonb,draft_version=draft_version+1,updated_at=now() WHERE id=$1 RETURNING draft_version`, id, string(raw)).Scan(&out.Version)
		}
		return out, e
	})
}
func (r *ManagedDashboardRepository) Adopt(ctx context.Context, key string, id, expected, reportID, publication int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-adopt", []int64{id, expected, reportID, publication}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		var raw []byte
		var ver, owner int64
		var state string
		if e := tx.QueryRow(ctx, `SELECT draft,draft_version,owner_id,lifecycle FROM sys_managed_dashboards WHERE id=$1 FOR UPDATE`, id).Scan(&raw, &ver, &owner, &state); e != nil {
			return out, e
		}
		if owner != ActorID(ctx) {
			return out, ErrManagedDenied
		}
		if ver != expected || state != "active" {
			return out, ErrManagedConflict
		}
		var d DashboardDefinition
		if e := json.Unmarshal(raw, &d); e != nil {
			return out, e
		}
		p, e := loadDashboardReport(ctx, tx, reportID, ActorID(ctx))
		if e != nil {
			return out, e
		}
		if p.PublicationNo != publication {
			return out, ErrManagedConflict
		}
		found := false
		for i := range d.Tiles {
			if d.Tiles[i].ReportID == reportID {
				d.Tiles[i].ContractHash = p.Contract.Hash
				found = true
			}
		}
		if !found {
			return out, ErrNotFound
		}
		e = tx.QueryRow(ctx, `UPDATE sys_managed_dashboards SET draft=$2::jsonb,draft_version=draft_version+1,updated_at=now() WHERE id=$1 RETURNING draft_version`, id, string(canonicalReportJSON(d))).Scan(&out.Version)
		return out, e
	})
}
func dashboardGrant(ctx context.Context, q reportQueryer, users, groups []int64, viewer int64) bool {
	var ok bool
	e := q.QueryRow(ctx, `SELECT dbhub_member_role($1) IS NOT NULL AND ($1=ANY($2::bigint[]) OR EXISTS(SELECT 1 FROM sys_user_group_members WHERE user_id=$1 AND group_id=ANY($3::bigint[])))`, viewer, nonNilIDs(users), nonNilIDs(groups)).Scan(&ok)
	return e == nil && ok
}
func containsID(ids []int64, id int64) bool {
	for _, n := range ids {
		if n == id {
			return true
		}
	}
	return false
}

// Coverage is checked against explicit group grants, including future members.
func (r *ManagedDashboardRepository) checkDefinition(ctx context.Context, tx pgx.Tx, d DashboardDefinition, reader int64) ([]DashboardReport, error) {
	if e := dashboardInput(d, true); e != nil {
		return nil, e
	}
	if e := audience(ctx, tx, d.UserIDs, d.GroupIDs); e != nil {
		return nil, e
	}
	var recipients int
	if e := tx.QueryRow(ctx, `SELECT count(*) FROM sys_workspace_members WHERE workspace_id=dbhub_workspace() AND active AND dbhub_member_role(user_id) IS NOT NULL AND (user_id=ANY($1::bigint[]) OR user_id IN(SELECT user_id FROM sys_user_group_members WHERE group_id=ANY($2::bigint[])))`, nonNilIDs(d.UserIDs), nonNilIDs(d.GroupIDs)).Scan(&recipients); e != nil {
		return nil, e
	}
	if recipients == 0 {
		return nil, fmt.Errorf("%w: 至少需要一个有效接收人，空组不能作为唯一受众", ErrManagedInput)
	}
	if d.Quality.AllowUnknown {
		var allowed bool
		if e := tx.QueryRow(ctx, `SELECT allow_unknown_freshness FROM sys_workspaces WHERE id=dbhub_workspace()`).Scan(&allowed); e != nil {
			return nil, e
		}
		if !allowed {
			return nil, fmt.Errorf("%w: 本工作空间禁止未知时效看板", ErrManagedInput)
		}
	}
	refs := []DashboardReport{}
	comparisons := map[string]string{}
	for _, t := range d.Tiles {
		p, e := loadDashboardReport(ctx, tx, t.ReportID, reader)
		if e != nil {
			return nil, fmt.Errorf("%w: 审阅人须具有全部引用报表的普通查看权且报表须可用", ErrManagedInput)
		}
		if p.Contract.Hash != t.ContractHash {
			return nil, fmt.Errorf("%w: 引用报表口径已变化，请明确采纳并重新送审", ErrManagedConflict)
		}
		if p.Contract.Declaration.Freshness.Mode == "unverified" && !d.Quality.AllowUnknown {
			return nil, fmt.Errorf("%w: 报表时效未知，需要显式质量审批及空间策略许可", ErrManagedInput)
		}
		if t.Required {
			for _, uid := range d.UserIDs {
				if !dashboardGrant(ctx, tx, p.UserIDs, p.GroupIDs, uid) {
					return nil, fmt.Errorf("%w: 必需报表未覆盖全部个人受众", ErrManagedInput)
				}
			}
			for _, gid := range d.GroupIDs {
				if !containsID(p.GroupIDs, gid) {
					return nil, fmt.Errorf("%w: 必需报表必须明确授权给看板的用户组", ErrManagedInput)
				}
			}
		}
		if t.ComparisonGroup != "" {
			c := p.Contract.Declaration
			if c.ComparisonKey == "" {
				return nil, ErrManagedInput
			}
			hash := reportDigest([]any{c.ComparisonKey, c.ScopeKey, c.ScopeDescription, c.Unit, c.Currency, c.Timezone, c.TimeWindow})
			if prev, ok := comparisons[t.ComparisonGroup]; ok && prev != hash {
				return nil, fmt.Errorf("%w: 比较组的单位、范围、时区或时间窗口不一致", ErrManagedInput)
			}
			comparisons[t.ComparisonGroup] = hash
		}
		refs = append(refs, *p)
	}
	return refs, nil
}
func (r *ManagedDashboardRepository) Submit(ctx context.Context, key string, id, expected, reviewer int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "dashboard-submit", []int64{id, expected, reviewer}, false, true, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		var owner, version, next, epoch int64
		var raw []byte
		var lifecycle string
		if e := tx.QueryRow(ctx, `SELECT owner_id,draft_version,latest_version+1,lifecycle_epoch,draft,lifecycle FROM sys_managed_dashboards WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &version, &next, &epoch, &raw, &lifecycle); e != nil {
			return out, e
		}
		if owner != ActorID(ctx) {
			return out, ErrManagedDenied
		}
		if expected != version || lifecycle != "active" {
			return out, ErrManagedConflict
		}
		if _, e := tx.Exec(ctx, `UPDATE sys_dashboard_reviews SET status='expired' WHERE dashboard_id=$1 AND status='pending' AND expires_at<=now()`, id); e != nil {
			return out, e
		}
		var pending bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_dashboard_reviews WHERE dashboard_id=$1 AND status='pending')`, id).Scan(&pending); e != nil {
			return out, e
		}
		if pending {
			return out, ErrManagedConflict
		}
		var role *string
		if e := tx.QueryRow(ctx, `SELECT dbhub_member_role($1)`, reviewer).Scan(&role); e != nil {
			return out, e
		}
		if reviewer == owner || role == nil || *role != "admin" {
			return out, fmt.Errorf("%w: 请选择另一位具有全部报表读取权限的有效管理员", ErrManagedInput)
		}
		var d DashboardDefinition
		if e := json.Unmarshal(raw, &d); e != nil {
			return out, e
		}
		if _, e := r.checkDefinition(ctx, tx, d, ActorID(ctx)); e != nil {
			return out, e
		}
		if _, e := r.checkDefinition(ctx, tx, d, reviewer); e != nil {
			return out, e
		}
		var vid int64
		if e := tx.QueryRow(ctx, `INSERT INTO sys_dashboard_versions(dashboard_id,number,definition,content_hash,submitted_by,submitter_name) VALUES($1,$2,$3::jsonb,$4,$5,(SELECT username FROM sys_users WHERE id=$5)) RETURNING id`, id, next, string(raw), reportDigest(d), ActorID(ctx)).Scan(&vid); e != nil {
			return out, e
		}
		if e := tx.QueryRow(ctx, `INSERT INTO sys_dashboard_reviews(version_id,dashboard_id,lifecycle_epoch,reviewer_id) VALUES($1,$2,$3,$4) RETURNING id`, vid, id, epoch, reviewer).Scan(&out.ID); e != nil {
			return out, e
		}
		_, e := tx.Exec(ctx, `UPDATE sys_managed_dashboards SET latest_version=$2,updated_at=now() WHERE id=$1`, id, next)
		out.Version = next
		return out, e
	})
}
