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
	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/lineage"
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

	var sessionRedis *redis.Client
	if cfg.RedisURL != "" {
		options, e := redis.ParseURL(cfg.RedisURL)
		if e != nil {
			log.Error("Redis 配置无效")
			os.Exit(1)
		}
		options.ContextTimeoutEnabled = true
		sessionRedis = redis.NewClient(options)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e = sessionRedis.Ping(ctx).Err()
		cancel()
		if e != nil {
			log.Error("Redis 会话服务连接失败")
			os.Exit(1)
		}
		defer sessionRedis.Close()
	}
	sessions := auth.NewSessionStore(sessionRedis)
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
			UserRepo:   userRepo,
			Conns:      db.NewConnectionRepository(pgPool, cipher),
			Tunnels:    db.NewTunnelRepository(pgPool, cipher),
			Proxies:    db.NewProxyRepository(pgPool, cipher),
			History:    db.NewHistoryRepository(pgPool),
			Audit:      db.NewAuditRepository(pgPool),
			Meta:       db.NewMetadataRepository(pgPool),
			Reports:    db.NewReportRepository(pgPool),
			Dashboards: db.NewDashboardRepository(pgPool),
			Shares:     db.NewShareRepository(pgPool),
			Tickets:    db.NewTicketRepository(pgPool),
			Lineage:    db.NewLineageRepository(pgPool),
			Snippets:   db.NewSnippetRepository(pgPool),
			Sessions:   sessions,
			Redis:      sessionRedis, Controls: db.NewQueryControls(pgPool),
		}
		log.Info("已接入 PostgreSQL 元数据库")
		deps.AuditJournal, err = auditlog.Open(cfg.AuditDir, cfg.AuditMaxPending, deps.Audit, log)
		if err != nil {
			log.Error("初始化审计持久化队列失败", "error", err)
			os.Exit(1)
		}
	} else {
		memoryUsers, err := auth.NewMemoryStore(cfg.Admin)
		if err != nil {
			log.Error("初始化用户存储失败", "error", err)
			os.Exit(1)
		}
		users = memoryUsers
		log.Warn("未配置 DATABASE_URL，使用内存用户存储（仅适合本地快速调试）")
		deps = &server.Deps{Sessions: sessions}
	}

	router := server.NewRouter(cfg, log, users, deps)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       75 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	// 优雅停机
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	automationService := server.StartAutomation(ctx, cfg, log, deps)

	// 血缘定时解析：每 5 分钟增量扫描 sys_query_history（pg_query_go/vitess → sys_meta_lineage）
	if deps != nil && deps.Lineage != nil {
		lineageSvc := lineage.NewService(deps.Lineage, deps.History, deps.Conns, deps.Meta, log)
		lineageSvc.StartScheduler(ctx, 5*time.Minute)
		log.Info("血缘定时解析已启动", "interval", "5m")
	}

	go func() {
		log.Info("DBHub 后端启动", "env", cfg.Env, "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 服务异常退出", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("收到关停信号，正在优雅停机...")
	if deps != nil && deps.Queries != nil {
		deps.Queries.Close()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if automationService != nil {
		if err := automationService.Close(shutdownCtx); err != nil {
			log.Warn("后台任务尚未停止，将由租约恢复状态")
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("优雅停机失败", "error", err)
		os.Exit(1)
	}
	if pgPool != nil {
		if err := deps.AuditJournal.Close(shutdownCtx); err != nil {
			log.Warn("审计队列尚未写完，将在下次启动后恢复")
		}
		db.CloseScopedRuntime(pgPool)
		pgPool.Close()
	}
	log.Info("服务已安全退出")
}
