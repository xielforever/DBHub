// Package server 负责 HTTP 路由装配与中间件链组合。
package server

import (
	"log/slog"
	"net/http"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/health"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

// NewRouter 组装全部路由。
func NewRouter(cfg *config.Config, log *slog.Logger, users auth.UserStore) http.Handler {
	mux := http.NewServeMux()

	// 健康检查（不鉴权，供容器探针与负载均衡使用）
	healthH := health.NewHandler(cfg.Env)
	mux.HandleFunc("GET /api/health", healthH.Health)

	// 认证模块
	authH := auth.NewHandler(cfg, users, log)
	mux.HandleFunc("POST /api/v1/auth/login", authH.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", authH.Refresh)

	// 需要 Access Token 的路由
	mux.Handle("GET /api/v1/auth/me",
		middleware.Chain(
			http.HandlerFunc(authH.Me),
			middleware.RequireAuth(cfg.SecretKey),
		),
	)

	// 未匹配路由统一 404 信封
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "接口不存在"})
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
