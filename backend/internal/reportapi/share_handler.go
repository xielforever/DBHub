package reportapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"log/slog"

	"github.com/user/dbhub/internal/auth"
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
	c, _ := auth.ClaimsFromContext(r.Context())
	var in createShareRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.SubjectType != "report" && in.SubjectType != "dashboard" {
		httpx.Fail(w, httpx.BadRequest("subject_type 仅支持 report/dashboard"))
		return
	}
	if in.SubjectID <= 0 {
		httpx.Fail(w, httpx.BadRequest("subject_id 必填"))
		return
	}
	if in.SubjectType == "report" {
		rep, err := h.reports.Get(r.Context(), in.SubjectID)
		if err != nil {
			httpx.Fail(w, httpx.BadRequest("报表不存在"))
			return
		}
		if rep.OwnerUserID != c.Subject && c.Role != "admin" {
			httpx.Fail(w, &httpx.AppError{Code: httpx.CodeForbidden, Message: "仅所有者可分享"})
			return
		}
	} else {
		dash, err := h.dashboards.Get(r.Context(), in.SubjectID)
		if err != nil {
			httpx.Fail(w, httpx.BadRequest("仪表盘不存在"))
			return
		}
		if dash.OwnerUserID != c.Subject && c.Role != "admin" {
			httpx.Fail(w, &httpx.AppError{Code: httpx.CodeForbidden, Message: "仅所有者可分享"})
			return
		}
	}

	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		httpx.Fail(w, httpx.Internal("生成分享令牌失败", err))
		return
	}
	plain := hex.EncodeToString(b)

	var expireAt *time.Time
	if in.ExpireDays != nil {
		t := time.Now().Add(time.Duration(*in.ExpireDays) * 24 * time.Hour)
		expireAt = &t
	}

	s := &db.ShareToken{
		SubjectType: in.SubjectType,
		SubjectID:   in.SubjectID,
		CreatedBy:   c.Subject,
		ExpireAt:    expireAt,
	}
	if err := h.shares.Create(r.Context(), s, plain); err != nil {
		httpx.Fail(w, httpx.Internal("创建分享失败", err))
		return
	}
	httpx.OK(w, map[string]any{
		"id":           s.ID,
		"subject_type": s.SubjectType,
		"subject_id":   s.SubjectID,
		"token":        plain,
		"expire_at":    s.ExpireAt,
		"created_at":   s.CreatedAt,
	})
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
	token := r.PathValue("token")
	if token == "" {
		httpx.Fail(w, httpx.NotFound("分享不存在"))
		return
	}
	hash := db.HashToken(token)
	s, err := h.shares.GetByHash(r.Context(), hash)
	if err != nil {
		httpx.Fail(w, httpx.NotFound("分享不存在或已失效"))
		return
	}
	if s.Revoked {
		httpx.Fail(w, httpx.NotFound("分享已吊销"))
		return
	}
	if s.ExpireAt != nil && time.Now().After(*s.ExpireAt) {
		httpx.Fail(w, httpx.NotFound("分享已过期"))
		return
	}
	go func() {
		_ = h.shares.IncrementAccess(r.Context(), s.ID)
	}()

	if s.SubjectType == "report" {
		rep, err := h.reports.Get(r.Context(), s.SubjectID)
		if err != nil {
			httpx.Fail(w, httpx.NotFound("报表不存在"))
			return
		}
		httpx.OK(w, map[string]any{
			"share_id":     s.ID,
			"subject_type": s.SubjectType,
			"subject":      rep,
			"owner_name":   rep.OwnerName,
			"expire_at":    s.ExpireAt,
			"access_count": s.AccessCount + 1,
			"type":         "report",
			"report":       rep,
			"share":        s,
		})
		return
	}
	dash, err := h.dashboards.Get(r.Context(), s.SubjectID)
	if err != nil {
		httpx.Fail(w, httpx.NotFound("仪表盘不存在"))
		return
	}
	// 解析 layout，批量加载关联报表
	var reports []any
	if len(dash.Layout) > 0 {
		var items []struct {
			ReportID int64 `json:"report_id"`
		}
		if err := json.Unmarshal(dash.Layout, &items); err == nil {
			for _, it := range items {
				if it.ReportID <= 0 {
					continue
				}
				if rep, err := h.reports.Get(r.Context(), it.ReportID); err == nil {
					reports = append(reports, rep)
				}
			}
		}
	}
	dashMap := map[string]any{
		"id":          dash.ID,
		"name":        dash.Name,
		"description": dash.Description,
		"visibility":  dash.Visibility,
		"layout":      json.RawMessage(dash.Layout),
		"owner_name":  dash.OwnerName,
		"reports":     reports,
	}
	httpx.OK(w, map[string]any{
		"share_id":     s.ID,
		"subject_type": s.SubjectType,
		"subject":      dashMap,
		"owner_name":   dash.OwnerName,
		"expire_at":    s.ExpireAt,
		"access_count": s.AccessCount + 1,
		"type":         "dashboard",
		"dashboard":    dashMap,
		"share":        s,
	})
}
