package queryapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/sqlparse"
	"github.com/user/dbhub/internal/target"
)

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	s, e := h.controls.Settings(r.Context(), claimsOf(r).Subject)
	if e != nil {
		httpx.Fail(w, httpx.Internal("读取资源设置失败", e))
		return
	}
	httpx.OK(w, s)
}
func (h *Handler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	var s db.QuerySettings
	if e := httpx.DecodeJSON(w, r, &s); e != nil {
		httpx.Fail(w, e)
		return
	}
	if s.TimeoutMS < 1000 || s.TimeoutMS > 120000 || s.MaxRows < 1 || s.MaxRows > 10000 {
		httpx.Fail(w, httpx.BadRequest("超时须为 1000–120000ms，行数须为 1–10000"))
		return
	}
	if !isAdmin(claimsOf(r)) && !s.BlockUnsafeWrite {
		httpx.Fail(w, httpx.Forbidden("只有管理员可关闭无 WHERE 写入保护"))
		return
	}
	if e := h.controls.SaveSettings(r.Context(), claimsOf(r).Subject, s); e != nil {
		httpx.Fail(w, httpx.Internal("保存资源设置失败", e))
		return
	}
	httpx.OK(w, s)
}
func (h *Handler) Policies(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("connection_id"), 10, 64)
	role := claimsOf(r).Role
	if isAdmin(claimsOf(r)) {
		role = ""
	}
	list, e := h.controls.Policies(r.Context(), id, role)
	if e != nil {
		httpx.Fail(w, httpx.Internal("读取策略失败", e))
		return
	}
	httpx.OK(w, map[string]any{"items": list})
}
func (h *Handler) SavePolicy(w http.ResponseWriter, r *http.Request) {
	var p db.QueryPolicy
	if e := httpx.DecodeJSON(w, r, &p); e != nil {
		httpx.Fail(w, e)
		return
	}
	conn, _, _, e := h.loadOwnedConnection(r, p.ConnectionID)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	if p.Database == "" {
		p.Database = conn.Database
	}
	if conn.Type == "mysql" {
		p.Schema = ""
	}
	if p.Schema == "" && conn.Type == "postgres" {
		p.Schema = "public"
	}
	if p.Table == "" || (p.Role != "developer" && p.Role != "readonly") || conn.Type == "redis" || len(p.Columns) > 500 || len(p.Conditions) > 20 {
		httpx.Fail(w, httpx.BadRequest("策略表名、角色或条件无效"))
		return
	}
	for _, permission := range p.Columns {
		if permission != "visible" && permission != "masked" && permission != "hidden" {
			httpx.Fail(w, httpx.BadRequest("列权限无效"))
			return
		}
	}
	for _, c := range p.Conditions {
		switch strings.ToUpper(c.Operator) {
		case "=", "!=", ">", ">=", "<", "<=", "IS NULL", "IS NOT NULL":
		default:
			httpx.Fail(w, httpx.BadRequest("行条件操作符无效"))
			return
		}
		switch c.Value.(type) {
		case nil, string, float64, bool:
		default:
			httpx.Fail(w, httpx.BadRequest("行条件必须为标量值"))
			return
		}
	}
	handle, _, closeFn, openErr := h.openRelational(r, p.ConnectionID, p.Database)
	if openErr != nil {
		httpx.Fail(w, openErr)
		return
	}
	defer closeFn()
	columns, columnErr := target.Columns(r.Context(), handle, p.Database, p.Schema, p.Table)
	if columnErr != nil || len(columns) == 0 {
		httpx.Fail(w, httpx.BadRequest("策略表不存在或无法读取"))
		return
	}
	known := map[string]bool{}
	for _, col := range columns {
		known[col.Name] = true
	}
	for col := range p.Columns {
		if !known[col] {
			httpx.Fail(w, httpx.BadRequest("策略引用不存在的列"))
			return
		}
	}
	for _, condition := range p.Conditions {
		if !known[condition.Column] {
			httpx.Fail(w, httpx.BadRequest("策略条件引用不存在的列"))
			return
		}
	}
	if p.Columns == nil {
		p.Columns = map[string]string{}
	}
	if p.Conditions == nil {
		p.Conditions = []db.RowCondition{}
	}
	if e = h.controls.SavePolicy(r.Context(), &p, claimsOf(r).Subject); e != nil {
		httpx.Fail(w, httpx.Internal("保存策略失败", e))
		return
	}
	httpx.OK(w, p)
}
func (h *Handler) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e := h.controls.DeletePolicy(r.Context(), id); e != nil {
		httpx.Fail(w, httpx.NotFound("策略不存在"))
		return
	}
	httpx.OK(w, map[string]int64{"id": id})
}
func (h *Handler) ListShares(w http.ResponseWriter, r *http.Request) {
	s, e := h.controls.ListShares(r.Context(), claimsOf(r).Subject)
	if e != nil {
		httpx.Fail(w, httpx.Internal("读取分享失败", e))
		return
	}
	httpx.OK(w, map[string]any{"items": s})
}
func (h *Handler) RevokeShare(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e := h.controls.RevokeShare(r.Context(), claimsOf(r).Subject, id); e != nil {
		httpx.Fail(w, httpx.NotFound("分享不存在"))
		return
	}
	httpx.OK(w, map[string]bool{"revoked": true})
}
func (h *Handler) PublicShare(w http.ResponseWriter, r *http.Request) {
	raw, e := h.controls.ReadShare(r.Context(), r.PathValue("token"))
	if e != nil {
		httpx.Fail(w, httpx.NotFound("分享不存在、已撤销或已过期"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	httpx.OK(w, raw)
}
func (h *Handler) CreateShare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string         `json:"name"`
		ConnectionID  int64          `json:"connection_id"`
		Database      string         `json:"database"`
		SQL           string         `json:"sql"`
		Params        map[string]any `json:"params"`
		ExpireDays    int            `json:"expire_days"`
		IncludeResult bool           `json:"include_result"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || in.ExpireDays < 1 || in.ExpireDays > 30 || len(in.SQL) > 10000 || in.SQL == "" {
		httpx.Fail(w, httpx.BadRequest("分享名称、SQL 或期限无效（1–30 天）"))
		return
	}
	handle, conn, closeFn, e := h.openRelational(r, in.ConnectionID, in.Database)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	defer closeFn()
	query, args, e := sqlparse.BindNamed(in.SQL, conn.Type, in.Params)
	if e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	c := claimsOf(r)
	query, args, e = h.applyPolicies(r.Context(), handle, c.Subject, c.Role, query, args)
	if e != nil {
		httpx.Fail(w, httpx.Forbidden(e.Error()))
		return
	}
	payload := map[string]any{"name": in.Name, "sql": in.SQL, "params": in.Params, "database": conn.Database}
	if in.IncludeResult {
		res, e := target.ExecuteWithOptions(r.Context(), handle, query, true, target.ExecuteOptions{MaxRows: 100, Timeout: 15 * time.Second, Args: args})
		if e != nil {
			httpx.Fail(w, httpx.BadRequest("生成快照失败: "+safeErr(e)))
			return
		}
		payload["columns"] = res.Columns
		payload["rows"] = res.Rows
	}
	token := randomID() + randomID()
	share, e := h.controls.CreateShare(r.Context(), c.Subject, token, in.Name, payload, time.Now().Add(time.Duration(in.ExpireDays)*24*time.Hour))
	if e != nil {
		httpx.Fail(w, httpx.Internal("创建分享失败", e))
		return
	}
	httpx.OK(w, map[string]any{"share": share, "token": token})
}
