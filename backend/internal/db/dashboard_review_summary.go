package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

type DashboardChange struct {
	Area    string `json:"area"`
	Subject string `json:"subject"`
	Before  string `json:"before"`
	After   string `json:"after"`
}
type DashboardSummaryCard struct {
	Key             string        `json:"key"`
	Name            string        `json:"name"`
	Required        bool          `json:"required"`
	Width           string        `json:"width"`
	ComparisonGroup string        `json:"comparison_group"`
	ContractStatus  string        `json:"contract_status"`
	Contract        *DataContract `json:"contract,omitempty"`
}
type DashboardReviewSummary struct {
	ID                  int64                  `json:"review_id"`
	Version             int64                  `json:"version"`
	ReviewerName        string                 `json:"reviewer_name"`
	BaselineKind        string                 `json:"baseline_kind"`
	BaselineVersion     int64                  `json:"baseline_version"`
	BaselinePublication int64                  `json:"baseline_publication_no"`
	Changes             []DashboardChange      `json:"changes"`
	Cards               []DashboardSummaryCard `json:"cards"`
	Users               []DashboardSubject     `json:"users"`
	Groups              []DashboardSubject     `json:"groups"`
}

// Summary compares immutable business definitions. It never runs a report or
// returns SQL, account identities or the contents of an inaccessible report.
func (r *ManagedDashboardRepository) ReviewSummary(ctx context.Context, review int64) (*DashboardReviewSummary, error) {
	tx, err := r.pool.BeginSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	out := &DashboardReviewSummary{ID: review, BaselineKind: "initial", Changes: []DashboardChange{}, Cards: []DashboardSummaryCard{}, Users: []DashboardSubject{}, Groups: []DashboardSubject{}}
	var id, reviewer, publication int64
	var release *int64
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT d.id,v.number,v.definition,rv.reviewer_id,d.active_release_id,d.publication_no FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id WHERE rv.id=$1 AND d.lifecycle<>'deleted' AND (d.owner_id=$2 OR $3)`, review, ActorID(ctx), ActorAdmin(ctx)).Scan(&id, &out.Version, &raw, &reviewer, &release, &publication)
	if err != nil {
		return nil, managedError(err)
	}
	var target, before DashboardDefinition
	if err = json.Unmarshal(raw, &target); err != nil {
		return nil, err
	}
	var base []byte
	if release != nil {
		err = tx.QueryRow(ctx, `SELECT v.definition,v.number FROM sys_dashboard_releases re JOIN sys_dashboard_reviews rv ON rv.id=re.review_id JOIN sys_dashboard_versions v ON v.id=rv.version_id WHERE re.id=$1 AND re.dashboard_id=$2`, *release, id).Scan(&base, &out.BaselineVersion)
		if err != nil {
			return nil, err
		}
		out.BaselineKind = "current_release"
		out.BaselinePublication = publication
	} else {
		err = tx.QueryRow(ctx, `SELECT definition,number FROM sys_dashboard_versions WHERE dashboard_id=$1 AND number<$2 ORDER BY number DESC LIMIT 1`, id, out.Version).Scan(&base, &out.BaselineVersion)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			out.BaselineKind = "previous_version"
		}
	}
	if len(base) > 0 {
		if err = json.Unmarshal(base, &before); err != nil {
			return nil, err
		}
	}
	directory, err := dashboardSubjectDirectory(ctx, tx)
	if err != nil {
		return nil, err
	}
	out.ReviewerName = directory.user(reviewer).Name
	names := map[int64]string{}
	loaded := map[int64]*DashboardReport{}
	for _, tile := range append(append([]DashboardTile{}, target.Tiles...), before.Tiles...) {
		if _, ok := names[tile.ReportID]; ok {
			continue
		}
		p, e := loadDashboardReport(ctx, tx, tile.ReportID, ActorID(ctx))
		if e != nil {
			if !dashboardBlocked(e) {
				return nil, e
			}
			names[tile.ReportID] = "受限报表"
			continue
		}
		names[tile.ReportID] = p.Name
		loaded[tile.ReportID] = p
	}
	for _, tile := range target.Tiles {
		card := DashboardSummaryCard{Key: tile.Key, Name: names[tile.ReportID], Required: tile.Required, Width: tile.Width, ComparisonGroup: tile.ComparisonGroup, ContractStatus: "restricted"}
		if p := loaded[tile.ReportID]; p != nil {
			card.Contract = &p.Contract.Declaration
			card.ContractStatus = "compatible"
			if p.Contract.Hash != tile.ContractHash {
				card.ContractStatus = "changed"
			}
		}
		out.Cards = append(out.Cards, card)
	}
	add := func(area, subject, a, b string) {
		if a != b {
			out.Changes = append(out.Changes, DashboardChange{area, subject, a, b})
		}
	}
	add("基本信息", "名称", before.Name, target.Name)
	add("基本信息", "说明", before.Description, target.Description)
	old := map[string]DashboardTile{}
	positions := map[string]int{}
	for i, tile := range before.Tiles {
		old[tile.Key] = tile
		positions[tile.Key] = i
	}
	width := map[string]string{"full": "整行", "half": "半行", "wide": "三分之二", "narrow": "三分之一"}
	required := func(value bool) string {
		if value {
			return "必需"
		}
		return "可选"
	}
	for i, tile := range target.Tiles {
		name := names[tile.ReportID]
		previous, ok := old[tile.Key]
		if !ok {
			add("卡片", "新增卡片", "", name)
			continue
		}
		if previous.ReportID != tile.ReportID {
			add("卡片", "引用报表", names[previous.ReportID], name)
		}
		add("布局", name+" · 顺序", strconv.Itoa(positions[tile.Key]+1), strconv.Itoa(i+1))
		add("布局", name+" · 宽度", width[previous.Width], width[tile.Width])
		add("完整性", name, required(previous.Required), required(tile.Required))
		add("比较组", name, previous.ComparisonGroup, tile.ComparisonGroup)
		if previous.ContractHash != tile.ContractHash {
			add("业务口径", name, "原批准口径", "重新采纳的报表口径")
		}
		delete(old, tile.Key)
	}
	for _, tile := range before.Tiles {
		if _, ok := old[tile.Key]; ok {
			add("卡片", "移除卡片", names[tile.ReportID], "")
		}
	}
	for _, uid := range target.UserIDs {
		out.Users = append(out.Users, directory.user(uid))
		if !containsID(before.UserIDs, uid) {
			add("受众", "新增个人受众", "", directory.user(uid).Name)
		}
	}
	for _, uid := range before.UserIDs {
		if !containsID(target.UserIDs, uid) {
			add("受众", "移除个人受众", directory.user(uid).Name, "")
		}
	}
	for _, gid := range target.GroupIDs {
		out.Groups = append(out.Groups, directory.group(gid))
		if !containsID(before.GroupIDs, gid) {
			add("受众", "新增用户组", "", directory.group(gid).Name)
		}
	}
	for _, gid := range before.GroupIDs {
		if !containsID(target.GroupIDs, gid) {
			add("受众", "移除用户组", directory.group(gid).Name, "")
		}
	}
	freshness := func(allow bool) string {
		if allow {
			return "允许未知时效，须显示未知状态"
		}
		return "必须提供业务数据水位"
	}
	add("时效规则", "未知时效策略", freshness(before.Quality.AllowUnknown), freshness(target.Quality.AllowUnknown))
	add("时效规则", "业务理由", before.Quality.Reason, target.Quality.Reason)
	add("时效规则", "比较组最大水位差", fmt.Sprintf("%d 秒", before.Quality.MaxWatermarkSkew), fmt.Sprintf("%d 秒", target.Quality.MaxWatermarkSkew))
	if out.BaselineKind == "initial" {
		for i := range out.Changes {
			out.Changes[i].Before = ""
		}
	}
	return out, tx.Commit(ctx)
}
