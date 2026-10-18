package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type DashboardSubject struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	role   string
	active bool
}
type DashboardIssue struct {
	Code       string             `json:"code"`
	Severity   string             `json:"severity"`
	Message    string             `json:"message"`
	TileKey    string             `json:"tile_key,omitempty"`
	ReportName string             `json:"report_name,omitempty"`
	Users      []DashboardSubject `json:"users,omitempty"`
	Groups     []DashboardSubject `json:"groups,omitempty"`
	Action     string             `json:"action,omitempty"`
}
type DashboardPreflight struct {
	ID        int64              `json:"dashboard_id"`
	Version   int64              `json:"draft_version"`
	CheckedAt time.Time          `json:"checked_at"`
	CanSubmit bool               `json:"can_submit"`
	Issues    []DashboardIssue   `json:"issues"`
	Reviewers []DashboardSubject `json:"eligible_reviewers"`
}

type dashboardSubjects struct {
	users   map[int64]DashboardSubject
	groups  map[int64]DashboardSubject
	members map[int64]map[int64]bool
	admins  []DashboardSubject
}

// A fixed number of directory reads serves audience checks and every candidate; avoid
// an audience-size × card-count sequence of SQL queries.
func dashboardSubjectDirectory(ctx context.Context, q pgx.Tx) (*dashboardSubjects, error) {
	d := &dashboardSubjects{users: map[int64]DashboardSubject{}, groups: map[int64]DashboardSubject{}, members: map[int64]map[int64]bool{}}
	rows, err := q.Query(ctx, `SELECT u.id,u.username,m.role_code,(m.active AND u.is_active) FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id WHERE m.workspace_id=dbhub_workspace() ORDER BY u.username,u.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s DashboardSubject
		if err = rows.Scan(&s.ID, &s.Name, &s.role, &s.active); err != nil {
			rows.Close()
			return nil, err
		}
		d.users[s.ID] = s
		if s.active && s.role == "admin" {
			d.admins = append(d.admins, s)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `SELECT id,name FROM sys_user_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s DashboardSubject
		if err = rows.Scan(&s.ID, &s.Name); err != nil {
			rows.Close()
			return nil, err
		}
		d.groups[s.ID] = s
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `SELECT group_id,user_id FROM sys_user_group_members ORDER BY group_id,user_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var group, user int64
		if err = rows.Scan(&group, &user); err != nil {
			rows.Close()
			return nil, err
		}
		if d.members[group] == nil {
			d.members[group] = map[int64]bool{}
		}
		if d.users[user].active {
			d.members[group][user] = true
		}
	}
	rows.Close()
	return d, rows.Err()
}
func (d *dashboardSubjects) user(id int64) DashboardSubject {
	if s, ok := d.users[id]; ok {
		return s
	}
	return DashboardSubject{ID: id, Name: fmt.Sprintf("已离开的成员 #%d", id)}
}
func (d *dashboardSubjects) group(id int64) DashboardSubject {
	if s, ok := d.groups[id]; ok {
		return s
	}
	return DashboardSubject{ID: id, Name: fmt.Sprintf("已移除的用户组 #%d", id)}
}
func (d *dashboardSubjects) granted(user int64, p *DashboardReport) bool {
	if !d.users[user].active {
		return false
	}
	if containsID(p.UserIDs, user) {
		return true
	}
	for _, group := range p.GroupIDs {
		if d.members[group][user] {
			return true
		}
	}
	return false
}
func dashboardBlocked(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrManagedDenied) || errors.Is(err, ErrManagedInput) || errors.Is(err, ErrCatalogDisabled)
}

func (r *ManagedDashboardRepository) Preflight(ctx context.Context, id, expected int64) (*DashboardPreflight, error) {
	tx, err := r.pool.BeginSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var owner, version int64
	var raw []byte
	var lifecycle string
	err = tx.QueryRow(ctx, `SELECT owner_id,draft_version,draft,lifecycle FROM sys_managed_dashboards WHERE id=$1 AND lifecycle<>'deleted' AND (owner_id=$2 OR $3)`, id, ActorID(ctx), ActorAdmin(ctx)).Scan(&owner, &version, &raw, &lifecycle)
	if err != nil {
		return nil, managedError(err)
	}
	if version != expected {
		return nil, ErrManagedConflict
	}
	var def DashboardDefinition
	if err = json.Unmarshal(raw, &def); err != nil {
		return nil, err
	}
	directory, err := dashboardSubjectDirectory(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &DashboardPreflight{ID: id, Version: version, CheckedAt: time.Now().UTC(), Issues: []DashboardIssue{}, Reviewers: []DashboardSubject{}}
	add := func(code, message, action string) {
		out.Issues = append(out.Issues, DashboardIssue{Code: code, Severity: "error", Message: message, Action: action})
	}
	if lifecycle != "active" {
		add("archived", "请先恢复草稿，再检查送审条件", "restore")
	}
	if owner != ActorID(ctx) {
		add("owner_only", "请由当前责任人提交审阅", "contact_owner")
	}
	if err = dashboardInput(def, true); err != nil {
		add("invalid_definition", "送审需要1至8张卡片、至少一张必需卡片，并完整填写质量规则", "edit")
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_dashboard_reviews WHERE dashboard_id=$1 AND status='pending' AND expires_at>now())`, id).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		add("pending_review", "已有待审版本，请先处理或撤回", "review")
	}
	if !validateSubjects(def.UserIDs) || !validateSubjects(def.GroupIDs) {
		add("invalid_audience", "请检查重复或无效的受众", "edit")
	}
	for _, uid := range def.UserIDs {
		if !directory.users[uid].active {
			out.Issues = append(out.Issues, DashboardIssue{Code: "inactive_user", Severity: "error", Message: "个人受众已离开或停用，请调整受众", Users: []DashboardSubject{directory.user(uid)}, Action: "edit"})
		}
	}
	recipients := map[int64]bool{}
	for _, uid := range def.UserIDs {
		if directory.users[uid].active {
			recipients[uid] = true
		}
	}
	for _, gid := range def.GroupIDs {
		if _, ok := directory.groups[gid]; !ok {
			out.Issues = append(out.Issues, DashboardIssue{Code: "missing_group", Severity: "error", Message: "受众用户组已移除", Groups: []DashboardSubject{directory.group(gid)}, Action: "edit"})
		}
		for uid := range directory.members[gid] {
			recipients[uid] = true
		}
	}
	if len(recipients) == 0 {
		add("empty_audience", "至少选择一位有效接收人；空用户组不能作为唯一受众", "edit")
	}
	if def.Quality.AllowUnknown {
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT allow_unknown_freshness FROM sys_workspaces WHERE id=dbhub_workspace()`).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			add("freshness_policy", "当前工作空间禁止发布允许未知时效的看板", "edit")
		}
	}
	refs := []*DashboardReport{}
	comparisons := map[string]string{}
	for _, tile := range def.Tiles {
		p, e := loadDashboardReport(ctx, tx, tile.ReportID, ActorID(ctx))
		if e != nil {
			if !dashboardBlocked(e) {
				return nil, e
			}
			out.Issues = append(out.Issues, DashboardIssue{Code: "report_unavailable", Severity: "error", TileKey: tile.Key, Message: "卡片报表不可访问、未发布或缺少已批准口径，请先处理报表状态和本人权限", Action: "edit"})
			continue
		}
		refs = append(refs, p)
		if p.Contract.Hash != tile.ContractHash {
			out.Issues = append(out.Issues, DashboardIssue{Code: "contract_changed", Severity: "error", TileKey: tile.Key, ReportName: p.Name, Message: "正式报表口径已变化，请核对并明确采纳后重新检查", Action: "adopt_contract"})
		}
		if p.Contract.Declaration.Freshness.Mode == "unverified" && !def.Quality.AllowUnknown {
			out.Issues = append(out.Issues, DashboardIssue{Code: "unknown_freshness", Severity: "error", TileKey: tile.Key, ReportName: p.Name, Message: "报表未提供业务水位，需要显式质量规则及工作空间许可", Action: "edit"})
		}
		issue := DashboardIssue{Code: "audience_coverage", Severity: "warning", TileKey: tile.Key, ReportName: p.Name, Message: "可选卡片未覆盖部分受众，发布后将对这些受众显示为受限", Action: "request_access"}
		if tile.Required {
			issue.Severity = "error"
			issue.Message = "必需报表未覆盖以下受众，请先完成补权并正式发布"
		}
		for _, uid := range def.UserIDs {
			if directory.users[uid].active && !directory.granted(uid, p) {
				issue.Users = append(issue.Users, directory.user(uid))
			}
		}
		for _, gid := range def.GroupIDs {
			if _, ok := directory.groups[gid]; ok && !containsID(p.GroupIDs, gid) {
				issue.Groups = append(issue.Groups, directory.group(gid))
			}
		}
		if len(issue.Users)+len(issue.Groups) > 0 {
			out.Issues = append(out.Issues, issue)
		}
		if tile.ComparisonGroup != "" {
			c := p.Contract.Declaration
			hash := reportDigest([]any{c.ComparisonKey, c.ScopeKey, c.ScopeDescription, c.Unit, c.Currency, c.Timezone, c.TimeWindow})
			if c.ComparisonKey == "" || (comparisons[tile.ComparisonGroup] != "" && comparisons[tile.ComparisonGroup] != hash) {
				out.Issues = append(out.Issues, DashboardIssue{Code: "comparison_mismatch", Severity: "error", TileKey: tile.Key, ReportName: p.Name, Message: "比较组的单位、范围、时区或时间窗口不一致，请调整比较组", Action: "edit"})
			}
			comparisons[tile.ComparisonGroup] = hash
		}
	}
	if len(refs) == len(def.Tiles) && len(refs) > 0 {
		for _, candidate := range directory.admins {
			if candidate.ID == owner {
				continue
			}
			eligible := true
			for _, p := range refs {
				if !directory.granted(candidate.ID, p) {
					eligible = false
					break
				}
			}
			if eligible {
				out.Reviewers = append(out.Reviewers, candidate)
			}
		}
	}
	if len(out.Reviewers) == 0 {
		add("no_reviewer", "暂无同时拥有全部引用报表查看权的独立管理员，请先配置人员及报表授权", "configure_reviewer")
	}
	out.CanSubmit = true
	for _, issue := range out.Issues {
		if issue.Severity == "error" {
			out.CanSubmit = false
			break
		}
	}
	return out, tx.Commit(ctx)
}
