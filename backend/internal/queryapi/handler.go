// Package queryapi 提供 SQL 工作台相关接口：SQL 执行、对象元数据浏览、
// 表数据预览、Redis 浏览与查询历史。
package queryapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/target"
)

// Handler 查询工作台处理器。
type Handler struct {
	conns   *db.ConnectionRepository
	tunnels *db.TunnelRepository
	history *db.HistoryRepository
	log     *slog.Logger
}

func NewHandler(conns *db.ConnectionRepository, tunnels *db.TunnelRepository, history *db.HistoryRepository, log *slog.Logger) *Handler {
	return &Handler{conns: conns, tunnels: tunnels, history: history, log: log}
}

type executeRequest struct {
	ConnectionID int64  `json:"connection_id"`
	Database     string `json:"database"`
	SQL          string `json:"sql"`
}

func claimsOf(r *http.Request) *auth.Claims {
	c, _ := auth.ClaimsFromContext(r.Context())
	return c
}

func isAdmin(c *auth.Claims) bool { return c.Role == "admin" }

// loadOwnedConnection 取连接+解密口令+隧道，并做归属校验。
func (h *Handler) loadOwnedConnection(r *http.Request, connID int64) (*db.Connection, string, *db.SSHTunnel, error) {
	c := claimsOf(r)
	conn, password, err := h.conns.GetSecrets(r.Context(), connID)
	if err != nil {
		return nil, "", nil, mapNotFound(err)
	}
	if !isAdmin(c) && conn.UserID != c.Subject {
		return nil, "", nil, &httpx.AppError{Code: httpx.CodeNotFound, Message: "连接不存在"}
	}
	var tunnel *db.SSHTunnel
	if conn.SSHTunnelID != nil {
		t, err := h.tunnels.GetSecrets(r.Context(), *conn.SSHTunnelID)
		if err != nil {
			return nil, "", nil, mapNotFound(err)
		}
		if !isAdmin(c) && t.UserID != c.Subject {
			return nil, "", nil, httpx.BadRequest("所选 SSH 隧道不可用")
		}
		tunnel = t
	}
	return conn, password, tunnel, nil
}

