package ticketapi

import (
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
	tickets *db.TicketRepository
	log     *slog.Logger
}

func NewHandler(tickets *db.TicketRepository, log *slog.Logger) *Handler {
	return &Handler{tickets: tickets, log: log}
}

func claimsOf(r *http.Request) *auth.Claims {
	c, _ := auth.ClaimsFromContext(r.Context())
	return c
}

type createRequest struct {
	Title        string `json:"title"`
	Type         string `json:"type"`
	Table        string `json:"table"`
	Database     string `json:"database"`
	ConnectionID *int64 `json:"connection_id"`
	SQL          string `json:"sql"`
	Risk         string `json:"risk"`
	Reason       string `json:"reason"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	if c == nil {
		httpx.Fail(w, httpx.Unauthorized("未认证"))
		return
	}
	var in createRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Table = strings.TrimSpace(in.Table)
	in.SQL = strings.TrimSpace(in.SQL)
	in.Reason = strings.TrimSpace(in.Reason)
	in.Database = strings.TrimSpace(in.Database)
	if in.Title == "" {
		httpx.Fail(w, httpx.BadRequest("标题必填"))
		return
	}
	if len([]rune(in.Title)) > 128 {
		httpx.Fail(w, httpx.BadRequest("标题不能超过128字"))
		return
	}
	if in.Table == "" {
		httpx.Fail(w, httpx.BadRequest("目标表必填"))
		return
	}
	if in.SQL == "" {
		httpx.Fail(w, httpx.BadRequest("SQL 不能为空"))
		return
	}
	if in.Type == "" {
		in.Type = "ddl"
	}
	if in.Type != "ddl" && in.Type != "dml" && in.Type != "index" {
		httpx.Fail(w, httpx.BadRequest("类型非法，仅支持 ddl/dml/index"))
		return
	}
	if in.Risk == "" {
		in.Risk = "low"
	}
	if in.Risk != "low" && in.Risk != "medium" && in.Risk != "high" {
		httpx.Fail(w, httpx.BadRequest("风险等级非法"))
		return
	}
	t := &db.Ticket{
		Title:        in.Title,
		Type:         in.Type,
		TableName:    in.Table,
		DatabaseName: in.Database,
		ConnectionID: in.ConnectionID,
		SQLText:      in.SQL,
		Risk:         in.Risk,
		Status:       "pending",
		RequesterID:  c.Subject,
		Reason:       in.Reason,
	}
	if err := h.tickets.Create(r.Context(), t); err != nil {
		h.log.Error("创建工单失败", "err", err)
		httpx.Fail(w, httpx.Internal("创建工单失败", err))
		return
	}
	// 补充用户名
	t.Requester = c.Username
	httpx.OK(w, t)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	f := db.TicketFilter{
		Status:  strings.TrimSpace(q.Get("status")),
		Keyword: strings.TrimSpace(q.Get("q")),
		Type:    strings.TrimSpace(q.Get("type")),
		Offset:  (page - 1) * pageSize,
		Limit:   pageSize,
	}
	// 校验 status
	if f.Status != "" && f.Status != "pending" && f.Status != "approved" && f.Status != "rejected" && f.Status != "executed" {
		httpx.Fail(w, httpx.BadRequest("状态非法"))
		return
	}
	if f.Type != "" && f.Type != "ddl" && f.Type != "dml" && f.Type != "index" {
		httpx.Fail(w, httpx.BadRequest("类型非法"))
		return
	}
	items, total, err := h.tickets.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询工单失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	t, err := h.tickets.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "工单不存在"})
			return
		}
		httpx.Fail(w, httpx.Internal("查询工单失败", err))
		return
	}
	httpx.OK(w, t)
}

type statusRequest struct {
	Status string `json:"status"`
}

func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	var in statusRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.Status = strings.TrimSpace(in.Status)
	if in.Status != "approved" && in.Status != "rejected" && in.Status != "executed" && in.Status != "pending" {
		httpx.Fail(w, httpx.BadRequest("状态非法，仅支持 pending/approved/rejected/executed"))
		return
	}
	// 校验存在
	existing, err := h.tickets.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "工单不存在"})
			return
		}
		httpx.Fail(w, httpx.Internal("查询工单失败", err))
		return
	}
	// 简单状态机：pending -> approved/rejected, approved -> executed
	// 允许 admin 强制，但给提示
	if existing.Status == "pending" && in.Status != "approved" && in.Status != "rejected" {
		httpx.Fail(w, httpx.BadRequest("待审批工单仅可批准或拒绝"))
		return
	}
	if existing.Status == "approved" && in.Status != "executed" {
		httpx.Fail(w, httpx.BadRequest("已批准工单仅可标记已执行"))
		return
	}
	if existing.Status == "rejected" || existing.Status == "executed" {
		httpx.Fail(w, httpx.BadRequest("已终态工单不可再变更"))
		return
	}
	// pending -> 同状态不允许？ already handled
	if err := h.tickets.UpdateStatus(r.Context(), id, in.Status); err != nil {
		httpx.Fail(w, httpx.Internal("更新状态失败", err))
		return
	}
	httpx.OK(w, map[string]any{"id": id, "status": in.Status})
}
