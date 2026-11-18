package reportapi

import (
	"fmt"
	"net/http"

	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/reportexport"
)

func (h *ManagedHandler) Exports(w http.ResponseWriter, r *http.Request) {
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	items, total, err := h.repo.Exports(r.Context(), size, (page-1)*size)
	if err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *ManagedHandler) Export(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, err := h.repo.Export(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, out)
}
func (h *ManagedHandler) ReviewExport(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version  int64  `json:"version"`
		Decision string `json:"decision"`
		Comment  string `json:"comment"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Version < 1 || in.Decision != "approved" && in.Decision != "rejected" || len([]rune(in.Comment)) > 500 {
		httpx.Fail(w, httpx.BadRequest("导出审批版本、决定或意见无效"))
		return
	}
	if err := h.repo.ReviewExport(r.Context(), id, in.Version, in.Decision, in.Comment); err != nil {
		ManagedError(w, err)
		return
	}
	out, err := h.repo.Export(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	httpx.OK(w, out)
}
func (h *ManagedHandler) DownloadExport(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, snapshot, release, err := h.repo.DownloadExport(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	defer release()
	raw, contentType, err := reportexport.Encode(snapshot, out.Format, out.SHA256)
	if err != nil {
		httpx.Fail(w, httpx.BadRequest(err.Error()))
		return
	}
	if err = h.repo.CheckExportDelivery(r.Context(), out, false); err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Report-Snapshot-SHA256", out.SHA256)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="report-%d-snapshot-%d.%s"`, out.ReportID, out.ID, out.Format))
	w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (h *ManagedHandler) PreviewExport(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, snapshot, release, err := h.repo.PreviewExport(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	defer release()
	result := snapshot.Result
	if len(result.Rows) > 20 {
		result.Rows = result.Rows[:20]
		result.Truncated = true
	}
	if err = h.repo.CheckExportDelivery(r.Context(), out, true); err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"export": out, "result": result, "params": snapshot.Params, "labels": snapshot.Labels, "scope": snapshot.Scope})
}
