// Package config 负责从环境变量加载并校验应用配置。
package config

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/targetallowlist"
)

// Config 应用运行期配置，全部由环境变量注入。
type Config struct {
	// Empty by default. Activation additionally requires an owner-provisioned
	// database isolation marker, current fleet proof and independent approval.
	IAMIsolationID string
	// Phase2ReportVerification opts into policy verification and explicitly bound reports in an isolated database.
	Phase2ReportVerification bool
	// Env 运行环境：development / staging / production
	Env string
	// Port HTTP 监听端口
	Port string
	// BindHost is empty for all interfaces; source deployments use 127.0.0.1.
	BindHost string
	// LogLevel 日志级别：debug / info / warn / error
	LogLevel string

	// SecretKey JWT 签名与敏感字段加密的主密钥（>= 32 字节）
	SecretKey []byte

	// AccessTTL Access Token 有效期（默认 15 分钟）
	AccessTTL time.Duration
	// RefreshTTL Refresh Token 有效期（默认 7 天）
	RefreshTTL time.Duration

	// Admin 开发期引导管理员账号（后续接入元数据库后改为迁移种子）
	Admin AdminConfig

	// DatabaseURL PostgreSQL 元数据库 DSN
	DatabaseURL string
	// Nacos 配置源参数；启用后白名单键优先于环境变量。
	Nacos NacosOptions
	// NacosInitialHash 首次读取的配置内容哈希，用于监听变化。
	NacosInitialHash string
	// NacosLoadError 记录 REQUIRED=false 时首次读取失败的原因（不含凭据）。
	NacosLoadError string
	// MetaDBMaxConns 元数据库连接池上限（单会话线协议代理应设为 1）
	MetaDBMaxConns int32
	// MigrationTimeout 启动迁移及运行账号初始化总时限。
	MigrationTimeout              time.Duration
	ReadinessTimeout              time.Duration
	RedisProbeTimeout             time.Duration
	LoginRedisTimeout             time.Duration
	ConnectionCheckPersistTimeout time.Duration
	// RedisURL Redis 缓存 DSN
	RedisURL                  string
	CORSOrigins               []string
	AIBaseURL, AIKey, AIModel string
	JDBCURL, JDBCToken        string
	AuditDir                  string
	AuditMaxPending           int
	LoginMaxAttempts          int
	LoginIPMaxAttempts        int
	LoginWindow               time.Duration
	TrustedProxyCIDRs         []netip.Prefix
	Operations                OperationsConfig
	OIDC                      OIDCConfig
	NotificationWebhook       NotificationWebhookConfig
}

type OperationsConfig struct {
	AuditRetentionDays            int `json:"audit_retention_days"`
	HistoryRetentionDays          int `json:"history_retention_days"`
	ReadNotificationRetentionDays int `json:"read_notification_retention_days"`
	BackupExpectedHours           int `json:"backup_expected_hours"`
}

// AdminConfig 引导管理员账号配置。
type AdminConfig struct {
	Username string
	Password string
}

