// Package server 负责 HTTP 路由装配与中间件链组合。
package server

import (
	"log/slog"
	"net/http"

	"github.com/user/dbhub/internal/adminapi"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/datasource"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/health"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/metrics"
	"github.com/user/dbhub/internal/middleware"
	"github.com/user/dbhub/internal/queryapi"
)

// Deps 路由装配所需的仓储与处理器依赖（接入 PostgreSQL 后非空）。
type Deps struct {
	UserRepo *db.UserRepository
	Conns    *db.ConnectionRepository
	Tunnels  *db.TunnelRepository
	History  *db.HistoryRepository
	Audit    *db.AuditRepository
}

// NewRouter 组装全部路由。
func NewRouter(cfg *config.Config, log *slog.Logger, users auth.UserStore, deps *Deps) http.Handler {
	mux := http.NewServeMux()

	// 健康检查（不鉴权，供容器探针与负载均衡使用）
	healthH := health.NewHandler(cfg.Env)
	mux.HandleFunc("GET /api/health", healthH.Health)

	// 认证模块
	authH := auth.NewHandler(cfg, users, log)

	requireAuth := middleware.RequireAuth(cfg.SecretKey)

	if deps != nil {
		auditMW := middleware.Audit(deps.Audit, log)
		canWrite := middleware.RequireRoles("admin", "developer")
		requireAdmin := middleware.RequireRoles("admin")

		// 登录尝试也要审计（无用户上下文，user_id 留空）
		mux.Handle("POST /api/v1/auth/login",
			middleware.Chain(http.HandlerFunc(authH.Login), auditMW))
		mux.HandleFunc("POST /api/v1/auth/refresh", authH.Refresh)
		mux.Handle("GET /api/v1/auth/me",
			middleware.Chain(http.HandlerFunc(authH.Me), requireAuth))

		// 修改自己的密码（任意登录用户）
		adminH := adminapi.NewHandler(deps.UserRepo, log)
		mux.Handle("POST /api/v1/auth/change-password",
			middleware.Chain(http.HandlerFunc(adminH.ChangePassword), requireAuth, auditMW))

		// ---------- 用户与角色（仅 admin） ----------
		mux.Handle("GET /api/v1/roles",
			middleware.Chain(http.HandlerFunc(adminH.Roles), requireAuth, requireAdmin))
		mux.Handle("GET /api/v1/users",
			middleware.Chain(http.HandlerFunc(adminH.Users), requireAuth, requireAdmin))
		mux.Handle("POST /api/v1/users",
			middleware.Chain(http.HandlerFunc(adminH.CreateUser), requireAuth, requireAdmin, auditMW))
		mux.Handle("PUT /api/v1/users/{id}",
			middleware.Chain(http.HandlerFunc(adminH.UpdateUser), requireAuth, requireAdmin, auditMW))
		mux.Handle("PATCH /api/v1/users/{id}/status",
			middleware.Chain(http.HandlerFunc(adminH.SetUserStatus), requireAuth, requireAdmin, auditMW))
		mux.Handle("POST /api/v1/users/{id}/reset-password",
			middleware.Chain(http.HandlerFunc(adminH.ResetPassword), requireAuth, requireAdmin, auditMW))
		mux.Handle("DELETE /api/v1/users/{id}",
			middleware.Chain(http.HandlerFunc(adminH.DeleteUser), requireAuth, requireAdmin, auditMW))

		// 审计日志（仅 admin）
		auditH := adminapi.NewAuditHandler(deps.Audit)
		mux.Handle("GET /api/v1/audit-logs",
			middleware.Chain(http.HandlerFunc(auditH.AuditLogs), requireAuth, requireAdmin))

		// ---------- 数据源 ----------
		dsH := datasource.NewHandler(deps.Conns, deps.Tunnels, log)
		mux.Handle("GET /api/v1/connections",
			middleware.Chain(http.HandlerFunc(dsH.List), requireAuth))
		mux.Handle("POST /api/v1/connections",
			middleware.Chain(http.HandlerFunc(dsH.Create), requireAuth, canWrite, auditMW))
		mux.Handle("POST /api/v1/connections/test",
			middleware.Chain(http.HandlerFunc(dsH.TestUnpersisted), requireAuth, auditMW))
		mux.Handle("GET /api/v1/connections/{id}",
			middleware.Chain(http.HandlerFunc(dsH.Get), requireAuth))
		mux.Handle("PUT /api/v1/connections/{id}",
			middleware.Chain(http.HandlerFunc(dsH.Update), requireAuth, canWrite, auditMW))
		mux.Handle("DELETE /api/v1/connections/{id}",
			middleware.Chain(http.HandlerFunc(dsH.Delete), requireAuth, canWrite, auditMW))
		mux.Handle("POST /api/v1/connections/{id}/test",
			middleware.Chain(http.HandlerFunc(dsH.TestPersisted), requireAuth, auditMW))

		mux.Handle("GET /api/v1/ssh-tunnels",
			middleware.Chain(http.HandlerFunc(dsH.ListTunnels), requireAuth))
		mux.Handle("POST /api/v1/ssh-tunnels",
			middleware.Chain(http.HandlerFunc(dsH.CreateTunnel), requireAuth, canWrite, auditMW))
		mux.Handle("POST /api/v1/ssh-tunnels/test",
			middleware.Chain(http.HandlerFunc(dsH.TestTunnel), requireAuth, canWrite, auditMW))
		mux.Handle("PUT /api/v1/ssh-tunnels/{id}",
			middleware.Chain(http.HandlerFunc(dsH.UpdateTunnel), requireAuth, canWrite, auditMW))
		mux.Handle("DELETE /api/v1/ssh-tunnels/{id}",
			middleware.Chain(http.HandlerFunc(dsH.DeleteTunnel), requireAuth, canWrite, auditMW))

		// ---------- 仪表盘汇总指标 ----------
		mH := metrics.NewHandler(deps.Conns, deps.History, deps.Audit, deps.UserRepo)
		mux.Handle("GET /api/v1/metrics/overview",
			middleware.Chain(http.HandlerFunc(mH.Overview), requireAuth))

		// ---------- SQL 工作台 ----------
		qH := queryapi.NewHandler(deps.Conns, deps.Tunnels, deps.History, log)
		mux.Handle("POST /api/v1/query/execute",
			middleware.Chain(http.HandlerFunc(qH.Execute), requireAuth, auditMW))
		mux.Handle("GET /api/v1/metadata/databases",
			middleware.Chain(http.HandlerFunc(qH.Databases), requireAuth))
		mux.Handle("GET /api/v1/metadata/schemas",
			middleware.Chain(http.HandlerFunc(qH.Schemas), requireAuth))
		mux.Handle("GET /api/v1/metadata/tables",
			middleware.Chain(http.HandlerFunc(qH.Tables), requireAuth))
		mux.Handle("GET /api/v1/metadata/columns",
			middleware.Chain(http.HandlerFunc(qH.Columns), requireAuth))
		mux.Handle("GET /api/v1/data/preview",
			middleware.Chain(http.HandlerFunc(qH.Preview), requireAuth))
		mux.Handle("GET /api/v1/redis/overview",
			middleware.Chain(http.HandlerFunc(qH.RedisOverview), requireAuth))
		mux.Handle("GET /api/v1/redis/keys",
			middleware.Chain(http.HandlerFunc(qH.RedisKeys), requireAuth))
		mux.Handle("GET /api/v1/redis/value",
			middleware.Chain(http.HandlerFunc(qH.RedisValue), requireAuth))
		mux.Handle("GET /api/v1/query/history",
			middleware.Chain(http.HandlerFunc(qH.History), requireAuth))
		mux.Handle("DELETE /api/v1/query/history/{id}",
			middleware.Chain(http.HandlerFunc(qH.DeleteHistory), requireAuth, auditMW))
		mux.Handle("DELETE /api/v1/query/history",
			middleware.Chain(http.HandlerFunc(qH.ClearHistory), requireAuth, auditMW))
	} else {
		// 无元数据库（内存模式）下的最小认证路由
		mux.HandleFunc("POST /api/v1/auth/login", authH.Login)
		mux.HandleFunc("POST /api/v1/auth/refresh", authH.Refresh)
		mux.Handle("GET /api/v1/auth/me",
			middleware.Chain(http.HandlerFunc(authH.Me), requireAuth))
	}

	// 未匹配路由统一 404 信封
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		httpx.Fail(w, httpx.NotFound("接口不存在"))
	})

	allowedOrigins := []string{"*"}
	if cfg.IsProduction() {
		// 生产环境通过环境变量 CORS_ALLOWED_ORIGINS 注入白名单（逗号分隔）
		allowedOrigins = []string{} // TODO: 读取 CORS_ALLOWED_ORIGINS
	}

	return middleware.Chain(
		mux,
		middleware.RequestID,
		middleware.Recoverer(log),
		middleware.RequestLogger(log),
		middleware.CORS(allowedOrigins),
	)
}
