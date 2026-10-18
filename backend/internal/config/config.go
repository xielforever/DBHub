// Package config 负责从环境变量加载并校验应用配置。
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config 应用运行期配置，全部由环境变量注入。
type Config struct {
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
	// MetaDBMaxConns 元数据库连接池上限（单会话线协议代理应设为 1）
	MetaDBMaxConns int32
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
		JDBCURL: os.Getenv("DBHUB_JDBC_URL"), JDBCToken: os.Getenv("DBHUB_JDBC_TOKEN"),
		Env:             getenv("APP_ENV", "development"),
		Port:            getenv("PORT", "8080"),
		BindHost:        os.Getenv("HTTP_BIND_HOST"),
		LogLevel:        getenv("LOG_LEVEL", "info"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		MetaDBMaxConns:  int32(getenvInt("META_DB_MAX_CONNS", 10)),
		RedisURL:        os.Getenv("REDIS_URL"),
		AuditDir:        getenv("DBHUB_AUDIT_DIR", ".dbhub/audit"),
		AuditMaxPending: getenvInt("DBHUB_AUDIT_MAX_PENDING", 10000),
		AIBaseURL:       getenv("DBHUB_AI_BASE_URL", "https://api.openai.com/v1"), AIKey: os.Getenv("DBHUB_AI_API_KEY"), AIModel: getenv("DBHUB_AI_MODEL", "gpt-4.1-mini"),
		Admin: AdminConfig{
			Username: getenv("APP_ADMIN_USERNAME", "admin"),
			Password: getenv("APP_ADMIN_PASSWORD", "admin123"),
		},
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
		rules := strings.TrimSpace(os.Getenv("DBHUB_TARGET_ALLOWLIST"))
		if rules == "" {
			return nil, errors.New("生产环境必须配置 DBHUB_TARGET_ALLOWLIST")
		}
		for _, rule := range strings.Split(rules, ",") {
			host, port, err := net.SplitHostPort(strings.TrimSpace(rule))
			number, parseErr := strconv.Atoi(port)
			if err != nil || parseErr != nil || host == "" || strings.ContainsAny(host, "/\\@?#;=\r\n\t ") || number < 1 || number > 65535 {
				return nil, errors.New("DBHUB_TARGET_ALLOWLIST 必须为主机:端口列表")
			}
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
