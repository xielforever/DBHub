// Package config 负责从环境变量加载并校验应用配置。
package config

import (
	"errors"
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
	for _, origin := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if strings.TrimSpace(origin) != "" {
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