func (h *Handler) openRelational(r *http.Request, connID int64, database string) (*target.RelDB, *db.Connection, func(), error) {
	conn, password, tunnel, err := h.loadOwnedConnection(r, connID)
	if err != nil {
		return nil, nil, nil, err
	}
	if database != "" {
		conn.Database = database
	}
	if conn.Type == "redis" {
		return nil, nil, nil, httpx.BadRequest("该接口仅支持 MySQL / PostgreSQL，请使用 Redis 浏览接口")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	handle, err := target.OpenRelational(ctx, conn, password, tunnel)
	if err != nil {
		return nil, nil, nil, httpx.BadRequest("打开目标数据库失败: " + safeErr(err))
	}
	return handle, conn, handle.Close, nil
}

// Execute POST /api/v1/query/execute
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in executeRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.SQL = strings.TrimSpace(in.SQL)
	if in.ConnectionID <= 0 {
		httpx.Fail(w, httpx.BadRequest("connection_id 非法"))
		return
	}
	if in.SQL == "" {
		httpx.Fail(w, httpx.BadRequest("SQL 内容不能为空"))
		return
	}

	conn, password, tunnel, err := h.loadOwnedConnection(r, in.ConnectionID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if conn.Type == "redis" {
		httpx.Fail(w, httpx.BadRequest("Redis 不支持 SQL，请使用键浏览功能"))
		return
	}
	if in.Database != "" {
		conn.Database = in.Database
	}

	openCtx, openCancel := context.WithTimeout(r.Context(), 20*time.Second)
	handle, err := target.OpenRelational(openCtx, conn, password, tunnel)
	openCancel()
	if err != nil {
		h.recordHistory(r.Context(), c, in, conn, 0, nil, err)
		httpx.Fail(w, httpx.BadRequest("打开目标数据库失败: "+safeErr(err)))
		return
	}
	defer handle.Close()

	forceReadOnly := c.Role == "readonly"
	result, execErr := target.Execute(r.Context(), handle, in.SQL, forceReadOnly)
	if execErr != nil {
		h.recordHistory(r.Context(), c, in, conn, 0, nil, execErr)
		httpx.Fail(w, httpx.BadRequest(safeErr(execErr)))
		return
	}
	var affected *int64
	if result.Kind == "write" {
		n := result.AffectedRows
		affected = &n
	}
	h.recordHistory(r.Context(), c, in, conn, result.DurationMS, affected, nil)
	httpx.OK(w, result)
}

func (h *Handler) recordHistory(ctx context.Context, c *auth.Claims, in executeRequest, conn *db.Connection, durationMS int64, affected *int64, execErr error) {
	dbName := in.Database
	if dbName == "" {
		dbName = conn.Database
	}
	rec := &db.QueryHistory{
		ConnectionID: in.ConnectionID,
		UserID:       c.Subject,
		DatabaseName: dbName,
		SQLText:      firstN(in.SQL, 10000),
	}
	rec.ExecutionTimeMS = intPtr(int(durationMS))
	if execErr != nil {
		rec.Status = 0
		rec.ErrorMessage = firstN(execErr.Error(), 1000)
	} else {
		rec.Status = 1
		rec.AffectedRows = affected
	}
	if err := h.history.Create(ctx, rec); err != nil {
		h.log.Warn("写入查询历史失败", "error", err)
	}
}

// Databases GET /api/v1/metadata/databases
func (h *Handler) Databases(w http.ResponseWriter, r *http.Request) {
	connID, _ := strconv.ParseInt(r.URL.Query().Get("connection_id"), 10, 64)
	if connID <= 0 {
		httpx.Fail(w, httpx.BadRequest("connection_id 非法"))
		return
	}
	if h.isRedis(r, connID) {
		httpx.Fail(w, httpx.BadRequest("Redis 没有数据库列表概念，请使用键空间浏览"))
		return
	}
	handle, _, closeFn, err := h.openRelational(r, connID, "")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeFn()
	list, err := target.Databases(r.Context(), handle)
	if err != nil {
		httpx.Fail(w, httpx.Internal("获取数据库列表失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": list})
}

// Schemas GET /api/v1/metadata/schemas
func (h *Handler) Schemas(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	handle, _, closeFn, err := h.openRelational(r, connID, q.Get("database"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeFn()
	list, err := target.Schemas(r.Context(), handle)
	if err != nil {
		httpx.Fail(w, httpx.Internal("获取模式列表失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": list})
}

// Tables GET /api/v1/metadata/tables
func (h *Handler) Tables(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	handle, _, closeFn, err := h.openRelational(r, connID, q.Get("database"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeFn()
	list, err := target.Tables(r.Context(), handle, q.Get("database"), q.Get("schema"))
	if err != nil {
		httpx.Fail(w, httpx.Internal("获取表列表失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": list})
}

// Columns GET /api/v1/metadata/columns
func (h *Handler) Columns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	table := q.Get("table")
	if table == "" {
		httpx.Fail(w, httpx.BadRequest("table 不能为空"))
		return
	}
	handle, _, closeFn, err := h.openRelational(r, connID, q.Get("database"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeFn()
	cols, err := target.Columns(r.Context(), handle, q.Get("database"), q.Get("schema"), table)
	if err != nil {
		httpx.Fail(w, httpx.Internal("获取列结构失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": cols})
}

// Preview GET /api/v1/data/preview
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	table := q.Get("table")
	if table == "" {
		httpx.Fail(w, httpx.BadRequest("table 不能为空"))
		return
	}
	page := atoiDefault(q.Get("page"), 1)
	pageSize := atoiDefault(q.Get("page_size"), 50)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	handle, _, closeFn, err := h.openRelational(r, connID, q.Get("database"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeFn()
	columns, rows, total, err := target.PreviewTable(r.Context(), handle,
		q.Get("database"), q.Get("schema"), table, pageSize+1, (page-1)*pageSize)
	if err != nil {
		httpx.Fail(w, httpx.BadRequest(safeErr(err)))
		return
	}
	hasMore := false
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		hasMore = true
	}
	httpx.OK(w, map[string]any{
		"columns":   columns,
		"rows":      rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"has_more":  hasMore,
	})
}

// RedisOverview GET /api/v1/redis/overview
func (h *Handler) RedisOverview(w http.ResponseWriter, r *http.Request) {
	connID, _ := strconv.ParseInt(r.URL.Query().Get("connection_id"), 10, 64)
	handle, closeFn, err := h.openRedis(r, connID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeFn()
	info, err := target.RedisOverviewInfo(r.Context(), handle.Client)
	if err != nil {
		httpx.Fail(w, httpx.BadRequest(safeErr(err)))
		return
	}
	httpx.OK(w, info)
}

// RedisKeys GET /api/v1/redis/keys
func (h *Handler) RedisKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	max := int64(atoiDefault(q.Get("limit"), 200))
	handle, closeFn, err := h.openRedis(r, connID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeFn()
	keys, err := target.ScanKeys(r.Context(), handle.Client, q.Get("pattern"), max)
	if err != nil {
		httpx.Fail(w, httpx.BadRequest(safeErr(err)))
		return
	}
	httpx.OK(w, map[string]any{"items": keys, "returned": len(keys)})
}

// RedisValue GET /api/v1/redis/value
func (h *Handler) RedisValue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	key := q.Get("key")
	if key == "" {
		httpx.Fail(w, httpx.BadRequest("key 不能为空"))
		return
	}
	handle, closeFn, err := h.openRedis(r, connID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeFn()
	val, err := target.InspectKey(r.Context(), handle.Client, key)
	if err != nil {
		httpx.Fail(w, httpx.BadRequest(safeErr(err)))
		return
	}
	httpx.OK(w, val)
}

func (h *Handler) openRedis(r *http.Request, connID int64) (*target.RedisClient, func(), error) {
	conn, password, tunnel, err := h.loadOwnedConnection(r, connID)
	if err != nil {
		return nil, nil, err
	}
	if conn.Type != "redis" {
		return nil, nil, httpx.BadRequest("该数据源不是 Redis")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	handle, err := target.OpenRedis(ctx, conn, password, tunnel)
	if err != nil {
		return nil, nil, httpx.BadRequest("打开 Redis 失败: " + safeErr(err))
	}
	return handle, handle.Close, nil
}

// isRedis 轻量探测连接类型（元数据入口分流用）。
func (h *Handler) isRedis(r *http.Request, connID int64) bool {
	conn, err := h.conns.Get(r.Context(), connID)
	if err != nil {
		return false
	}
	return conn.Type == "redis"
}

// History GET /api/v1/query/history
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	pageSize := atoiDefault(q.Get("page_size"), 20)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	items, total, err := h.history.List(r.Context(), c.Subject, isAdmin(c), connID,
		q.Get("status"), pageSize, (page-1)*pageSize)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询历史失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// DeleteHistory DELETE /api/v1/query/history/{id}
func (h *Handler) DeleteHistory(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	if err := h.history.Delete(r.Context(), id, c.Subject, isAdmin(c)); err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

// ClearHistory DELETE /api/v1/query/history
func (h *Handler) ClearHistory(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	if err := h.history.Clear(r.Context(), c.Subject, isAdmin(c)); err != nil {
		httpx.Fail(w, httpx.Internal("清空历史失败", err))
		return
	}
	httpx.OK(w, map[string]any{"ok": true})
}

func atoiDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func intPtr(n int) *int { return &n }

func mapNotFound(err error) error {
	if errors.Is(err, db.ErrNotFound) {
		return &httpx.AppError{Code: httpx.CodeNotFound, Message: "记录不存在"}
	}
	return httpx.Internal("数据访问失败", err)
}

func safeErr(err error) string {
	msg := strings.SplitN(err.Error(), "\n", 2)[0]
	const max = 200
	if len(msg) > max {
		return msg[:max]
	}
	return msg
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
