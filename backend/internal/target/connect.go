package target

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/netx"
)

func OpenRelational(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*RelDB, error) {
	if conn == nil {
		return nil, fmt.Errorf("连接配置为空")
	}
	dial, cleanup, err := netx.Dialer(ctx, conn.Proxy, tunnel)
	if err != nil {
		return nil, err
	}
	r := &RelDB{Conn: conn, Password: password, Tunnel: tunnel, closeTransport: cleanup}
	success := false
	defer func() {
		if !success {
			r.Close()
		}
	}()
	timeout := time.Duration(conn.ConnectionTimeout) * time.Second
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch conn.Type {
	case "postgres":
		u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port)), Path: "/" + firstNonEmpty(conn.Database, "postgres"), User: url.UserPassword(conn.Username, password)}
		q := u.Query()
		mode := "verify-full"
		if conn.SSLMode == "disable" {
			mode = "disable"
		}
		q.Set("sslmode", mode)
		u.RawQuery = q.Encode()
		cfg, e := pgx.ParseConfig(u.String())
		if e != nil {
			return nil, fmt.Errorf("PostgreSQL 连接配置无效")
		}
		cfg.ConnectTimeout = timeout
		cfg.DialFunc = func(c context.Context, network, addr string) (net.Conn, error) { return dial(c, network, addr) }
		if conn.Proxy != nil && conn.Proxy.Type == "db_proxy" && cfg.TLSConfig != nil {
			cfg.TLSConfig.ServerName = conn.Proxy.Host
		}
		r.pgConn, err = pgx.ConnectConfig(ctx, cfg)
	case "mysql":
		cfg := mysql.NewConfig()
		cfg.User = conn.Username
		cfg.Passwd = password
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port))
		cfg.DBName = conn.Database
		cfg.ParseTime = true
		cfg.Timeout = timeout
		cfg.ReadTimeout = 120 * time.Second
		cfg.WriteTimeout = 30 * time.Second
		cfg.Collation = "utf8mb4_unicode_ci"
		if conn.SSLMode != "disable" {
			cfg.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: conn.Host}
			if conn.Proxy != nil && conn.Proxy.Type == "db_proxy" {
				cfg.TLS.ServerName = conn.Proxy.Host
			}
		}
		cfg.DialFunc = func(c context.Context, network, addr string) (net.Conn, error) { return dial(c, network, addr) }
		connector, e := mysql.NewConnector(cfg)
		if e != nil {
			return nil, fmt.Errorf("MySQL 连接配置无效")
		}
		r.sqlDB = sql.OpenDB(connector)
		r.sqlDB.SetMaxOpenConns(1)
		r.sqlDB.SetMaxIdleConns(1)
		r.sqlConn, err = r.sqlDB.Conn(ctx)
		if err == nil {
			err = r.sqlConn.PingContext(ctx)
		}
	default:
		return nil, fmt.Errorf("不支持的关系型数据库类型")
	}
	if err != nil {
		return nil, &connector.Error{Kind: "connection_failed", Message: "目标数据库连接失败（请检查地址、TLS 与凭据）", Cause: err}
	}
	success = true
	return r, nil
}

func OpenRedis(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*RedisClient, error) {
	if conn == nil || conn.Type != "redis" {
		return nil, fmt.Errorf("不是 Redis 连接")
	}
	dial, cleanup, err := netx.Dialer(ctx, conn.Proxy, tunnel)
	if err != nil {
		return nil, err
	}
	number := 0
	if conn.Database != "" {
		number, err = strconv.Atoi(conn.Database)
		if err != nil || number < 0 {
			if cleanup != nil {
				_ = cleanup()
			}
			return nil, fmt.Errorf("Redis 数据库必须为非负整数")
		}
	}
	opt := &redis.Options{Addr: net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port)), Username: conn.Username, Password: password, DB: number, DialTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxRetries: -1}
	opt.Dialer = dial
	if conn.SSLMode != "disable" {
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: conn.Host}
		opt.Dialer = func(c context.Context, network, addr string) (net.Conn, error) {
			raw, e := dial(c, network, addr)
			if e != nil {
				return nil, e
			}
			secure := tls.Client(raw, opt.TLSConfig)
			if e = secure.HandshakeContext(c); e != nil {
				_ = raw.Close()
				return nil, e
			}
			return secure, nil
		}
	}
	cli := redis.NewClient(opt)
	r := &RedisClient{Client: cli, Conn: conn, closeTransport: cleanup}
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = cli.Ping(check).Err(); err != nil {
		r.Close()
		return nil, &connector.Error{Kind: "connection_failed", Message: "Redis 连接失败（请检查地址、TLS 与凭据）", Cause: err}
	}
	return r, nil
}

func Test(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*TestInfo, error) {
	if conn == nil {
		return nil, fmt.Errorf("连接配置为空")
	}
	if conn.Type == "redis" {
		r, e := OpenRedis(ctx, conn, password, tunnel)
		if e != nil {
			return nil, e
		}
		defer r.Close()
		return ProbeRedis(ctx, r)
	}
	r, e := OpenRelational(ctx, conn, password, tunnel)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return ProbeRelational(ctx, r)
}

// ProbeRedis reads identity on the already-open connection.
func ProbeRedis(ctx context.Context, r *RedisClient) (*TestInfo, error) {
	info, e := r.Client.Info(ctx, "server").Result()
	if e != nil {
		if strings.HasPrefix(e.Error(), "NOPERM ") {
			return &TestInfo{Type: "redis", Compatibility: Compatibility{Engine: "redis", Product: "Redis", Status: "unknown", Message: "连接已建立，但账号无权读取版本信息；兼容性尚未确认。"}}, nil
		}
		return nil, &connector.Error{Kind: "connection_failed", Message: "读取 Redis 版本失败", Cause: e}
	}
	server := infoMap(info)
	version, product := server["redis_version"], ""
	for _, fork := range []string{"valkey", "keydb", "dragonfly"} {
		if server[fork+"_version"] != "" {
			version, product = server[fork+"_version"], fork
			break
		}
	}
	return &TestInfo{Type: "redis", Version: server["redis_version"], Compatibility: classifyCompatibility("redis", version, product, server["redis_mode"])}, nil
}

// ProbeRelational reads identity without opening a second physical connection.
func ProbeRelational(ctx context.Context, r *RelDB) (*TestInfo, error) {
	var e error
	var version, comment string
	if r.pgConn != nil {
		e = r.pgConn.QueryRow(ctx, "SELECT version()").Scan(&version)
	} else {
		e = r.sqlConn.QueryRowContext(ctx, "SELECT VERSION(), @@version_comment").Scan(&version, &comment)
	}
	if e != nil {
		return nil, &connector.Error{Kind: "connection_failed", Message: "读取数据库版本失败", Cause: e}
	}
	return &TestInfo{Type: r.Conn.Type, Version: version, Compatibility: classifyCompatibility(r.Conn.Type, version, comment, "")}, nil
}
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func IsUserDatabase(typ, name string) bool {
	n := strings.ToLower(name)
	if typ == "mysql" {
		return n != "mysql" && n != "information_schema" && n != "performance_schema" && n != "sys"
	}
	return n != "template0" && n != "template1"
}
func IsUserSchema(typ, name string) bool {
	return typ != "postgres" || (!strings.HasPrefix(name, "pg_") && name != "information_schema")
}
