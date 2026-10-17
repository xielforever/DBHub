package queryapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/sqlparse"
)

func (h *Handler) ListExports(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	items, e := h.controls.ListExports(r.Context(), c.Subject, false)
	if e != nil {
		httpx.Fail(w, httpx.Internal("读取导出申请失败", e))
		return
	}
	httpx.OK(w, map[string]any{"items": items})
}
func (h *Handler) CreateExport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectionID int64          `json:"connection_id"`
		Database     string         `json:"database"`
		SQL          string         `json:"sql"`
		Params       map[string]any `json:"params"`
		Format       string         `json:"format"`
		Reason       string         `json:"reason"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if (in.Format != "csv" && in.Format != "json") || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 {
		httpx.Fail(w, httpx.BadRequest("导出须填写原因，格式仅支持 CSV/JSON"))
		return
	}
	handle, conn, closeFn, e := h.openRelational(r, in.ConnectionID, in.Database)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	defer closeFn()
	c := claimsOf(r)
	ctx, finish, e := h.jobs.admit(r.Context(), c.Subject, conn.ID, in.SQL)
	if e != nil {
		httpx.Fail(w, httpx.New(42900, "导出队列繁忙", nil))
		return
	}
	var execErr error
	defer func() { finish(execErr) }()
	query, args, e := sqlparse.BindNamed(in.SQL, conn.SQLDialect(), in.Params)
	if e != nil {
		execErr = e
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	query, args, e = h.applyPolicies(ctx, handle, c.Subject, c.Role, query, args)
	if e != nil {
		execErr = e
		httpx.Fail(w, httpx.Forbidden(e.Error()))
		return
	}
	result, e := target.ExecuteWithOptions(ctx, handle, query, true, target.ExecuteOptions{MaxRows: 1000, Timeout: 30 * time.Second, Args: args, BeforeExecute: func() error { return h.conns.ValidateCurrent(ctx, conn) }})
	if e != nil {
		execErr = e
		httpx.Fail(w, httpx.BadRequest("生成导出快照失败: "+safeErr(e)))
		return
	}
	payload := map[string]any{"columns": result.Columns, "rows": result.Rows}
	id, e := h.controls.CreateExport(ctx, c.Subject, conn.ID, in.Format, in.Reason, len(result.Rows), payload)
	if e != nil {
		execErr = e
		httpx.Fail(w, httpx.Internal("保存导出申请失败", e))
		return
	}
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ExportID = id; d.Outcome = "export_pending" })
	httpx.OK(w, map[string]any{"id": id, "status": "pending"})
}
func (h *Handler) ReviewExport(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Status string `json:"status"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if in.Status != "approved" && in.Status != "rejected" {
		httpx.Fail(w, httpx.BadRequest("审批状态无效"))
		return
	}
	if e := h.controls.ReviewExport(r.Context(), id, claimsOf(r).Subject, in.Status); e != nil {
		httpx.Fail(w, httpx.Conflict("申请已处理或不存在"))
		return
	}
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ExportID = id; d.Outcome = "export_" + in.Status })
	httpx.OK(w, map[string]string{"status": in.Status})
}
func (h *Handler) DownloadExport(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c := claimsOf(r)
	format, raw, connectionID, e := h.controls.DownloadExport(r.Context(), id, c.Subject, false)
	if e != nil {
		httpx.Fail(w, httpx.NotFound("导出不存在、未批准或无权限"))
		return
	}
	auditctx.Update(r.Context(), func(d *auditctx.Data) {
		d.ExportID = id
		d.ConnectionID = connectionID
	})
	if err := auditctx.Checkpoint(r.Context()); err != nil {
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.Outcome = "rejected" })
		httpx.Fail(w, httpx.New(50300, "审计存储暂不可用", err))
		return
	}
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.Outcome = "download_response" })
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"format": format, "result": raw})
}
