package ticketapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/sqlparse"
)

type Handler struct {
	tickets *db.TicketRepository
	log     *slog.Logger
	conns   *db.ConnectionRepository
	tunnels *db.TunnelRepository
	history *db.HistoryRepository
	drivers *target.Runtime
}

func NewHandler(tickets *db.TicketRepository, log *slog.Logger, conns *db.ConnectionRepository, tunnels *db.TunnelRepository, history *db.HistoryRepository, runtimes ...*target.Runtime) *Handler {
	return &Handler{tickets: tickets, log: log, conns: conns, tunnels: tunnels, history: history, drivers: target.Default(runtimes)}
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
	if in.ConnectionID == nil || *in.ConnectionID < 1 {
		httpx.Fail(w, httpx.BadRequest("工单必须指定目标 connection_id"))
		return
	}
	conn, err := h.conns.Get(r.Context(), *in.ConnectionID)
	if err != nil || conn.Type == "redis" {
		httpx.Fail(w, httpx.BadRequest("目标数据源不存在或不支持 SQL"))
		return
	}
	if _, err = sqlparse.Parse(in.SQL, conn.SQLDialect()); err != nil {
		httpx.Fail(w, httpx.BadRequest(err.Error()))
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
	if f.Status != "" && f.Status != "pending" && f.Status != "approved" && f.Status != "rejected" && f.Status != "executed" && f.Status != "executing" && f.Status != "failed" {
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
	if in.Status != "approved" && in.Status != "rejected" {
		httpx.Fail(w, httpx.BadRequest("审批仅支持 approved/rejected；请通过执行接口实际执行工单"))
		return
	}
	if e := h.tickets.Review(r.Context(), id, in.Status, claimsOf(r).Subject); e != nil {
		httpx.Fail(w, httpx.Conflict("工单已处理或不存在"))
		return
	}
	httpx.OK(w, map[string]any{"id": id, "status": in.Status})
}

// Execute claims once before connecting; failed or uncertain executions never retry automatically.
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ticket, e := h.tickets.Get(r.Context(), id)
	if e != nil {
		httpx.Fail(w, httpx.NotFound("工单不存在"))
		return
	}
	if ticket.ConnectionID == nil {
		httpx.Fail(w, httpx.BadRequest("历史工单没有目标连接，请重新建单"))
		return
	}
	conn, password, e := h.conns.GetSecrets(r.Context(), *ticket.ConnectionID)
	if e != nil {
		httpx.Fail(w, httpx.BadRequest("工单目标连接不可用"))
		return
	}
	if ticket.DatabaseName != "" {
		conn.Database = ticket.DatabaseName
	}
	var tunnel *db.SSHTunnel
	if conn.SSHTunnelID != nil {
		tunnel, e = h.tunnels.GetSecrets(r.Context(), *conn.SSHTunnelID)
		if e != nil {
			httpx.Fail(w, httpx.BadRequest("工单隧道不可用"))
			return
		}
	}
	if e = h.tickets.ClaimExecution(r.Context(), id, claimsOf(r).Subject); e != nil {
		httpx.Fail(w, httpx.Conflict("仅可执行已批准且尚未执行的工单"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	start := time.Now()
	handle, execErr := h.drivers.OpenRelational(ctx, conn, password, tunnel)
	var result *target.ExecResult
	if execErr == nil {
		defer handle.Close()
		result, execErr = target.ExecuteWithOptions(ctx, handle, ticket.SQLText, false, target.ExecuteOptions{Timeout: 30 * time.Second, MaxRows: 100, AllowUnsafeWrite: true})
	}
	message := ""
	if execErr != nil {
		message = "目标数据库执行失败，请检查 SQL 与数据库状态"
	}
	cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	history := &db.QueryHistory{ConnectionID: conn.ID, UserID: claimsOf(r).Subject, DatabaseName: conn.Database, SQLText: ticket.SQLText, Status: 1}
	duration := int(time.Since(start).Milliseconds())
	history.ExecutionTimeMS = &duration
	if execErr != nil {
		history.Status = 0
		history.ErrorMessage = message
	} else {
		history.AffectedRows = &result.AffectedRows
	}
	if e = h.history.Create(cleanup, history); e != nil {
		h.log.Error("工单执行历史保存失败", "ticket_id", id)
	}
	if e = h.tickets.FinishExecution(cleanup, id, result, message); e != nil {
		httpx.Fail(w, httpx.Internal("执行结果保存失败，请核查目标数据库后人工处理，切勿重复执行", nil))
		return
	}
	if execErr != nil {
		httpx.Fail(w, httpx.BadRequest(message))
		return
	}
	httpx.OK(w, map[string]any{"id": id, "status": "executed", "result": result})
}