// Load 从环境变量读取配置并执行必要的校验。
func Load() (*Config, error) {
	cfg := &Config{
		IAMIsolationID: os.Getenv("DBHUB_IAM_ISOLATION_ID"),
		JDBCURL:        os.Getenv("DBHUB_JDBC_URL"), JDBCToken: os.Getenv("DBHUB_JDBC_TOKEN"),
		Env:                           getenv("APP_ENV", "development"),
		Port:                          getenv("PORT", "8080"),
		BindHost:                      os.Getenv("HTTP_BIND_HOST"),
		LogLevel:                      getenv("LOG_LEVEL", "info"),
		DatabaseURL:                   os.Getenv("DATABASE_URL"),
		MetaDBMaxConns:                int32(getenvInt("META_DB_MAX_CONNS", 10)),
		MigrationTimeout:              30 * time.Second,
		ReadinessTimeout:              2 * time.Second,
		RedisProbeTimeout:             500 * time.Millisecond,
		LoginRedisTimeout:             500 * time.Millisecond,
		ConnectionCheckPersistTimeout: 3 * time.Second,
		RedisURL:                      os.Getenv("REDIS_URL"),
		AuditDir:                      getenv("DBHUB_AUDIT_DIR", ".dbhub/audit"),
		AuditMaxPending:               getenvInt("DBHUB_AUDIT_MAX_PENDING", 10000),
		AIBaseURL:                     getenv("DBHUB_AI_BASE_URL", "https://api.openai.com/v1"), AIKey: os.Getenv("DBHUB_AI_API_KEY"), AIModel: getenv("DBHUB_AI_MODEL", "gpt-4.1-mini"),
		Admin: AdminConfig{
			Username: getenv("APP_ADMIN_USERNAME", "admin"),
			Password: getenv("APP_ADMIN_PASSWORD", "admin123"),
		},
	}
	nacosOpts, err := loadNacosOptions()
	if err != nil {
		return nil, err
	}
	cfg.Nacos = nacosOpts
	if nacosOpts.Enabled {
		ctx, cancel := context.WithTimeout(context.Background(), nacosOpts.Timeout*2)
		values, hash, fetchErr := FetchNacosValues(ctx, nacosOpts)
		cancel()
		if fetchErr != nil {
			if nacosOpts.Required {
				return nil, fmt.Errorf("读取 Nacos 配置失败: %w", fetchErr)
			}
			cfg.NacosLoadError = fetchErr.Error()
		} else {
			if v := values["DATABASE_URL"]; v != "" {
				cfg.DatabaseURL = v
			}
			if v := values["REDIS_URL"]; v != "" {
				cfg.RedisURL = v
			}
			cfg.NacosInitialHash = hash
		}
	}
	if raw := os.Getenv("DBHUB_MIGRATION_TIMEOUT_SECONDS"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds < 1 || seconds > 600 {
			return nil, errors.New("DBHUB_MIGRATION_TIMEOUT_SECONDS 必须为1到600的整数")
		}
		cfg.MigrationTimeout = time.Duration(seconds) * time.Second
	}
	for _, setting := range []struct {
		key  string
		max  int
		unit time.Duration
		dest *time.Duration
	}{
		{"DBHUB_READINESS_TIMEOUT_SECONDS", 60, time.Second, &cfg.ReadinessTimeout},
		{"DBHUB_REDIS_PROBE_TIMEOUT_MS", 10000, time.Millisecond, &cfg.RedisProbeTimeout},
		{"DBHUB_LOGIN_REDIS_TIMEOUT_MS", 10000, time.Millisecond, &cfg.LoginRedisTimeout},
		{"DBHUB_CONNECTION_CHECK_PERSIST_TIMEOUT_SECONDS", 60, time.Second, &cfg.ConnectionCheckPersistTimeout},
	} {
		if raw := os.Getenv(setting.key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > setting.max {
				return nil, fmt.Errorf("%s 必须为1到%d的整数", setting.key, setting.max)
			}
			*setting.dest = time.Duration(value) * setting.unit
		}
	}
	if cfg.IAMIsolationID != "" && cfg.Env != "development" {
		return nil, errors.New("隔离权限切换仅允许 development 验收部署")
	}
	if raw := os.Getenv("DBHUB_PHASE2_REPORT_VERIFICATION"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil || (enabled && (cfg.Env != "development" || strings.TrimSpace(cfg.IAMIsolationID) == "")) {
			return nil, errors.New("阶段2报表验证需要布尔开关、development 环境和隔离登记标识")
		}
		cfg.Phase2ReportVerification = enabled
	}
	if cfg.BindHost != "" && net.ParseIP(cfg.BindHost) == nil {
		return nil, errors.New("HTTP_BIND_HOST 必须为空或有效 IP 地址")
	}
	if raw := os.Getenv("DBHUB_AUDIT_MAX_PENDING"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, errors.New("DBHUB_AUDIT_MAX_PENDING 必须是整数")
		}
		cfg.AuditMaxPending = value
	}
	if cfg.AuditMaxPending < 1 || cfg.AuditMaxPending > 100000 {
		return nil, errors.New("DBHUB_AUDIT_MAX_PENDING 必须在 1–100000 之间")
	}
	for _, field := range []struct {
		name               string
		fallback, min, max int
		value              *int
	}{
		{"DBHUB_AUDIT_RETENTION_DAYS", 0, 30, 3650, &cfg.Operations.AuditRetentionDays},
		{"DBHUB_HISTORY_RETENTION_DAYS", 0, 7, 3650, &cfg.Operations.HistoryRetentionDays},
		{"DBHUB_READ_NOTIFICATION_RETENTION_DAYS", 90, 7, 3650, &cfg.Operations.ReadNotificationRetentionDays},
		{"DBHUB_BACKUP_EXPECTED_HOURS", 0, 1, 168, &cfg.Operations.BackupExpectedHours},
	} {
		v, e := strconv.Atoi(getenv(field.name, strconv.Itoa(field.fallback)))
		if e != nil || v < 0 || v > field.max || (v > 0 && v < field.min) {
			return nil, fmt.Errorf("%s 必须为 0（关闭）或 %d–%d", field.name, field.min, field.max)
		}
		*field.value = v
	}
	if cfg.IsProduction() && !filepath.IsAbs(os.Getenv("DBHUB_AUDIT_DIR")) {
		return nil, errors.New("生产环境必须配置绝对路径 DBHUB_AUDIT_DIR，并挂载持久化存储")
	}

	if cfg.JDBCURL != "" && len(cfg.JDBCToken) < 32 {
		return nil, errors.New("配置 JDBC 服务时必须设置至少 32 字符的 DBHUB_JDBC_TOKEN")
	}
	secret := os.Getenv("APP_SECRET_KEY")
	if secret == "" {
		if cfg.IsProduction() {
			return nil, errors.New("生产环境必须通过 APP_SECRET_KEY 注入主密钥")
		}
		// 开发环境兜底固定密钥，仅用于本地调试
		secret = "dev-only-dbhub-secret-key-please-change-32b"
	}
	if len(secret) < 32 {
		return nil, errors.New("APP_SECRET_KEY 长度不能小于 32 字节")
	}
	cfg.SecretKey = []byte(secret)

	if cfg.IsProduction() && cfg.Admin.Password == "admin123" {
		return nil, errors.New("生产环境禁止使用默认管理员密码，请通过 APP_ADMIN_PASSWORD 注入强密码")
	}

	accessMinutes := getenvInt("JWT_ACCESS_TTL_MINUTES", 15)
	refreshHours := getenvInt("JWT_REFRESH_TTL_HOURS", 24*7)
	if accessMinutes <= 0 || refreshHours <= 0 {
		return nil, errors.New("JWT 有效期配置必须为正整数")
	}
	cfg.AccessTTL = time.Duration(accessMinutes) * time.Minute
	cfg.RefreshTTL = time.Duration(refreshHours) * time.Hour

	if cfg.IsProduction() && cfg.DatabaseURL == "" {
		return nil, errors.New("生产环境必须配置 DATABASE_URL")
	}
	if cfg.IsProduction() && cfg.RedisURL == "" {
		return nil, errors.New("生产环境必须配置 REDIS_URL")
	}
	for _, field := range []struct {
		name          string
		fallback, max int
		value         *int
	}{
		{"DBHUB_LOGIN_MAX_ATTEMPTS", 10, 1000, &cfg.LoginMaxAttempts},
		{"DBHUB_LOGIN_IP_MAX_ATTEMPTS", 60, 10000, &cfg.LoginIPMaxAttempts},
	} {
		value, err := strconv.Atoi(getenv(field.name, strconv.Itoa(field.fallback)))
		if err != nil || value < 1 || value > field.max {
			return nil, fmt.Errorf("%s 必须在 1–%d 之间", field.name, field.max)
		}
		*field.value = value
	}
	window, err := strconv.Atoi(getenv("DBHUB_LOGIN_WINDOW_SECONDS", "300"))
	if err != nil || window < 10 || window > 3600 {
		return nil, errors.New("DBHUB_LOGIN_WINDOW_SECONDS 必须在 10–3600 之间")
	}
	cfg.LoginWindow = time.Duration(window) * time.Second
	for _, raw := range strings.Split(os.Getenv("DBHUB_TRUSTED_PROXY_CIDRS"), ",") {
		if raw = strings.TrimSpace(raw); raw != "" {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				return nil, errors.New("DBHUB_TRUSTED_PROXY_CIDRS 必须为有效 CIDR 列表")
			}
			cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, prefix.Masked())
		}
	}
	if cfg.IsProduction() {
		rules := strings.TrimSpace(os.Getenv(targetallowlist.Env))
		if rules == "" {
			return nil, errors.New("生产环境必须配置 DBHUB_TARGET_ALLOWLIST")
		}
		if _, err := targetallowlist.Parse(rules); err != nil {
			return nil, fmt.Errorf("%s %w", targetallowlist.Env, err)
		}
	}
	for _, origin := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if strings.TrimSpace(origin) != "" {
			if cfg.IsProduction() && strings.TrimSpace(origin) == "*" {
				return nil, errors.New("生产环境 CORS 不允许通配来源")
			}
			cfg.CORSOrigins = append(cfg.CORSOrigins, strings.TrimSpace(origin))
		}
	}
	cfg.OIDC = OIDCConfig{Issuer: os.Getenv("DBHUB_OIDC_ISSUER"), ClientID: os.Getenv("DBHUB_OIDC_CLIENT_ID"), ClientSecret: os.Getenv("DBHUB_OIDC_CLIENT_SECRET"), CallbackURL: os.Getenv("DBHUB_OIDC_CALLBACK_URL")}
	if err := ValidateOIDC(cfg.OIDC, !cfg.IsProduction()); err != nil {
		return nil, err
	}
	cfg.NotificationWebhook = NotificationWebhookConfig{URL: os.Getenv("DBHUB_NOTIFICATION_WEBHOOK_URL"), Secret: os.Getenv("DBHUB_NOTIFICATION_WEBHOOK_SECRET")}
	if err := ValidateNotificationWebhook(cfg.NotificationWebhook, !cfg.IsProduction()); err != nil {
		return nil, err
	}
	return cfg, nil
}

// IsProduction 判断是否为生产环境。
func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
