// Package metrics 提供仪表盘汇总指标接口。
package metrics

import (
	"net/http"
	"strconv"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// Handler 仪表盘指标处理器。
type Handler struct {
	conns   *db.ConnectionRepository
	history *db.HistoryRepository
	audit   *db.AuditRepository
	users   *db.UserRepository
}

func NewHandler(conns *db.ConnectionRepository, history *db.HistoryRepository, audit *db.AuditRepository, users *db.UserRepository) *Handler {
	return &Handler{conns: conns, history: history, audit: audit, users: users}
}

// Overview GET /api/v1/metrics/overview?days=14
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	isAdmin := claims != nil && claims.Role == "admin"

	days := 14
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d >= 1 && d <= 90 {
		days = d
	}

	ctx := r.Context()
	totalConns, byType, err := h.conns.Count(ctx)
	if err != nil {
		httpx.Fail(w, httpx.Internal("统计数据源失败", err))
		return
	}
	todayQueries, activeUsers, err := h.history.TodayStats(ctx)
	if err != nil {
		httpx.Fail(w, httpx.Internal("统计今日指标失败", err))
		return
	}
	auditEvents, err := h.audit.TotalCount(ctx)
	if err != nil {
		httpx.Fail(w, httpx.Internal("统计审计事件失败", err))
		return
	}
	trend, err := h.history.DailyTrend(ctx, days)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询趋势失败", err))
		return
	}
	rank, err := h.history.ConnectionRank(ctx, days, 8)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询排行失败", err))
		return
	}
	recent, err := h.history.RecentQueries(ctx, claims.Subject, isAdmin, 8)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询最近记录失败", err))
		return
	}

	httpx.OK(w, map[string]any{
		"kpi": map[string]int64{
			"connections":        totalConns,
			"today_queries":      todayQueries,
			"today_active_users": activeUsers,
			"audit_events":       auditEvents,
		},
		"connection_types": byType,
		"trend":            trend,
		"rank":             rank,
		"recent":           recent,
		"days":             days,
	})
}
