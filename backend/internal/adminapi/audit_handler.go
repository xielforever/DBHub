package adminapi

import (
	"net/http"
	"time"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// AuditHandler 审计日志查询（仅 admin）。
type AuditHandler struct {
	audit *db.AuditRepository
}

func NewAuditHandler(audit *db.AuditRepository) *AuditHandler {
	return &AuditHandler{audit: audit}
}

// AuditLogs GET /api/v1/audit-logs
func (h *AuditHandler) AuditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	pageSize := atoiDefault(q.Get("page_size"), 20)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	filter := db.AuditFilter{
		Username:     q.Get("username"),
		Action:       q.Get("action"),
		ResourceType: q.Get("resource_type"),
		Status:       q.Get("status"),
	}
	if days := atoiDefault(q.Get("days"), 0); days > 0 && days <= 365 {
		filter.End = time.Now()
		filter.Start = filter.End.AddDate(0, 0, -days)
	}

	items, total, err := h.audit.List(r.Context(), filter, pageSize, (page-1)*pageSize)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询审计日志失败", err))
		return
	}
	httpx.OK(w, map[string]any{
		"items": items, "total": total, "page": page, "page_size": pageSize,
	})
}
