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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/logx"
	"github.com/user/dbhub/internal/server"
	securecrypto "github.com/user/dbhub/pkg/crypto"
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

	// 凭据加密器（连接口令、SSH 私钥等）
	cipher, err := securecrypto.NewCipher(cfg.SecretKey)
	if err != nil {
		log.Error("初始化凭据加密器失败", "error", err)
		os.Exit(1)
	}

	// 用户存储：配置 DATABASE_URL 时接入 PostgreSQL 元数据库，否则回退内存存储
	var users auth.UserStore
	var deps *server.Deps
	var pgPool *pgxpool.Pool
	if cfg.DatabaseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var err error
		pgPool, err = db.Connect(ctx, cfg.DatabaseURL, cfg.MetaDBMaxConns)
		cancel()
		if err != nil {
			log.Error("连接元数据库失败", "error", err)
			os.Exit(1)
		}
		migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 30*time.Second)
		applied, err := db.Migrate(migrateCtx, pgPool)
		migrateCancel()
		if err != nil {
			log.Error("数据库迁移失败", "error", err)
			os.Exit(1)
		}
		if len(applied) > 0 {
			log.Info("数据库迁移完成", "files", applied)
		}

		adminHash, err := auth.HashPassword(cfg.Admin.Password)
		if err != nil {
			log.Error("引导管理员密码哈希失败", "error", err)
			os.Exit(1)
		}
		userRepo := db.NewUserRepository(pgPool)
		if err := userRepo.EnsureAdmin(context.Background(), cfg.Admin.Username, adminHash); err != nil {
			log.Error("引导管理员初始化失败", "error", err)
			os.Exit(1)
		}
		users = userRepo
		deps = &server.Deps{
			UserRepo: userRepo,
			Conns:    db.NewConnectionRepository(pgPool, cipher),
			Tunnels:  db.NewTunnelRepository(pgPool, cipher),
			History:  db.NewHistoryRepository(pgPool),
			Audit:    db.NewAuditRepository(pgPool),
		}
		log.Info("已接入 PostgreSQL 元数据库")
	} else {
		memoryUsers, err := auth.NewMemoryStore(cfg.Admin)
		if err != nil {
			log.Error("初始化用户存储失败", "error", err)
			os.Exit(1)
		}
		users = memoryUsers
		log.Warn("未配置 DATABASE_URL，使用内存用户存储（仅适合本地快速调试）")
		// TODO: 初始化 Redis 客户端（会话/黑名单/缓存）
	}

	router := server.NewRouter(cfg, log, users, deps)
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
	if pgPool != nil {
		pgPool.Close()
	}
	log.Info("服务已安全退出")
}
