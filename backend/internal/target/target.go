// Package target 封装对受管目标数据库（MySQL / PostgreSQL / Redis）的
// 连接建立、连通性测试、SQL 执行与元数据浏览能力，是 HTTP 层与各驱动之间
// 的统一门面。所有连接均为短生命周期的请求级连接，支持经 SSH 隧道拨号。
package target

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/sshx"
)

// 默认端口。
const (
	defaultMySQLPort    = 3306
	defaultPostgresPort = 5432
	defaultRedisPort    = 6379
)

// RelDB 已打开的关系型数据库句柄（请求级，调用方必须 Close）。
type RelDB struct {
	Kind     string // mysql | postgres
	Database string // 当前连接所在库（已解析默认值）
	db       *sql.DB
	closers  []func() error
}

// Close 释放数据库连接及其依赖资源（如 SSH 客户端）。
func (r *RelDB) Close() {
	if r == nil {
		return
	}
	if r.db != nil {
		_ = r.db.Close()
	}
	// 逆序释放：先连接后隧道。
	for i := len(r.closers) - 1; i >= 0; i-- {
		_ = r.closers[i]()
	}
}

// tunnelDialer 建立 SSH 客户端并返回拨号函数与关闭函数。
func tunnelDialer(ctx context.Context, tunnel *db.SSHTunnel) (func(ctx context.Context, network, addr string) (net.Conn, error), func() error, error) {
	if tunnel == nil {
		return nil, nil, nil
	}
	client, err := sshx.DialClient(ctx, tunnel)
	if err != nil {
		return nil, nil, err
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return client.DialContext(ctx, network, addr)
	}
	return dial, client.Close, nil
}

// OpenRelational 打开一个关系型目标数据库连接。
func OpenRelational(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*RelDB, error) {
	dial, closeTunnel, err := tunnelDialer(ctx, tunnel)
	if err != nil {
		return nil, err
	}
	handle := &RelDB{Kind: conn.Type}
	if closeTunnel != nil {
		handle.closers = append(handle.closers, closeTunnel)
	}
	timeout := time.Duration(conn.ConnectionTimeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	switch conn.Type {
	case "mysql":
		if conn.Port == 0 {
			conn.Port = defaultMySQLPort
		}
		cfg := mysql.NewConfig()
		cfg.User = conn.Username
		cfg.Passwd = password
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port))
		cfg.DBName = conn.Database
		cfg.Timeout = timeout
		cfg.ReadTimeout = 20 * time.Second
		cfg.WriteTimeout = 20 * time.Second
		cfg.ParseTime = true
		cfg.Params = map[string]string{"charset": "utf8mb4"}
		if dial != nil {
			name := registerMySQLDial(dial)
			cfg.Net = name
			handle.closers = append(handle.closers, func() error { mysql.DeregisterDialContext(name); return nil })
		}
		if conn.SSLMode == "require" || conn.SSLMode == "verify-ca" || conn.SSLMode == "verify-full" {
			cfg.TLSConfig = mysqlTLSName(conn)
		}
		dbh, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			handle.Close()
			return nil, err
		}
		dbh.SetConnMaxLifetime(3 * time.Minute)
		handle.db = dbh
	case "postgres":
		if conn.Port == 0 {
			conn.Port = defaultPostgresPort
		}
		sslMode := conn.SSLMode
		if sslMode == "" {
			sslMode = "disable"
		}
		dbName := conn.Database
		if dbName == "" {
			dbName = "postgres"
		}
		// 使用 URL 形式 DSN，对口令/库名做百分号编码，避免特殊字符破坏解析。
		u := &url.URL{
			Scheme:   "postgres",
			Host:     net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port)),
			Path:     "/" + dbName,
			RawQuery: fmt.Sprintf("sslmode=%s&connect_timeout=%d", sslMode, int(timeout.Seconds())),
		}
		if conn.Username != "" {
			u.User = url.UserPassword(conn.Username, password)
		}
		pgCfg, err := pgx.ParseConfig(u.String())
		if err != nil {
			handle.Close()
			return nil, err
		}
		if dial != nil {
			pgCfg.DialFunc = dial
		}
		poolCfg, err := pgxpool.ParseConfig("")
		if err != nil {
			handle.Close()
			return nil, err
		}
		poolCfg.ConnConfig = pgCfg
		poolCfg.MaxConns = 4
		poolCfg.MaxConnLifetime = 3 * time.Minute
		pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
		if err != nil {
			handle.Close()
			return nil, err
		}
		handle.db = stdlib.OpenDBFromPool(pool)
		handle.closers = append(handle.closers, func() error { pool.Close(); return nil })
	default:
		handle.Close()
		return nil, fmt.Errorf("不支持的关系型数据库类型: %s", conn.Type)
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	if err := handle.db.PingContext(pingCtx); err != nil {
		handle.Close()
		return nil, err
	}
	handle.Database = currentDatabase(ctx, handle)
	return handle, nil
}

// RedisClient 已打开的 Redis 客户端（请求级，调用方必须 Close）。
type RedisClient struct {
	Client *redis.Client
	closer func() error
}

// Close 释放 Redis 连接与 SSH 隧道。
func (c *RedisClient) Close() {
	if c == nil {
		return
	}
	if c.Client != nil {
		_ = c.Client.Close()
	}
	if c.closer != nil {
		_ = c.closer()
	}
}

// OpenRedis 打开一个 Redis 目标连接。
func OpenRedis(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*RedisClient, error) {
	if conn.Port == 0 {
		conn.Port = defaultRedisPort
	}
	dbIndex := 0
	if conn.Database != "" {
		if n, err := strconv.Atoi(conn.Database); err == nil && n >= 0 {
			dbIndex = n
		}
	}
	dial, closeTunnel, err := tunnelDialer(ctx, tunnel)
	if err != nil {
		return nil, err
	}
	opts := &redis.Options{
		Addr:         net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port)),
		Username:     conn.Username,
		Password:     password,
		DB:           dbIndex,
		DialTimeout:  10 * time.Second,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	if dial != nil {
		opts.Dialer = dial
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		if closeTunnel != nil {
			_ = closeTunnel()
		}
		return nil, err
	}
	return &RedisClient{Client: client, closer: closeTunnel}, nil
}

// mysqlDialSeq 为每次隧道连接生成唯一拨号器名，避免并发互相覆盖。
var mysqlDialSeq atomic.Int64

// TestInfo 连通性测试结果。
type TestInfo struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}
