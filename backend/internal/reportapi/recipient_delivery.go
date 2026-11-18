package reportapi

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

func (h *ManagedHandler) IssueExportCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, err := h.repo.IssueFormalExport(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, out)
}

func (h *ManagedHandler) RedeemExportCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		Credential string `json:"credential"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	file, err := h.repo.RedeemFormalExport(r.Context(), id, in.Credential)
	if err != nil {
		ManagedError(w, err)
		return
	}
	mime := "application/json; charset=utf-8"
	switch file.Format {
	case "csv":
		mime = "text/csv; charset=utf-8"
	case "xlsx":
		mime = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Report-File-SHA256", file.SHA256)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="report-%d-export-%d.%s"`, file.ReportID, id, file.Format))
	w.Header().Set("Content-Length", strconv.Itoa(len(file.Bytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Bytes)
}

func (h *SubscriptionHandler) ChangeTeamRecipient(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		db.TeamRecipientCommand
		RequestID string `json:"request_id"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(in.RequestID) {
		subscriptionError(w, db.ErrManagedInput)
		return
	}
	out, err := h.repo.ChangeTeamRecipient(r.Context(), in.RequestID, id, in.TeamRecipientCommand)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, out)
}

func (h *SubscriptionHandler) OpenTeamPeriod(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
		Version   int64  `json:"version"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(in.RequestID) || in.Version < 1 {
		subscriptionError(w, db.ErrManagedInput)
		return
	}
	out, err := h.repo.OpenTeamPeriod(r.Context(), in.RequestID, id, in.Version)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, out)
}

func (h *SubscriptionHandler) TeamStatuses(owner bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var plan, period int64
		if owner {
			var ok bool
			plan, ok = reportID(w, r)
			if !ok {
				return
			}
			var err error
			period, err = strconv.ParseInt(r.PathValue("period"), 10, 64)
			if err != nil || period < 1 {
				subscriptionError(w, db.ErrManagedInput)
				return
			}
		}
		items, err := h.repo.TeamDeliveryStatuses(r.Context(), plan, period)
		if err != nil {
			subscriptionError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpx.OK(w, map[string]any{"items": items})
	}
}

func (h *SubscriptionHandler) TeamDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !requestKey(id) {
		subscriptionError(w, db.ErrManagedInput)
		return
	}
	out, err := h.repo.TeamDelivery(r.Context(), id)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"snapshot": out})
}
