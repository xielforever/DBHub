// Package server 负责 HTTP 路由装配与中间件链组合。
package server

import (
	"log/slog"
	"net/http"

	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/adminapi"
	"github.com/user/dbhub/internal/aiapi"
	"github.com/user/dbhub/internal/assetapi"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/datasource"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/health"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/lineage"
	"github.com/user/dbhub/internal/lineageapi"
	"github.com/user/dbhub/internal/metrics"
	"github.com/user/dbhub/internal/middleware"
	"github.com/user/dbhub/internal/presenceapi"
	"github.com/user/dbhub/internal/proxyapi"
	"github.com/user/dbhub/internal/queryapi"
	"github.com/user/dbhub/internal/reportapi"
	"github.com/user/dbhub/internal/snippetapi"
	"github.com/user/dbhub/internal/ticketapi"
)

// Deps 路由装配所需的仓储与处理器依赖（接入 PostgreSQL 后非空）。
type Deps struct {
	UserRepo     *db.UserRepository
	Conns        *db.ConnectionRepository
	Tunnels      *db.TunnelRepository
	Proxies      *db.ProxyRepository
	History      *db.HistoryRepository
	Audit        *db.AuditRepository
	AuditJournal *auditlog.Journal
	Meta         *db.MetadataRepository
	Reports      *db.ReportRepository
	Dashboards   *db.DashboardRepository
	Shares       *db.ShareRepository
	Tickets      *db.TicketRepository
	Lineage      *db.LineageRepository
	Snippets     *db.SnippetRepository
	Queries      *queryapi.Handler
	Sessions     *auth.SessionStore
	Redis        *redis.Client
	Controls     *db.QueryControls
}

