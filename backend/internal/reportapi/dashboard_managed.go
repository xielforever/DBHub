package reportapi

import (
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"net/http"
	"strings"
)

type ManagedDashboardHandler struct {
	repo *db.ManagedDashboardRepository
}

func NewManagedDashboardHandler(repo *db.ManagedDashboardRepository) *ManagedDashboardHandler {
	return &ManagedDashboardHandler{repo}
}
func (h *ManagedDashboardHandler) Preflight(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		Version int64 `json:"version"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if in.Version < 1 {
		httpx.Fail(w, httpx.BadRequest("草稿版本无效"))
		return
	}
	out, e := h.repo.Preflight(r.Context(), id, in.Version)
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, out)
}
func (h *ManagedDashboardHandler) ReviewSummary(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, e := h.repo.ReviewSummary(r.Context(), id)
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, out)
}
func (h *ManagedDashboardHandler) List(w http.ResponseWriter, r *http.Request) {
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope != "" && scope != "mine" && scope != "all" && scope != "pending" {
		httpx.Fail(w, httpx.BadRequest("目录范围无效"))
		return
	}
	items, total, e := h.repo.List(r.Context(), scope, strings.TrimSpace(r.URL.Query().Get("q")), size, (page-1)*size)
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *ManagedDashboardHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	d, e := h.repo.Get(r.Context(), id)
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, d)
}
func (h *ManagedDashboardHandler) Save(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID  string                 `json:"request_id"`
		Version    int64                  `json:"version"`
		Definition db.DashboardDefinition `json:"definition"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) {
		httpx.Fail(w, httpx.BadRequest("request_id 必须为UUID"))
		return
	}
	id := int64(0)
	if r.PathValue("id") != "" {
		var ok bool
		id, ok = reportID(w, r)
		if !ok {
			return
		}
	}
	result, e := h.repo.Save(r.Context(), in.RequestID, id, in.Version, in.Definition)
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.DashboardID = result.ID; d.DashboardDraftVersion = result.Version })
	commandResponse(w, result, e)
}
func (h *ManagedDashboardHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	items, e := h.repo.Catalog(r.Context())
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items})
}
func (h *ManagedDashboardHandler) Published(w http.ResponseWriter, r *http.Request) {
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	items, total, e := h.repo.Published(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")), size, (page-1)*size)
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *ManagedDashboardHandler) PublishedGet(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, e := h.repo.PublishedGet(r.Context(), id)
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *ManagedDashboardHandler) Reviews(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, e := h.repo.Reviews(r.Context(), id)
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": out})
}
func (h *ManagedDashboardHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		Mode     string `json:"mode"`
		ReviewID int64  `json:"review_id"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if in.Mode == "" {
		in.Mode = "published"
	}
	out, e := h.repo.Refresh(r.Context(), id, in.Mode, in.ReviewID)
	if e != nil {
		ManagedError(w, e)
		return
	}
	auditctx.Update(r.Context(), func(d *auditctx.Data) {
		d.DashboardID = id
		d.DashboardPublicationNo = out.PublicationNo
		d.DashboardMode = in.Mode
		d.DashboardReviewID = in.ReviewID
		if in.Mode == "draft" {
			d.DashboardDraftVersion = out.Version
		} else {
			d.DashboardVersion = out.Version
		}
		d.RefreshID = out.ID
	})
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, out)
}
func (h *ManagedDashboardHandler) Command(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := reportID(w, r)
		if !ok {
			return
		}
		var in struct {
			RequestID     string `json:"request_id"`
			Version       int64  `json:"version"`
			ReviewerID    int64  `json:"reviewer_id"`
			OwnerID       int64  `json:"owner_id"`
			ReportID      int64  `json:"report_id"`
			PublicationNo int64  `json:"publication_no"`
			ExpectedNo    int64  `json:"expected_publication_no"`
			ReviewID      int64  `json:"review_id"`
			Kind          string `json:"kind"`
			Decision      string `json:"decision"`
			Comment       string `json:"comment"`
			RefreshID     string `json:"refresh_id"`
		}
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		if !requestKey(in.RequestID) || len([]rune(in.Comment)) > 1000 {
			httpx.Fail(w, httpx.BadRequest("请求编号或说明无效"))
			return
		}
		var out db.ReportCommandResult
		var e error
		switch action {
		case "submit":
			out, e = h.repo.Submit(r.Context(), in.RequestID, id, in.Version, in.ReviewerID)
		case "adopt":
			out, e = h.repo.Adopt(r.Context(), in.RequestID, id, in.Version, in.ReportID, in.PublicationNo)
		case "release":
			out, e = h.repo.Release(r.Context(), in.RequestID, id, in.ExpectedNo, in.ReviewID, in.Kind)
		case "decision":
			out, e = h.repo.Decision(r.Context(), in.RequestID, id, in.Decision, strings.TrimSpace(in.Comment))
		case "validate":
			if !requestKey(in.RefreshID) {
				httpx.Fail(w, httpx.BadRequest("刷新编号无效"))
				return
			}
			out, e = h.repo.ValidateReview(r.Context(), in.RequestID, id, in.RefreshID)
		case "reassign":
			out, e = h.repo.Reassign(r.Context(), in.RequestID, id, in.ReviewerID)
		case "transfer":
			out, e = h.repo.Transfer(r.Context(), in.RequestID, id, in.Version, in.OwnerID)
		default:
			out, e = h.repo.Lifecycle(r.Context(), in.RequestID, id, in.Version, action)
		}
		auditctx.Update(r.Context(), func(d *auditctx.Data) {
			d.DashboardID = id
			switch action {
			case "validate", "decision", "reassign":
				d.DashboardID = 0
				d.DashboardReviewID = id
			case "submit":
				d.DashboardReviewID = out.ID
				d.DashboardVersion = out.Version
				d.DashboardDraftVersion = in.Version
			case "release":
				d.DashboardReviewID = in.ReviewID
				d.DashboardPublicationNo = out.Version
			default:
				d.DashboardDraftVersion = out.Version
			}
		})
		if action == "validate" || action == "decision" || action == "reassign" {
			if parent, version, err := h.repo.ReviewIdentity(r.Context(), id); err == nil {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.DashboardID = parent; d.DashboardVersion = version })
			}
		}
		commandResponse(w, out, e)
	}
}

func (h *ManagedDashboardHandler) AudienceChange(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string  `json:"request_id"`
		Base      int64   `json:"base_publication_no"`
		Reviewer  int64   `json:"reviewer_id"`
		UserIDs   []int64 `json:"user_ids"`
		GroupIDs  []int64 `json:"group_ids"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) {
		httpx.Fail(w, httpx.BadRequest("请求编号无效"))
		return
	}
	out, e := h.repo.AudienceChange(r.Context(), in.RequestID, id, in.Base, in.Reviewer, in.UserIDs, in.GroupIDs)
	auditctx.Update(r.Context(), func(d *auditctx.Data) {
		d.DashboardID = id
		d.DashboardVersion = out.Version
		d.DashboardReviewID = out.ID
	})
	commandResponse(w, out, e)
}

func (h *ManagedDashboardHandler) CurrentAudience(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, e := h.repo.CurrentAudience(r.Context(), id)
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, out)
}
