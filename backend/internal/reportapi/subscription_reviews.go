package reportapi

import (
	"net/http"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

func (h *SubscriptionHandler) ConfirmPersonal(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var input struct {
		RequestID string `json:"request_id"`
		Version   int64  `json:"version"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(input.RequestID) || input.Version < 1 {
		subscriptionError(w, db.ErrAutomationInput)
		return
	}
	out, err := h.repo.ConfirmPersonalSubscription(r.Context(), input.RequestID, id, input.Version)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, out)
}

func (h *SubscriptionHandler) SubmitTeam(w http.ResponseWriter, r *http.Request) {
	var input struct {
		db.TeamSubscriptionInput
		RequestID string `json:"request_id"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(input.RequestID) {
		subscriptionError(w, db.ErrAutomationInput)
		return
	}
	out, err := h.repo.SubmitTeamSubscription(r.Context(), input.RequestID, input.TeamSubscriptionInput)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, out)
}

func (h *SubscriptionHandler) ReadTeam(review bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := reportID(w, r)
		if !ok {
			return
		}
		out, err := h.repo.TeamSubscription(r.Context(), id, review)
		if err != nil {
			subscriptionError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpx.OK(w, out)
	}
}

func (h *SubscriptionHandler) ReviewTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var input struct {
		RequestID string `json:"request_id"`
		Version   int64  `json:"version"`
		Basis     string `json:"basis_sha256"`
		Decision  string `json:"decision"`
		Comment   string `json:"comment"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(input.RequestID) {
		subscriptionError(w, db.ErrAutomationInput)
		return
	}
	out, err := h.repo.DecideTeamSubscription(r.Context(), input.RequestID, id, input.Version, input.Basis, input.Decision, input.Comment)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, out)
}
