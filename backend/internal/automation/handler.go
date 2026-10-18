package automation

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type Handler struct {
	Repo    *db.AutomationRepository
	Conns   *db.ConnectionRepository
	Drivers *connectorruntime.Runtime
	Journal *auditlog.Journal
	Config  config.OperationsConfig
}

var commandID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func fail(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("记录不存在或无权访问"))
	case errors.Is(e, db.ErrAutomationInput):
		httpx.Fail(w, httpx.BadRequest(e.Error()))
	case errors.Is(e, db.ErrAutomationDenied), errors.Is(e, db.ErrSourceAccessDenied), errors.Is(e, db.ErrCatalogDisabled):
		httpx.Fail(w, httpx.Forbidden("当前身份无权执行此操作，或数据源已停用"))
	case errors.Is(e, db.ErrAutomationConflict):
		httpx.Fail(w, httpx.Conflict(e.Error()))
	case errors.Is(e, db.ErrAutomationLimit):
		w.Header().Set("Retry-After", "30")
		httpx.JSON(w, 429, httpx.Envelope{Code: 42900, Message: e.Error()})
	default:
		httpx.Fail(w, httpx.Internal("后台任务或通知服务暂不可用", e))
	}
}
func page(r *http.Request) (int, int, error) {
	p, size := 1, 20
	for _, field := range []struct {
		name  string
		value *int
		max   int
	}{{"page", &p, 100000}, {"page_size", &size, 100}} {
		if raw := r.URL.Query().Get(field.name); raw != "" {
			v, e := strconv.Atoi(raw)
			if e != nil || v < 1 || v > field.max {
				return 0, 0, db.ErrAutomationInput
			}
			*field.value = v
		}
	}
	return size, (p - 1) * size, nil
}

func (h *Handler) Inbox(w http.ResponseWriter, r *http.Request) {
	size, offset, e := page(r)
	if e != nil {
		fail(w, e)
		return
	}
	state := r.URL.Query().Get("state")
	if state != "" && state != "unread" {
		fail(w, db.ErrAutomationInput)
		return
	}
	out, e := h.Repo.Inbox(r.Context(), state == "unread", size, offset)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Todos(w http.ResponseWriter, r *http.Request) {
	size, offset, e := page(r)
	if e != nil {
		fail(w, e)
		return
	}
	items, total, e := h.Repo.Todos(r.Context(), size, offset)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total})
}
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs       []int64 `json:"ids"`
		ThroughID int64   `json:"through_id"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	n, e := h.Repo.ReadNotifications(r.Context(), in.IDs, in.ThroughID)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"changed": n})
}
func (h *Handler) Tasks(w http.ResponseWriter, r *http.Request) {
	size, offset, e := page(r)
	if e != nil {
		fail(w, e)
		return
	}
	status := r.URL.Query().Get("status")
	valid := status == ""
	for _, s := range []string{"queued", "running", "cancel_requested", "succeeded", "failed", "cancelled", "interrupted", "stale", "skipped", "unknown"} {
		valid = valid || status == s
	}
	if !valid {
		fail(w, db.ErrAutomationInput)
		return
	}
	items, total, e := h.Repo.SourceTasks(r.Context(), status, size, offset)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total})
}
func (h *Handler) Task(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !commandID.MatchString(id) {
		fail(w, db.ErrAutomationInput)
		return
	}
	out, e := h.Repo.SourceTask(r.Context(), id)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) validConnection(r *http.Request, id int64, kind string) (*db.Connection, error) {
	if id <= 0 || (kind != "check" && kind != "collect") {
		return nil, db.ErrAutomationInput
	}
	conn, _, e := h.Conns.GetSecrets(r.Context(), id)
	if e != nil {
		return nil, e
	}
	if kind == "collect" && !h.Drivers.Capabilities(conn).Collect {
		return nil, db.ErrAutomationInput
	}
	return conn, nil
}
func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectionID int64  `json:"connection_id"`
		Kind         string `json:"kind"`
		RequestID    string `json:"request_id"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !commandID.MatchString(in.RequestID) {
		fail(w, db.ErrAutomationInput)
		return
	}
	conn, e := h.validConnection(r, in.ConnectionID, in.Kind)
	if e != nil {
		fail(w, e)
		return
	}
	out, e := h.Repo.EnqueueSourceTask(r.Context(), conn, in.Kind, in.RequestID, "")
	if e != nil {
		fail(w, e)
		return
	}
	httpx.JSON(w, http.StatusAccepted, httpx.Envelope{Code: 0, Message: "任务已加入队列", Data: out})
}
func (h *Handler) RetryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		RequestID string `json:"request_id"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !commandID.MatchString(id) || !commandID.MatchString(in.RequestID) {
		fail(w, db.ErrAutomationInput)
		return
	}
	old, e := h.Repo.SourceTask(r.Context(), id)
	if e != nil {
		fail(w, e)
		return
	}
	conn, e := h.validConnection(r, old.ConnectionID, old.Kind)
	if e != nil {
		fail(w, e)
		return
	}
	out, e := h.Repo.EnqueueSourceTask(r.Context(), conn, old.Kind, in.RequestID, id)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.JSON(w, http.StatusAccepted, httpx.Envelope{Code: 0, Message: "已创建新的重试任务", Data: out})
}
func (h *Handler) CancelTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !commandID.MatchString(id) {
		fail(w, db.ErrAutomationInput)
		return
	}
	out, e := h.Repo.CancelSourceTask(r.Context(), id)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Schedules(w http.ResponseWriter, r *http.Request) {
	items, e := h.Repo.SourceSchedules(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items})
}
func (h *Handler) SaveSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectionID    int64  `json:"connection_id"`
		Kind            string `json:"kind"`
		IntervalMinutes int    `json:"interval_minutes"`
		Enabled         bool   `json:"enabled"`
		Version         int64  `json:"version"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if in.Version < 0 {
		fail(w, db.ErrAutomationInput)
		return
	}
	conn, e := h.validConnection(r, in.ConnectionID, in.Kind)
	if e != nil {
		fail(w, e)
		return
	}
	out, e := h.Repo.SaveSourceSchedule(r.Context(), conn, in.Kind, in.IntervalMinutes, in.Enabled, in.Version)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) CheckHistory(w http.ResponseWriter, r *http.Request) {
	size, offset, e := page(r)
	if e != nil {
		fail(w, e)
		return
	}
	var cid int64
	if raw := r.URL.Query().Get("connection_id"); raw != "" {
		cid, e = strconv.ParseInt(raw, 10, 64)
		if e != nil || cid <= 0 {
			fail(w, db.ErrAutomationInput)
			return
		}
	}
	items, total, e := h.Repo.SourceCheckHistory(r.Context(), cid, size, offset)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total})
}
func (h *Handler) Operations(w http.ResponseWriter, r *http.Request) {
	states, alerts, e := h.Repo.Operations(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	var audit any
	if h.Journal != nil {
		audit = h.Journal.Status()
	}
	httpx.OK(w, map[string]any{"states": states, "alerts": alerts, "config": h.Config, "audit": audit})
}
