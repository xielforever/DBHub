package reportapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"log/slog"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type Handler struct {
	reports *db.ReportRepository
	conns   *db.ConnectionRepository
	log     *slog.Logger
}

func NewHandler(reports *db.ReportRepository, conns *db.ConnectionRepository, log *slog.Logger) *Handler {
	return &Handler{reports: reports, conns: conns, log: log}
}

func claimsOf(r *http.Request) *auth.Claims {
	c, _ := auth.ClaimsFromContext(r.Context())
	return c
}

type createRequest struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ConnectionID int64           `json:"connection_id"`
	Database     string          `json:"database"`
	SQL          string          `json:"sql"`
	ChartType    string          `json:"chart_type"`
	ChartConfig  json.RawMessage `json:"chart_config"`
	Visibility   string          `json:"visibility"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in createRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.SQL = strings.TrimSpace(in.SQL)
	if in.Name == "" {
		httpx.Fail(w, httpx.BadRequest("报表名称必填"))
		return
	}
	if len([]rune(in.Name)) > 128 {
		httpx.Fail(w, httpx.BadRequest("报表名称不能超过128字"))
		return
	}
	if in.ConnectionID <= 0 {
		httpx.Fail(w, httpx.BadRequest("connection_id 必填"))
		return
	}
	if in.SQL == "" {
		httpx.Fail(w, httpx.BadRequest("SQL 不能为空"))
		return
	}
	// 单语句校验（与工作台一致）
	if strings.Count(in.SQL, ";") > 1 || (strings.Contains(in.SQL, ";") && !strings.HasSuffix(strings.TrimSpace(in.SQL), ";")) {
		// 简易校验，允许末尾分号
		trimmed := strings.TrimSpace(in.SQL)
		if strings.Count(trimmed, ";") > 1 {
			httpx.Fail(w, httpx.BadRequest("一次只允许一条 SQL"))
			return
		}
	}
	if in.ChartType == "" {
		in.ChartType = "table"
	}
	if in.ChartType != "table" && in.ChartType != "bar" && in.ChartType != "line" && in.ChartType != "pie" && in.ChartType != "metric" {
		httpx.Fail(w, httpx.BadRequest("图表类型非法"))
		return
	}
	if in.Visibility == "" {
		in.Visibility = "private"
	}
	if in.Visibility != "private" && in.Visibility != "shared" {
		httpx.Fail(w, httpx.BadRequest("可见性非法"))
		return
	}
	if in.ChartConfig == nil {
		in.ChartConfig = json.RawMessage("{}")
	}
	// 校验连接存在
	if _, err := h.conns.Get(r.Context(), in.ConnectionID); err != nil {
		httpx.Fail(w, httpx.BadRequest("指定的数据源不存在"))
		return
	}
	rep := &db.Report{
		Name:         in.Name,
		Description:  strings.TrimSpace(in.Description),
		ConnectionID: in.ConnectionID,
		DatabaseName: strings.TrimSpace(in.Database),
		SQLText:      in.SQL,
		ChartType:    in.ChartType,
		ChartConfig:  in.ChartConfig,
		Visibility:   in.Visibility,
		OwnerUserID:  c.Subject,
	}
	if err := h.reports.Create(r.Context(), rep); err != nil {
		httpx.Fail(w, httpx.Internal("创建报表失败", err))
		return
	}
	httpx.OK(w, rep)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	scope := q.Get("scope") // mine | starred | shared | all
	f := db.ReportFilter{
		Keyword: strings.TrimSpace(q.Get("q")),
		Offset:  (page - 1) * pageSize,
		Limit:   pageSize,
	}
	switch scope {
	case "mine":
		f.OwnerID = c.Subject
	case "starred":
		f.StarredBy = c.Subject
	case "shared":
		f.Visibility = "shared"
	default:
		// all: 不过滤
	}
	if connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64); connID > 0 {
		f.ConnectionID = connID
	}
	items, total, err := h.reports.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询报表失败", err))
		return
	}
	// 注入当前用户收藏标记（List 已处理 starred）
	// 非 admin 隐藏他人 private？按 PRD：非 owner 不可改他人私有报表，但列表可见性：我的/共享/收藏分 scope
	// 这里 scope=all 时，private 仅本人可见（除 admin）
	if scope == "" || scope == "all" {
		filtered := make([]db.Report, 0, len(items))
		for _, it := range items {
			if it.Visibility == "private" && it.OwnerUserID != c.Subject && c.Role != "admin" {
				continue
			}
			filtered = append(filtered, it)
		}
		// total 需重新计算？简化：保留原 total，前端以 items 为准
		items = filtered
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	rep, err := h.reports.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "报表不存在"})
			return
		}
		httpx.Fail(w, httpx.Internal("查询报表失败", err))
		return
	}
	c := claimsOf(r)
	// 私有报表仅 owner/admin 可见
	if rep.Visibility == "private" && rep.OwnerUserID != c.Subject && c.Role != "admin" {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeForbidden, Message: "无权查看该私有报表"})
		return
	}
	// 注入 starred
	for _, uid := range rep.StarredBy {
		if uid == c.Subject {
			rep.Starred = true
			break
		}
	}
	httpx.OK(w, rep)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	existing, err := h.reports.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询报表失败", err))
		return
	}
	if existing.OwnerUserID != c.Subject && c.Role != "admin" {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeForbidden, Message: "仅所有者可编辑"})
		return
	}
	var in createRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Name != "" {
		existing.Name = strings.TrimSpace(in.Name)
	}
	if in.Description != "" || r.Method == "PUT" {
		existing.Description = strings.TrimSpace(in.Description)
	}
	if in.ConnectionID > 0 {
		existing.ConnectionID = in.ConnectionID
	}
	if in.Database != "" {
		existing.DatabaseName = strings.TrimSpace(in.Database)
	}
	if in.SQL != "" {
		existing.SQLText = strings.TrimSpace(in.SQL)
	}
	if in.ChartType != "" {
		if in.ChartType != "table" && in.ChartType != "bar" && in.ChartType != "line" && in.ChartType != "pie" && in.ChartType != "metric" {
			httpx.Fail(w, httpx.BadRequest("图表类型非法"))
			return
		}
		existing.ChartType = in.ChartType
	}
	if in.ChartConfig != nil {
		existing.ChartConfig = in.ChartConfig
	}
	if in.Visibility != "" {
		if in.Visibility != "private" && in.Visibility != "shared" {
			httpx.Fail(w, httpx.BadRequest("可见性非法"))
			return
		}
		existing.Visibility = in.Visibility
	}
	if err := h.reports.Update(r.Context(), existing); err != nil {
		httpx.Fail(w, httpx.Internal("更新报表失败", err))
		return
	}
	httpx.OK(w, existing)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	existing, err := h.reports.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询报表失败", err))
		return
	}
	if existing.OwnerUserID != c.Subject && c.Role != "admin" {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeForbidden, Message: "仅所有者可删除"})
		return
	}
	if err := h.reports.Delete(r.Context(), id); err != nil {
		httpx.Fail(w, httpx.Internal("删除报表失败", err))
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

type starRequest struct {
	Star bool `json:"star"`
}

func (h *Handler) Star(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	var in starRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.reports.SetStar(r.Context(), id, c.Subject, in.Star); err != nil {
		httpx.Fail(w, httpx.Internal("收藏失败", err))
		return
	}
	httpx.OK(w, map[string]any{"starred": in.Star})
}
