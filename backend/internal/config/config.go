// Package config 负责从环境变量加载并校验应用配置。
package config

import (
	"errors"
	"os"
	"strconv"
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
	// RedisURL Redis 缓存 DSN
	RedisURL string
}

// AdminConfig 引导管理员账号配置。
type AdminConfig struct {
	Username string
	Password string
}

// Load 从环境变量读取配置并执行必要的校验。
func Load() (*Config, error) {
	cfg := &Config{
		Env:         getenv("APP_ENV", "development"),
		Port:        getenv("PORT", "8080"),
		LogLevel:    getenv("LOG_LEVEL", "info"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		Admin: AdminConfig{
			Username: getenv("APP_ADMIN_USERNAME", "admin"),
			Password: getenv("APP_ADMIN_PASSWORD", "admin123"),
		},
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
