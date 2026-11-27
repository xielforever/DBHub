package reportapi

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

func (h *ManagedDashboardHandler) SubmitDelivery(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		db.DashboardDeliveryInput
		Request string `json:"request_id"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(in.Request) {
		ManagedError(w, db.ErrManagedInput)
		return
	}
	out, err := h.repo.SubmitDashboardDelivery(r.Context(), in.Request, id, in.DashboardDeliveryInput)
	if err != nil {
		ManagedError(w, err)
		return
	}
	httpx.OK(w, out)
}

func (h *ManagedDashboardHandler) ReviewDelivery(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		out, err := h.repo.ReviewDashboardDelivery(r.Context(), id)
		if err != nil {
			ManagedError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpx.OK(w, out)
		return
	}
	var in struct {
		Request  string `json:"request_id"`
		Version  int64  `json:"version"`
		Basis    string `json:"basis_sha256"`
		Decision string `json:"decision"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(in.Request) {
		ManagedError(w, db.ErrManagedInput)
		return
	}
	out, err := h.repo.DecideDashboardDelivery(r.Context(), in.Request, id, in.Version, in.Basis, in.Decision)
	if err != nil {
		ManagedError(w, err)
		return
	}
	httpx.OK(w, out)
}

func (h *ManagedDashboardHandler) ChangeDeliveryRecipient(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		db.TeamRecipientCommand
		Request string `json:"request_id"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(in.Request) {
		ManagedError(w, db.ErrManagedInput)
		return
	}
	out, err := h.repo.ChangeDashboardRecipient(r.Context(), in.Request, id, in.TeamRecipientCommand)
	if err != nil {
		ManagedError(w, err)
		return
	}
	httpx.OK(w, out)
}

func (h *ManagedDashboardHandler) DeliveryStates(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") == "" {
		out, err := h.repo.DashboardOwnDeliveryStates(r.Context())
		if err != nil {
			ManagedError(w, err)
			return
		}
		if out == nil {
			out = []db.DashboardOwnDeliveryState{}
		}
		httpx.OK(w, map[string]any{"items": out})
		return
	}
	plan, ok := reportID(w, r)
	if !ok {
		return
	}
	out, err := h.repo.DashboardDeliveryStates(r.Context(), plan)
	if err != nil {
		ManagedError(w, err)
		return
	}
	if out == nil {
		out = []db.DashboardDeliveryState{}
	}
	httpx.OK(w, map[string]any{"items": out})
}

func (h *ManagedDashboardHandler) Delivery(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, err := h.repo.DashboardDelivery(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, out)
}

func (h *ManagedDashboardHandler) DeliveryCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, err := h.repo.IssueDashboardFile(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, out)
}

func (h *ManagedDashboardHandler) RedeemDelivery(w http.ResponseWriter, r *http.Request) {
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
	out, err := h.repo.RedeemDashboardFile(r.Context(), id, in.Credential)
	if err != nil {
		ManagedError(w, err)
		return
	}
	mime := map[string]string{"zip": "application/zip", "csv": "text/csv; charset=utf-8", "json": "application/json; charset=utf-8",
		"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"}[out.Format]
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Report-File-SHA256", out.SHA256)
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="dashboard-delivery-%d.%s"`, id, out.Format))
	w.Header().Set("Content-Length", strconv.Itoa(len(out.File)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.File)
}

func (h *ManagedDashboardHandler) DeliveryAlerts(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	out, err := h.repo.DashboardDeliveryAlerts(r.Context(), id)
	if err != nil {
		ManagedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"items": out})
}

func (h *ManagedDashboardHandler) DeliveryAlertDisposition(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	event, err := strconv.ParseInt(r.PathValue("event"), 10, 64)
	if err != nil || event < 1 {
		ManagedError(w, db.ErrManagedInput)
		return
	}
	var in struct {
		Request string `json:"request_id"`
		Note    string `json:"note"`
	}
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(in.Request) {
		ManagedError(w, db.ErrManagedInput)
		return
	}
	out, err := h.repo.DisposeDashboardAlert(r.Context(), in.Request, id, event, in.Note)
	if err != nil {
		ManagedError(w, err)
		return
	}
	httpx.OK(w, out)
}
