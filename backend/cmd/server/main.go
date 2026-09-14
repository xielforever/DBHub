// DBHub 后端服务入口。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/logx"
	"github.com/user/dbhub/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).
			Error("配置加载失败", "error", err)
		os.Exit(1)
	}

	log := logx.New(cfg.Env, cfg.LogLevel)
	// 让 httpx 等包级 slog 调用也走统一 handler，避免重复/格式不一致
	slog.SetDefault(log)

	users, err := auth.NewMemoryStore(cfg.Admin)
	if err != nil {
		log.Error("初始化用户存储失败", "error", err)
		os.Exit(1)
	}
	// TODO: 初始化 PostgreSQL 连接池并执行迁移，替换 MemoryStore
	// TODO: 初始化 Redis 客户端（会话/黑名单/缓存）

	router := server.NewRouter(cfg, log, users)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       75 * time.Second,
	}

	// 优雅停机
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("DBHub 后端启动", "env", cfg.Env, "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 服务异常退出", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("收到关停信号，正在优雅停机...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("优雅停机失败", "error", err)
		os.Exit(1)
	}
	log.Info("服务已安全退出")
}
