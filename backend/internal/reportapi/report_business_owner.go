package reportapi

import (
	"net/http"

	"github.com/user/dbhub/internal/httpx"
)

func (h *ManagedHandler) BusinessOwnerCandidates(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	items, err := h.repo.BusinessOwnerCandidates(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"items": items})
}

func (h *ManagedHandler) TransferBusinessOwner(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID       string `json:"request_id"`
		ExpectedVersion *int64 `json:"expected_version"`
		BusinessOwnerID int64  `json:"business_owner_id"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(in.RequestID) || in.ExpectedVersion == nil || *in.ExpectedVersion < 0 || in.BusinessOwnerID < 1 {
		httpx.Fail(w, httpx.BadRequest("请填写请求编号、当前业务负责人版本和接任主管"))
		return
	}
	bindManaged(r.Context(), id, *in.ExpectedVersion)
	result, err := h.repo.TransferBusinessOwner(r.Context(), in.RequestID, id, *in.ExpectedVersion, in.BusinessOwnerID)
	commandResponse(w, result, err)
}
