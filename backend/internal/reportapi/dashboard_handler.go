package reportapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type DashboardHandler struct {
	dashboards *db.DashboardRepository
	reports    *db.ReportRepository
	conns      *db.ConnectionRepository
}

func NewDashboardHandler(dashboards *db.DashboardRepository, reports *db.ReportRepository, conns *db.ConnectionRepository) *DashboardHandler {
	return &DashboardHandler{dashboards: dashboards, reports: reports, conns: conns}
}

type dashboardRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Visibility  string          `json:"visibility"`
	Layout      json.RawMessage `json:"layout"`
}

func (h *DashboardHandler) Create(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in dashboardRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		httpx.Fail(w, httpx.BadRequest("仪表盘名称必填"))
		return
	}
	if in.Visibility == "" {
		in.Visibility = "private"
	}
	if in.Visibility != "private" && in.Visibility != "shared" {
		httpx.Fail(w, httpx.BadRequest("可见性非法"))
		return
	}
	if in.Layout == nil {
		in.Layout = json.RawMessage("[]")
	}
	d := &db.Dashboard{
		Name:        in.Name,
		Description: strings.TrimSpace(in.Description),
		Visibility:  in.Visibility,
		Layout:      in.Layout,
		OwnerUserID: c.Subject,
	}
	if err := h.dashboards.Create(r.Context(), d); err != nil {
		httpx.Fail(w, httpx.Internal("创建仪表盘失败", err))
		return
	}
	httpx.OK(w, d)
}

func (h *DashboardHandler) List(w http.ResponseWriter, r *http.Request) {
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
	scope := q.Get("scope")
	f := db.DashboardFilter{
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
	}
	items, total, err := h.dashboards.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询仪表盘失败", err))
		return
	}
	if scope == "" || scope == "all" {
		filtered := make([]db.Dashboard, 0, len(items))
		for _, it := range items {
			if it.Visibility == "private" && it.OwnerUserID != c.Subject && c.Role != "admin" {
				continue
			}
			filtered = append(filtered, it)
		}
		items = filtered
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *DashboardHandler) Get(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	d, err := h.dashboards.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "仪表盘不存在"})
			return
		}
		httpx.Fail(w, httpx.Internal("查询仪表盘失败", err))
		return
	}
	if d.Visibility == "private" && d.OwnerUserID != c.Subject && c.Role != "admin" {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeForbidden, Message: "无权查看该私有仪表盘"})
		return
	}
	for _, uid := range d.StarredBy {
		if uid == c.Subject {
			d.Starred = true
			break
		}
	}
	httpx.OK(w, d)
}

func (h *DashboardHandler) Update(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	existing, err := h.dashboards.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询仪表盘失败", err))
		return
	}
	if existing.OwnerUserID != c.Subject && c.Role != "admin" {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeForbidden, Message: "仅所有者可编辑"})
		return
	}
	var in dashboardRequest
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
	if in.Visibility != "" {
		if in.Visibility != "private" && in.Visibility != "shared" {
			httpx.Fail(w, httpx.BadRequest("可见性非法"))
			return
		}
		existing.Visibility = in.Visibility
	}
	if in.Layout != nil {
		existing.Layout = in.Layout
	}
	if err := h.dashboards.Update(r.Context(), existing); err != nil {
		httpx.Fail(w, httpx.Internal("更新仪表盘失败", err))
		return
	}
	httpx.OK(w, existing)
}

func (h *DashboardHandler) Delete(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	existing, err := h.dashboards.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询仪表盘失败", err))
		return
	}
	if existing.OwnerUserID != c.Subject && c.Role != "admin" {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeForbidden, Message: "仅所有者可删除"})
		return
	}
	if err := h.dashboards.Delete(r.Context(), id); err != nil {
		httpx.Fail(w, httpx.Internal("删除仪表盘失败", err))
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

func (h *DashboardHandler) Star(w http.ResponseWriter, r *http.Request) {
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
	if err := h.dashboards.SetStar(r.Context(), id, c.Subject, in.Star); err != nil {
		httpx.Fail(w, httpx.Internal("收藏失败", err))
		return
	}
	httpx.OK(w, map[string]any{"starred": in.Star})
}
