package reportapi

import (
	"errors"
	"net/http"
	"strconv"

	"log/slog"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type ShareHandler struct {
	shares     *db.ShareRepository
	reports    *db.ReportRepository
	dashboards *db.DashboardRepository
	log        *slog.Logger
}

func NewShareHandler(shares *db.ShareRepository, reports *db.ReportRepository, dashboards *db.DashboardRepository, log *slog.Logger) *ShareHandler {
	return &ShareHandler{shares: shares, reports: reports, dashboards: dashboards, log: log}
}

type createShareRequest struct {
	SubjectType string `json:"subject_type"` // report | dashboard
	SubjectID   int64  `json:"subject_id"`
	ExpireDays  *int   `json:"expire_days"` // 1 | 7 | 30 | nil=永久
}

func (h *ShareHandler) Create(w http.ResponseWriter, r *http.Request) {
	httpx.Fail(w, httpx.Forbidden("个人连接阶段暂不提供公开分享链接，请使用登录后的共享报表定义"))
}

func (h *ShareHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	subjectType := q.Get("subject_type")
	subjectID, _ := strconv.ParseInt(q.Get("subject_id"), 10, 64)
	if subjectType == "" || subjectID <= 0 {
		httpx.Fail(w, httpx.BadRequest("subject_type 与 subject_id 必填"))
		return
	}
	list, err := h.shares.ListBySubject(r.Context(), subjectType, subjectID)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询分享失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": list})
}

func (h *ShareHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	if err := h.shares.Revoke(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.Fail(w, httpx.NotFound("分享不存在或无权限"))
			return
		}
		httpx.Fail(w, httpx.Internal("吊销失败", err))
		return
	}
	httpx.OK(w, map[string]any{"id": id, "revoked": true})
}

func (h *ShareHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	if err := h.shares.Delete(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.Fail(w, httpx.NotFound("分享不存在或无权限"))
			return
		}
		httpx.Fail(w, httpx.Internal("删除失败", err))
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

// PublicHandler 免登录只读分享页数据接口
type PublicHandler struct {
	shares     *db.ShareRepository
	reports    *db.ReportRepository
	dashboards *db.DashboardRepository
	conns      *db.ConnectionRepository
	log        *slog.Logger
}

func NewPublicHandler(shares *db.ShareRepository, reports *db.ReportRepository, dashboards *db.DashboardRepository, conns *db.ConnectionRepository, log *slog.Logger) *PublicHandler {
	return &PublicHandler{shares: shares, reports: reports, dashboards: dashboards, conns: conns, log: log}
}

func (h *PublicHandler) Get(w http.ResponseWriter, r *http.Request) {
	httpx.Fail(w, httpx.NotFound("此公开分享已停用，请登录后使用自己的连接"))
}
