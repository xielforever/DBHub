package reportapi

import (
	"errors"
	"net/http"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type SubscriptionHandler struct {
	repo    *db.ReportSubscriptionRepository
	webhook bool
}

func NewSubscriptionHandler(repo *db.ReportSubscriptionRepository, webhook bool) *SubscriptionHandler {
	return &SubscriptionHandler{repo, webhook}
}
func subscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrAutomationInput):
		httpx.Fail(w, httpx.BadRequest(err.Error()))
	case errors.Is(err, db.ErrAutomationConflict):
		httpx.Fail(w, httpx.Conflict(err.Error()))
	case errors.Is(err, db.ErrAutomationLimit):
		httpx.JSON(w, 429, httpx.Envelope{Code: 42900, Message: err.Error()})
	case errors.Is(err, db.ErrAutomationDenied):
		httpx.Fail(w, httpx.Forbidden("当前身份无权操作此订阅"))
	default:
		ManagedError(w, err)
	}
}
func (h *SubscriptionHandler) List(w http.ResponseWriter, r *http.Request) {
	archived := r.URL.Query().Get("include_archived")
	if archived != "" && archived != "true" && archived != "false" {
		httpx.Fail(w, httpx.BadRequest("归档筛选无效"))
		return
	}
	items, err := h.repo.List(r.Context(), archived == "true")
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "webhook_configured": h.webhook})
}
func (h *SubscriptionHandler) Save(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RequestID  string                          `json:"request_id"`
		Version    int64                           `json:"version"`
		Definition db.ReportSubscriptionDefinition `json:"definition"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(input.RequestID) {
		httpx.Fail(w, httpx.BadRequest("request_id必须为非空UUID"))
		return
	}
	id := int64(0)
	if r.PathValue("id") != "" {
		var ok bool
		id, ok = reportID(w, r)
		if !ok {
			return
		}
		if input.Version < 1 {
			httpx.Fail(w, httpx.BadRequest("订阅版本必填"))
			return
		}
	}
	out, err := h.repo.Save(r.Context(), input.RequestID, id, input.Version, input.Definition, h.webhook)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, out)
}
func (h *SubscriptionHandler) Command(archive bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			httpx.Fail(w, httpx.BadRequest("请求编号和订阅版本必填"))
			return
		}
		var out db.ReportCommandResult
		var err error
		if archive {
			out, err = h.repo.Archive(r.Context(), input.RequestID, id, input.Version)
		} else {
			out, err = h.repo.EnqueueNow(r.Context(), input.RequestID, id, input.Version)
		}
		if err != nil {
			subscriptionError(w, err)
			return
		}
		httpx.OK(w, out)
	}
}
func (h *SubscriptionHandler) Runs(w http.ResponseWriter, r *http.Request) {
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	items, total, err := h.repo.Runs(r.Context(), size, (page-1)*size)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *SubscriptionHandler) Delivery(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	snapshot, err := h.repo.Delivery(r.Context(), id)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	hooks, err := h.repo.WebhookStatuses(r.Context(), id)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"snapshot": snapshot, "webhook_deliveries": hooks})
}
func (h *SubscriptionHandler) Incidents(w http.ResponseWriter, r *http.Request) {
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	items, total, err := h.repo.Incidents(r.Context(), size, (page-1)*size)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *SubscriptionHandler) Incident(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	item, events, err := h.repo.Incident(r.Context(), id)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"incident": item, "events": events})
}
func (h *SubscriptionHandler) IncidentAction(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var input struct {
		RequestID string `json:"request_id"`
		Version   int64  `json:"version"`
		Action    string `json:"action"`
		Comment   string `json:"comment"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(input.RequestID) || input.Version < 1 {
		httpx.Fail(w, httpx.BadRequest("请求编号和跟踪版本必填"))
		return
	}
	out, err := h.repo.IncidentAction(r.Context(), input.RequestID, id, input.Version, input.Action, input.Comment)
	if err != nil {
		subscriptionError(w, err)
		return
	}
	httpx.OK(w, out)
}
