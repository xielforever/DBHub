package reportapi

import (
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"net/http"
)

func (h *ManagedHandler) AccessRequests(w http.ResponseWriter, r *http.Request) {
	items, e := h.repo.AccessRequests(r.Context())
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items})
}
func (h *ManagedHandler) AccessAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := reportID(w, r)
		if !ok {
			return
		}
		var in struct {
			RequestID string `json:"request_id"`
			Version   int64  `json:"version"`
			Assignee  int64  `json:"assigned_to"`
			Reason    string `json:"reason"`
		}
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		if !requestKey(in.RequestID) {
			httpx.Fail(w, httpx.BadRequest("请求编号无效"))
			return
		}
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AccessRequestID = id; d.AccessDecision = action })
		out, e := h.repo.AccessAction(r.Context(), in.RequestID, id, in.Version, in.Assignee, action, in.Reason)
		commandResponse(w, out, e)
	}
}
func (h *ManagedHandler) AudienceChange(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
		db.AudienceChange
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) {
		httpx.Fail(w, httpx.BadRequest("请求编号无效"))
		return
	}
	bindManaged(r.Context(), id, 0)
	out, e := h.repo.AudienceChange(r.Context(), in.RequestID, id, in.AudienceChange)
	auditctx.Update(r.Context(), func(d *auditctx.Data) {
		d.ReportReviewID = out.ID
		d.AccessRequestIDs = in.RequestIDs
		d.AccessTargetUserIDs = in.UserIDs
		d.AccessTargetGroupIDs = in.GroupIDs
	})
	commandResponse(w, out, e)
}
func (h *ManagedDashboardHandler) RequestAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
		Tile      string `json:"tile_key"`
		Reason    string `json:"reason"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) {
		httpx.Fail(w, httpx.BadRequest("请求编号无效"))
		return
	}
	out, e := h.repo.RequestAccess(r.Context(), in.RequestID, id, in.Tile, in.Reason)
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.DashboardID = id; d.TileID = in.Tile; d.AccessRequestID = out.ID })
	commandResponse(w, out, e)
}

func (h *ManagedHandler) CurrentAudience(w http.ResponseWriter, r *http.Request) {
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

func (h *ManagedDashboardHandler) RequestAudienceAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string  `json:"request_id"`
		Version   int64   `json:"version"`
		Tile      string  `json:"tile_key"`
		Reason    string  `json:"reason"`
		Users     []int64 `json:"user_ids"`
		Groups    []int64 `json:"group_ids"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) {
		httpx.Fail(w, httpx.BadRequest("请求编号无效"))
		return
	}
	out, e := h.repo.RequestAudienceAccess(r.Context(), in.RequestID, id, in.Version, in.Tile, in.Reason, in.Users, in.Groups)
	auditctx.Update(r.Context(), func(d *auditctx.Data) {
		d.DashboardID = id
		d.TileID = in.Tile
		d.AccessRequestIDs = out.IDs
		d.AccessTargetUserIDs = in.Users
		d.AccessTargetGroupIDs = in.Groups
	})
	commandResponse(w, out, e)
}