// NewRouter 组装全部路由。
func NewRouter(cfg *config.Config, log *slog.Logger, users auth.UserStore, deps *Deps) http.Handler {
	mux := http.NewServeMux()

	// 健康检查（不鉴权，供容器探针与负载均衡使用）
	healthH := health.NewHandler(cfg.Env)
	mux.HandleFunc("GET /api/health", healthH.Health)
	mux.HandleFunc("GET /api/ready", func(w http.ResponseWriter, r *http.Request) {
		if deps != nil && deps.Conns != nil && (deps.AuditJournal == nil || !deps.AuditJournal.Status().Ready) {
			httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "审计存储尚未就绪"})
			return
		}
		healthH.Health(w, r)
	})

	// 认证模块
	sessions := auth.NewSessionStore(nil)
	if deps != nil && deps.Sessions != nil {
		sessions = deps.Sessions
	}
	authH := auth.NewHandler(cfg, users, log, sessions)

	identityAuth := middleware.RequireAuth(cfg.SecretKey, users, sessions)
	requireAuth := identityAuth
	if deps != nil && deps.UserRepo != nil {
		scoped := workspaceAuth(deps.UserRepo)
		requireAuth = func(next http.Handler) http.Handler { return identityAuth(scoped(next)) }
		registerWorkspaces(mux, deps.UserRepo, identityAuth)
	}

	if deps != nil && deps.Conns != nil {
		drivers := connectorruntime.NewConfigured(cfg.JDBCURL, cfg.JDBCToken)
		drivers.RecordUsage = deps.Conns.MarkUsed
		canWrite := middleware.RequireRoles("admin", "developer")
		requireAdmin := middleware.RequireRoles("admin")

		// 登录尝试也要审计（无用户上下文，user_id 留空）
		mux.Handle("POST /api/v1/auth/login",
			middleware.Chain(http.HandlerFunc(authH.Login)))
		mux.HandleFunc("POST /api/v1/auth/refresh", authH.Refresh)
		mux.Handle("GET /api/v1/auth/me",
			middleware.Chain(http.HandlerFunc(authH.Me), identityAuth))

		// 修改自己的密码（任意登录用户）
		adminH := adminapi.NewHandler(deps.UserRepo, log)
		mux.Handle("POST /api/v1/auth/change-password",
			middleware.Chain(http.HandlerFunc(adminH.ChangePassword), identityAuth))

		// ---------- 用户与角色（仅 admin） ----------
		mux.Handle("GET /api/v1/roles",
			middleware.Chain(http.HandlerFunc(adminH.Roles), requireAuth, requireAdmin))
		mux.Handle("GET /api/v1/users",
			middleware.Chain(http.HandlerFunc(adminH.Users), requireAuth, requireAdmin))
		mux.Handle("POST /api/v1/users",
			middleware.Chain(http.HandlerFunc(adminH.CreateUser), requireAuth, requireAdmin))
		mux.Handle("PUT /api/v1/users/{id}",
			middleware.Chain(http.HandlerFunc(adminH.UpdateUser), requireAuth, requireAdmin))
		mux.Handle("PATCH /api/v1/users/{id}/status",
			middleware.Chain(http.HandlerFunc(adminH.SetUserStatus), requireAuth, requireAdmin))
		mux.Handle("POST /api/v1/users/{id}/reset-password",
			middleware.Chain(http.HandlerFunc(adminH.ResetPassword), requireAuth, requireAdmin))
		mux.Handle("DELETE /api/v1/users/{id}",
			middleware.Chain(http.HandlerFunc(adminH.DeleteUser), requireAuth, requireAdmin))

		mux.Handle("GET /api/v1/audience-groups", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			out, e := deps.UserRepo.AudienceGroups(r.Context())
			if e != nil {
				reportapi.ManagedError(w, e)
				return
			}
			httpx.OK(w, map[string]any{"items": out})
		}), requireAuth))
		mux.Handle("GET /api/v1/user-groups", middleware.Chain(http.HandlerFunc(adminH.Groups), requireAuth, requireAdmin))
		mux.Handle("POST /api/v1/user-groups", middleware.Chain(http.HandlerFunc(adminH.SaveGroup), requireAuth, requireAdmin))
		mux.Handle("PUT /api/v1/user-groups/{id}", middleware.Chain(http.HandlerFunc(adminH.SaveGroup), requireAuth, requireAdmin))
		mux.Handle("DELETE /api/v1/user-groups/{id}", middleware.Chain(http.HandlerFunc(adminH.DeleteGroup), requireAuth, requireAdmin))

		// 审计日志（仅 admin）
		auditH := adminapi.NewAuditHandler(deps.Audit, deps.AuditJournal)
		platformOnly := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !deps.UserRepo.IsPlatformAdmin(r.Context()) {
					httpx.Fail(w, httpx.Forbidden("仅平台管理员可查询身份审计与投递状态"))
					return
				}
				next.ServeHTTP(w, r)
			})
		}
		mux.Handle("GET /api/v1/platform/audit-logs", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auditH.AuditLogs(w, r.WithContext(db.WithPlatformAudit(r.Context())))
		}), identityAuth, platformOnly))
		mux.Handle("GET /api/v1/audit-logs",
			middleware.Chain(http.HandlerFunc(auditH.AuditLogs), requireAuth, requireAdmin))

		mux.Handle("GET /api/v1/audit-logs/delivery", middleware.Chain(http.HandlerFunc(auditH.Delivery), requireAuth, requireAdmin, platformOnly))

		// ---------- 数据源 ----------
		dsH := datasource.NewHandler(deps.Conns, deps.Tunnels, log, deps.Proxies, drivers)
		mux.Handle("GET /api/v1/connections",
			middleware.Chain(http.HandlerFunc(dsH.List), requireAuth))
		mux.Handle("GET /api/v1/connections/compatibility",
			middleware.Chain(http.HandlerFunc(dsH.CompatibilityMatrix), requireAuth))
		mux.Handle("POST /api/v1/connections",
			middleware.Chain(http.HandlerFunc(dsH.Create), requireAuth))
		mux.Handle("POST /api/v1/connections/test",
			middleware.Chain(http.HandlerFunc(dsH.TestUnpersisted), requireAuth))
		mux.Handle("GET /api/v1/connections/{id}",
			middleware.Chain(http.HandlerFunc(dsH.Get), requireAuth))
		mux.Handle("PUT /api/v1/connections/{id}",
			middleware.Chain(http.HandlerFunc(dsH.Update), requireAuth))
		mux.Handle("DELETE /api/v1/connections/{id}",
			middleware.Chain(http.HandlerFunc(dsH.Delete), requireAuth))
		mux.Handle("POST /api/v1/connections/{id}/test",
			middleware.Chain(http.HandlerFunc(dsH.TestPersisted), requireAuth))

		mux.Handle("GET /api/v1/connector-drivers/available", middleware.Chain(http.HandlerFunc(dsH.AvailableDrivers), requireAuth))
		mux.Handle("GET /api/v1/connector-drivers", middleware.Chain(http.HandlerFunc(dsH.Drivers), requireAuth))
		mux.Handle("POST /api/v1/connector-drivers", middleware.Chain(http.HandlerFunc(dsH.RegisterDriver), requireAuth))
		mux.Handle("GET /api/v1/datasource-templates", middleware.Chain(http.HandlerFunc(dsH.Templates), requireAuth))
		mux.Handle("POST /api/v1/datasource-templates", middleware.Chain(http.HandlerFunc(dsH.PublishTemplate), requireAuth))
		mux.Handle("PUT /api/v1/datasource-templates/{id}", middleware.Chain(http.HandlerFunc(dsH.PublishTemplate), requireAuth))
		mux.Handle("PATCH /api/v1/connector-drivers/{id}/status", middleware.Chain(dsH.SetCatalogStatus("driver"), requireAuth))
		mux.Handle("PATCH /api/v1/datasource-templates/{id}/status", middleware.Chain(dsH.SetCatalogStatus("template"), requireAuth))
		mux.Handle("DELETE /api/v1/connector-drivers/{id}", middleware.Chain(dsH.DeleteCatalog("driver"), requireAuth))
		mux.Handle("DELETE /api/v1/datasource-templates/{id}", middleware.Chain(dsH.DeleteCatalog("template"), requireAuth))
		mux.Handle("GET /api/v1/connections/{id}/capabilities", middleware.Chain(http.HandlerFunc(dsH.Capabilities), requireAuth))

		mux.Handle("GET /api/v1/datasource-definitions", middleware.Chain(http.HandlerFunc(dsH.Definitions), requireAuth))
		mux.Handle("GET /api/v1/datasource-definitions/categories", middleware.Chain(http.HandlerFunc(dsH.DefinitionCategories), requireAuth))
		mux.Handle("GET /api/v1/datasource-definitions/{id}/access", middleware.Chain(http.HandlerFunc(dsH.DefinitionAccess), requireAuth))
		mux.Handle("PUT /api/v1/datasource-definitions/{id}/access", middleware.Chain(http.HandlerFunc(dsH.DefinitionAccess), requireAuth))
		mux.Handle("PUT /api/v1/datasource-definitions/{id}/profile", middleware.Chain(http.HandlerFunc(dsH.DefinitionProfile), requireAuth))
		mux.Handle("PUT /api/v1/datasource-definitions/{id}/favorite", middleware.Chain(http.HandlerFunc(dsH.DefinitionFavorite), requireAuth))

		mux.Handle("GET /api/v1/datasource-definitions/{id}/revisions", middleware.Chain(http.HandlerFunc(dsH.DefinitionRevisions), requireAuth))
		mux.Handle("POST /api/v1/datasource-definitions", middleware.Chain(http.HandlerFunc(dsH.SaveDefinition), requireAuth))
		mux.Handle("POST /api/v1/datasource-definitions/{id}/revisions", middleware.Chain(http.HandlerFunc(dsH.SaveDefinition), requireAuth))
		mux.Handle("PATCH /api/v1/datasource-definitions/{id}/status", middleware.Chain(http.HandlerFunc(dsH.DefinitionStatus), requireAuth))
		mux.Handle("POST /api/v1/datasource-definitions/{id}/connections", middleware.Chain(http.HandlerFunc(dsH.AddPersonal), requireAuth))
		mux.Handle("POST /api/v1/connections/{id}/publish", middleware.Chain(http.HandlerFunc(dsH.PublishConnection), requireAuth))
		mux.Handle("POST /api/v1/connections/{id}/revision", middleware.Chain(http.HandlerFunc(dsH.UpgradePersonal), requireAuth))

		// SSH 隧道（已废弃，保留兼容，M5 迁移至代理）
		mux.Handle("GET /api/v1/ssh-tunnels",
			middleware.Chain(http.HandlerFunc(dsH.ListTunnels), requireAuth))
		mux.Handle("POST /api/v1/ssh-tunnels",
			middleware.Chain(http.HandlerFunc(dsH.CreateTunnel), requireAuth, canWrite))
		mux.Handle("POST /api/v1/ssh-tunnels/test",
			middleware.Chain(http.HandlerFunc(dsH.TestTunnel), requireAuth, canWrite))
		mux.Handle("PUT /api/v1/ssh-tunnels/{id}",
			middleware.Chain(http.HandlerFunc(dsH.UpdateTunnel), requireAuth, canWrite))
		mux.Handle("DELETE /api/v1/ssh-tunnels/{id}",
			middleware.Chain(http.HandlerFunc(dsH.DeleteTunnel), requireAuth, canWrite))

		// 代理管理与实际拨号
		if deps.Proxies != nil {
			proxyH := proxyapi.NewHandler(deps.Proxies, log)
			mux.Handle("GET /api/v1/proxies",
				middleware.Chain(http.HandlerFunc(proxyH.List), requireAuth))
			mux.Handle("POST /api/v1/proxies",
				middleware.Chain(http.HandlerFunc(proxyH.Create), requireAuth, canWrite))
			mux.Handle("POST /api/v1/proxies/test",
				middleware.Chain(http.HandlerFunc(proxyH.Test), requireAuth, canWrite))
			mux.Handle("PUT /api/v1/proxies/{id}",
				middleware.Chain(http.HandlerFunc(proxyH.Update), requireAuth, canWrite))
			mux.Handle("DELETE /api/v1/proxies/{id}",
				middleware.Chain(http.HandlerFunc(proxyH.Delete), requireAuth, canWrite))
		}

		// ---------- 工单中心（Step26 落库） ----------
		if deps.Tickets != nil {
			tH := ticketapi.NewHandler(deps.Tickets, log, deps.Conns, deps.Tunnels, deps.History, drivers)
			mux.Handle("POST /api/v1/tickets",
				middleware.Chain(http.HandlerFunc(tH.Create), requireAuth, canWrite))
			mux.Handle("GET /api/v1/tickets",
				middleware.Chain(http.HandlerFunc(tH.List), requireAuth))
			mux.Handle("GET /api/v1/tickets/{id}",
				middleware.Chain(http.HandlerFunc(tH.Get), requireAuth))
			mux.Handle("POST /api/v1/tickets/{id}/status",
				middleware.Chain(http.HandlerFunc(tH.UpdateStatus), requireAuth, requireAdmin))
			mux.Handle("POST /api/v1/tickets/{id}/execute", middleware.Chain(http.HandlerFunc(tH.Execute), requireAuth, requireAdmin))
		}

		// ---------- 仪表盘汇总指标 ----------
		mH := metrics.NewHandler(deps.Conns, deps.History, deps.Audit, deps.UserRepo)
		mux.Handle("GET /api/v1/metrics/overview",
			middleware.Chain(http.HandlerFunc(mH.Overview), requireAuth))

		snippetH := snippetapi.NewHandler(deps.Snippets)
		mux.Handle("GET /api/v1/snippets", middleware.Chain(http.HandlerFunc(snippetH.List), requireAuth))
		mux.Handle("POST /api/v1/snippets", middleware.Chain(http.HandlerFunc(snippetH.Create), requireAuth))
		mux.Handle("PUT /api/v1/snippets/{id}", middleware.Chain(http.HandlerFunc(snippetH.Update), requireAuth))
		mux.Handle("DELETE /api/v1/snippets/{id}", middleware.Chain(http.HandlerFunc(snippetH.Delete), requireAuth))
		// ---------- SQL 工作台 ----------
		qH := queryapi.NewHandler(deps.Conns, deps.Tunnels, deps.History, log, deps.Controls, drivers)
		mux.Handle("GET /api/v1/metrics/query", middleware.Chain(http.HandlerFunc(qH.QueryMetrics), requireAuth, requireAdmin, platformOnly))
		aiH := aiapi.New(cfg.AIBaseURL, cfg.AIKey, cfg.AIModel)
		mux.Handle("GET /api/v1/ai/status", middleware.Chain(http.HandlerFunc(aiH.Status), requireAuth))
		mux.Handle("POST /api/v1/ai/complete", middleware.Chain(http.HandlerFunc(aiH.Complete), requireAuth))
		mux.Handle("GET /api/v1/query/exports", middleware.Chain(http.HandlerFunc(qH.ListExports), requireAuth))
		mux.Handle("POST /api/v1/query/exports", middleware.Chain(http.HandlerFunc(qH.CreateExport), requireAuth))
		mux.Handle("POST /api/v1/query/exports/{id}/review", middleware.Chain(http.HandlerFunc(qH.ReviewExport), requireAuth, requireAdmin))
		mux.Handle("GET /api/v1/query/exports/{id}/download", middleware.Chain(http.HandlerFunc(qH.DownloadExport), requireAuth))
		presenceH := &presenceapi.Handler{Redis: deps.Redis, Conns: deps.Conns}
		mux.Handle("POST /api/v1/query/presence", middleware.Chain(http.HandlerFunc(presenceH.Heartbeat), requireAuth))
		mux.Handle("GET /api/v1/query/presence", middleware.Chain(http.HandlerFunc(presenceH.List), requireAuth))
		mux.Handle("GET /api/v1/query/settings", middleware.Chain(http.HandlerFunc(qH.Settings), requireAuth))
		mux.Handle("PUT /api/v1/query/settings", middleware.Chain(http.HandlerFunc(qH.SaveSettings), requireAuth))
		mux.Handle("GET /api/v1/query/policies", middleware.Chain(http.HandlerFunc(qH.Policies), requireAuth))
		mux.Handle("PUT /api/v1/query/policies", middleware.Chain(http.HandlerFunc(qH.SavePolicy), requireAuth, requireAdmin))
		mux.Handle("DELETE /api/v1/query/policies/{id}", middleware.Chain(http.HandlerFunc(qH.DeletePolicy), requireAuth, requireAdmin))
		mux.Handle("GET /api/v1/query/shares", middleware.Chain(http.HandlerFunc(qH.ListShares), requireAuth))
		mux.Handle("POST /api/v1/query/shares", middleware.Chain(http.HandlerFunc(qH.CreateShare), requireAuth))
		mux.Handle("DELETE /api/v1/query/shares/{id}", middleware.Chain(http.HandlerFunc(qH.RevokeShare), requireAuth))
		mux.HandleFunc("GET /api/v1/public/query-shares/{token}", qH.PublicShare)

		deps.Queries = qH
		mux.Handle("POST /api/v1/query/transaction/begin", middleware.Chain(http.HandlerFunc(qH.BeginTransaction), requireAuth))
		mux.Handle("POST /api/v1/query/transaction/commit", middleware.Chain(qH.FinishTransaction(true), requireAuth))
		mux.Handle("POST /api/v1/query/transaction/rollback", middleware.Chain(qH.FinishTransaction(false), requireAuth))
		mux.Handle("GET /api/v1/query/transaction/status", middleware.Chain(http.HandlerFunc(qH.TransactionStatus), requireAuth))
		mux.Handle("GET /api/v1/query/jobs", middleware.Chain(http.HandlerFunc(qH.Jobs), requireAuth))
		mux.Handle("POST /api/v1/query/jobs/{id}/cancel", middleware.Chain(http.HandlerFunc(qH.CancelJob), requireAuth))
		mux.Handle("POST /api/v1/query/execute",
			middleware.Chain(http.HandlerFunc(qH.Execute), requireAuth))
		mux.Handle("GET /api/v1/query/executions/{id}",
			middleware.Chain(http.HandlerFunc(qH.Execution), requireAuth))
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
			middleware.Chain(http.HandlerFunc(qH.DeleteHistory), requireAuth))
		mux.Handle("DELETE /api/v1/query/history",
			middleware.Chain(http.HandlerFunc(qH.ClearHistory), requireAuth))

		mux.Handle("DELETE /api/v1/redis/key", middleware.Chain(qH.RedisMutation("delete"), requireAuth, canWrite))
		mux.Handle("PUT /api/v1/redis/key/ttl", middleware.Chain(qH.RedisMutation("ttl"), requireAuth, canWrite))
		mux.Handle("POST /api/v1/redis/key", middleware.Chain(qH.RedisMutation("create"), requireAuth, canWrite))
		mux.Handle("PUT /api/v1/redis/key/value", middleware.Chain(qH.RedisMutation("value"), requireAuth, canWrite))
		mux.Handle("PUT /api/v1/redis/hash/field", middleware.Chain(qH.RedisMutation("hset"), requireAuth, canWrite))
		mux.Handle("DELETE /api/v1/redis/hash/field", middleware.Chain(qH.RedisMutation("hdel"), requireAuth, canWrite))
		mux.Handle("POST /api/v1/redis/list/push", middleware.Chain(qH.RedisMutation("lpush"), requireAuth, canWrite))
		mux.Handle("POST /api/v1/redis/list/pop", middleware.Chain(qH.RedisMutation("lpop"), requireAuth, canWrite))
		mux.Handle("POST /api/v1/redis/set/member", middleware.Chain(qH.RedisMutation("sadd"), requireAuth, canWrite))
		mux.Handle("DELETE /api/v1/redis/set/member", middleware.Chain(qH.RedisMutation("srem"), requireAuth, canWrite))
		mux.Handle("POST /api/v1/redis/zset/member", middleware.Chain(qH.RedisMutation("zadd"), requireAuth, canWrite))
		mux.Handle("DELETE /api/v1/redis/zset/member", middleware.Chain(qH.RedisMutation("zrem"), requireAuth, canWrite))
		// ---------- 数据资产（字典快照 / 目录 / 标注 / 收藏 / 搜索） ----------
		if deps.Meta != nil {
			aH := assetapi.NewHandler(deps.Conns, deps.Tunnels, deps.Meta,
				deps.UserRepo, deps.History, log, drivers)
			mux.Handle("POST /api/v1/assets/sync",
				middleware.Chain(http.HandlerFunc(aH.Sync), requireAuth, canWrite))
			mux.Handle("GET /api/v1/assets/search",
				middleware.Chain(http.HandlerFunc(aH.Search), requireAuth))
			mux.Handle("GET /api/v1/assets/overview",
				middleware.Chain(http.HandlerFunc(aH.Overview), requireAuth))
			mux.Handle("GET /api/v1/assets/tree",
				middleware.Chain(http.HandlerFunc(aH.Tree), requireAuth))
			mux.Handle("GET /api/v1/assets/tables",
				middleware.Chain(http.HandlerFunc(aH.ListTables), requireAuth))
			mux.Handle("GET /api/v1/assets/table",
				middleware.Chain(http.HandlerFunc(aH.TableDetail), requireAuth))
			mux.Handle("PUT /api/v1/assets/annotations",
				middleware.Chain(http.HandlerFunc(aH.PutAnnotation), requireAuth, canWrite))
			mux.Handle("POST /api/v1/assets/star",
				middleware.Chain(http.HandlerFunc(aH.Star), requireAuth))
			mux.Handle("GET /api/v1/users/brief",
				middleware.Chain(http.HandlerFunc(aH.BriefUsers), requireAuth))
			// 血缘（M5，Step27-28 缓存+实时：sys_meta_lineage 定时 pg_query_go/vitess 解析 sys_query_history）
			var lineageSvc *lineage.Service
			if deps.Lineage != nil {
				lineageSvc = lineage.NewService(deps.Lineage, deps.History, deps.Conns, deps.Meta, log)
			}
			lH := lineageapi.NewHandler(deps.Meta, deps.History, deps.Reports, deps.Dashboards, deps.Lineage, lineageSvc, log, deps.Conns)
			mux.Handle("GET /api/v1/assets/lineage",
				middleware.Chain(http.HandlerFunc(lH.Lineage), requireAuth))
			mux.Handle("POST /api/v1/assets/lineage/refresh",
				middleware.Chain(http.HandlerFunc(lH.Refresh), requireAuth, canWrite))
		}

		dashRepo := deps.Conns.ManagedDashboards()
		dashH := reportapi.NewManagedDashboardHandler(dashRepo)
		mux.Handle("GET /api/v1/managed-dashboards/{id}/current-audience", middleware.Chain(http.HandlerFunc(dashH.CurrentAudience), requireAuth, canWrite))
		mux.Handle("POST /api/v1/managed-dashboards/{id}/audience-changes", middleware.Chain(http.HandlerFunc(dashH.AudienceChange), requireAuth, canWrite))
		mux.Handle("POST /api/v1/managed-dashboards/{id}/access-requests", middleware.Chain(http.HandlerFunc(dashH.RequestAudienceAccess), requireAuth, canWrite))
		mux.Handle("GET /api/v1/managed-dashboards", middleware.Chain(http.HandlerFunc(dashH.List), requireAuth, canWrite))
		mux.Handle("POST /api/v1/managed-dashboards", middleware.Chain(http.HandlerFunc(dashH.Save), requireAuth, canWrite))
		mux.Handle("GET /api/v1/managed-dashboards/{id}", middleware.Chain(http.HandlerFunc(dashH.Get), requireAuth, canWrite))
		mux.Handle("PUT /api/v1/managed-dashboards/{id}/draft", middleware.Chain(http.HandlerFunc(dashH.Save), requireAuth, canWrite))
		mux.Handle("GET /api/v1/dashboard-report-catalog", middleware.Chain(http.HandlerFunc(dashH.Catalog), requireAuth, canWrite))
		mux.Handle("GET /api/v1/managed-dashboards/{id}/versions", middleware.Chain(http.HandlerFunc(dashH.Reviews), requireAuth, canWrite))
		mux.Handle("GET /api/v1/published-dashboards", middleware.Chain(http.HandlerFunc(dashH.Published), requireAuth))
		mux.Handle("GET /api/v1/published-dashboards/{id}", middleware.Chain(http.HandlerFunc(dashH.PublishedGet), requireAuth))
		mux.Handle("GET /api/v1/dashboard-refreshes/{refresh}/tiles/{tile}/receipt", middleware.Chain(qH.DashboardReceipt(dashRepo), requireAuth))
		mux.Handle("POST /api/v1/dashboard-refreshes/{refresh}/tiles/{tile}/execute", middleware.Chain(qH.ExecuteDashboard(dashRepo, deps.Conns.ManagedReports()), requireAuth))
		mux.Handle("POST /api/v1/managed-dashboards/{id}/refreshes", middleware.Chain(http.HandlerFunc(dashH.Refresh), requireAuth))
		for path, action := range map[string]string{"submit": "submit", "adopt-contract": "adopt", "releases": "release", "archive": "archive", "restore": "restore", "delete": "delete", "transfer": "transfer"} {
			mux.Handle("POST /api/v1/managed-dashboards/{id}/"+path, middleware.Chain(dashH.Command(action), requireAuth, canWrite))
		}
		for path, action := range map[string]string{"validate": "validate", "decision": "decision", "reassign": "reassign"} {
			mux.Handle("POST /api/v1/dashboard-reviews/{id}/"+path, middleware.Chain(dashH.Command(action), requireAuth, canWrite))
		}

		// ---------- 报表中心（M3/M4） ----------
		if deps.Reports != nil {
			rH := reportapi.NewHandler(deps.Reports, deps.Conns, log)
			managed := deps.Conns.ManagedReports()
			managedH := reportapi.NewManagedHandler(managed, rH)
			mux.Handle("GET /api/v1/managed-reports/{id}/current-audience", middleware.Chain(http.HandlerFunc(managedH.CurrentAudience), requireAuth, canWrite))
			mux.Handle("GET /api/v1/access-requests", middleware.Chain(http.HandlerFunc(managedH.AccessRequests), requireAuth))
			mux.Handle("POST /api/v1/managed-reports/{id}/audience-changes", middleware.Chain(http.HandlerFunc(managedH.AudienceChange), requireAuth, canWrite))
			for path, action := range map[string]string{"assign": "assign", "reject": "reject", "cancel": "cancel"} {
				mux.Handle("POST /api/v1/access-requests/{id}/"+path, middleware.Chain(managedH.AccessAction(action), requireAuth))
			}
			mux.Handle("POST /api/v1/published-dashboards/{id}/access-requests", middleware.Chain(http.HandlerFunc(dashH.RequestAccess), requireAuth))
			mux.Handle("GET /api/v1/published-reports", middleware.Chain(http.HandlerFunc(managedH.Published), requireAuth))
			mux.Handle("GET /api/v1/published-reports/{id}", middleware.Chain(http.HandlerFunc(managedH.PublishedGet), requireAuth))
			mux.Handle("POST /api/v1/published-reports/{id}/execute", middleware.Chain(qH.ExecutePublished(managed), requireAuth))
			mux.Handle("GET /api/v1/managed-reports", middleware.Chain(http.HandlerFunc(managedH.List), requireAuth, canWrite))
			mux.Handle("POST /api/v1/managed-reports", middleware.Chain(http.HandlerFunc(managedH.SaveDraft), requireAuth, canWrite))
			mux.Handle("GET /api/v1/managed-reports/{id}", middleware.Chain(http.HandlerFunc(managedH.Get), requireAuth, canWrite))
			mux.Handle("PUT /api/v1/managed-reports/{id}/draft", middleware.Chain(http.HandlerFunc(managedH.SaveDraft), requireAuth, canWrite))
			mux.Handle("POST /api/v1/managed-reports/{id}/submit", middleware.Chain(http.HandlerFunc(managedH.Submit), requireAuth, canWrite))
			mux.Handle("GET /api/v1/managed-reports/{id}/dashboard-dependencies", middleware.Chain(http.HandlerFunc(managedH.DashboardDependencies), requireAuth, canWrite))
			mux.Handle("GET /api/v1/managed-reports/{id}/dashboard-impact", middleware.Chain(http.HandlerFunc(managedH.DashboardImpact), requireAuth, canWrite))
			mux.Handle("GET /api/v1/managed-reports/{id}/versions", middleware.Chain(http.HandlerFunc(managedH.Versions), requireAuth, canWrite))
			mux.Handle("GET /api/v1/managed-reports/{id}/releases", middleware.Chain(http.HandlerFunc(managedH.Releases), requireAuth, canWrite))
			mux.Handle("POST /api/v1/managed-reports/{id}/releases", middleware.Chain(http.HandlerFunc(managedH.Release), requireAuth, requireAdmin))
			mux.Handle("POST /api/v1/managed-reports/{id}/transfer", middleware.Chain(http.HandlerFunc(managedH.Transfer), requireAuth, requireAdmin))
			mux.Handle("POST /api/v1/report-reviews/{id}/validate", middleware.Chain(qH.ValidateReportReview(managed), requireAuth, requireAdmin))
			mux.Handle("POST /api/v1/report-reviews/{id}/decision", middleware.Chain(http.HandlerFunc(managedH.Decide), requireAuth, canWrite))
			mux.Handle("GET /api/v1/report-accounts", middleware.Chain(http.HandlerFunc(managedH.Accounts), requireAuth, requireAdmin))
			mux.Handle("POST /api/v1/report-accounts", middleware.Chain(http.HandlerFunc(managedH.CreateAccount), requireAuth, requireAdmin))
			mux.Handle("PATCH /api/v1/report-accounts/{id}", middleware.Chain(http.HandlerFunc(managedH.UpdateAccount), requireAuth, requireAdmin))
			mux.Handle("POST /api/v1/report-accounts/{id}/test", middleware.Chain(qH.TestReportAccount(managed), requireAuth, requireAdmin))
			mux.Handle("POST /api/v1/reports/preview", middleware.Chain(http.HandlerFunc(qH.PreviewReport), requireAuth, canWrite))
			mux.Handle("POST /api/v1/reports/{id}/execute", middleware.Chain(qH.ExecuteReport(deps.Reports), requireAuth))
			mux.Handle("POST /api/v1/reports",
				middleware.Chain(http.HandlerFunc(rH.Create), requireAuth, canWrite))
			mux.Handle("GET /api/v1/reports",
				middleware.Chain(http.HandlerFunc(rH.List), requireAuth))
			mux.Handle("GET /api/v1/reports/{id}",
				middleware.Chain(http.HandlerFunc(rH.Get), requireAuth))
			mux.Handle("PUT /api/v1/reports/{id}",
				middleware.Chain(http.HandlerFunc(rH.Update), requireAuth, canWrite))
			mux.Handle("DELETE /api/v1/reports/{id}",
				middleware.Chain(http.HandlerFunc(rH.Delete), requireAuth, canWrite))
			mux.Handle("POST /api/v1/reports/{id}/star",
				middleware.Chain(http.HandlerFunc(rH.Star), requireAuth))
		}
		if deps.Dashboards != nil && deps.Reports != nil {
			dH := reportapi.NewDashboardHandler(deps.Dashboards, deps.Reports, deps.Conns)
			mux.Handle("POST /api/v1/dashboards",
				middleware.Chain(http.HandlerFunc(dH.Create), requireAuth, canWrite))
			mux.Handle("GET /api/v1/dashboards",
				middleware.Chain(http.HandlerFunc(dH.List), requireAuth))
			mux.Handle("GET /api/v1/dashboards/{id}",
				middleware.Chain(http.HandlerFunc(dH.Get), requireAuth))
			mux.Handle("PUT /api/v1/dashboards/{id}",
				middleware.Chain(http.HandlerFunc(dH.Update), requireAuth, canWrite))
			mux.Handle("DELETE /api/v1/dashboards/{id}",
				middleware.Chain(http.HandlerFunc(dH.Delete), requireAuth, canWrite))
			mux.Handle("POST /api/v1/dashboards/{id}/star",
				middleware.Chain(http.HandlerFunc(dH.Star), requireAuth))
		}
		if deps.Shares != nil && deps.Reports != nil && deps.Dashboards != nil {
			sH := reportapi.NewShareHandler(deps.Shares, deps.Reports, deps.Dashboards, log)
			mux.Handle("POST /api/v1/shares",
				middleware.Chain(http.HandlerFunc(sH.Create), requireAuth, canWrite))
			mux.Handle("GET /api/v1/shares",
				middleware.Chain(http.HandlerFunc(sH.List), requireAuth))
			mux.Handle("POST /api/v1/shares/{id}/revoke",
				middleware.Chain(http.HandlerFunc(sH.Revoke), requireAuth, canWrite))
			mux.Handle("DELETE /api/v1/shares/{id}",
				middleware.Chain(http.HandlerFunc(sH.Delete), requireAuth, canWrite))

			// 免登录公开分享（不走鉴权链）
			pH := reportapi.NewPublicHandler(deps.Shares, deps.Reports, deps.Dashboards, deps.Conns, log)
			mux.HandleFunc("GET /api/v1/public/s/{token}", pH.Get)
			// 前端公开页 /s/:token 由前端路由处理，API 为上述
		}
	} else {
		// 无元数据库（内存模式）下的最小认证路由
		mux.HandleFunc("POST /api/v1/auth/login", authH.Login)
		mux.HandleFunc("POST /api/v1/auth/refresh", authH.Refresh)
		mux.Handle("GET /api/v1/auth/me",
			middleware.Chain(http.HandlerFunc(authH.Me), identityAuth))
	}

	mux.Handle("POST /api/v1/auth/logout", middleware.Chain(http.HandlerFunc(authH.Logout), identityAuth))

	// 未匹配路由统一 404 信封
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		httpx.Fail(w, httpx.NotFound("接口不存在"))
	})

	allowedOrigins := []string{"*"}
	if cfg.IsProduction() {
		// 生产环境通过环境变量 CORS_ALLOWED_ORIGINS 注入白名单（逗号分隔）
		allowedOrigins = cfg.CORSOrigins
	}

	var auditJournal *auditlog.Journal
	if deps != nil {
		auditJournal = deps.AuditJournal
	}
	return workspaceRouting(middleware.Chain(
		mux,
		middleware.RequestID,
		middleware.RequestLogger(log),
		middleware.Audit(auditJournal, log),
		middleware.Recoverer(log),
		middleware.CORS(allowedOrigins),
	))
}
