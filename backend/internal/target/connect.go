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
		return nil, fmt.Errorf("目标数据库连接失败（请检查地址、TLS 与凭据）")
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
		return nil, fmt.Errorf("Redis 连接失败（请检查地址、TLS 与凭据）")
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
		info, e := r.Client.Info(ctx, "server").Result()
		if e != nil {
			return nil, fmt.Errorf("读取 Redis 版本失败")
		}
		return &TestInfo{Type: "redis", Version: infoMap(info)["redis_version"]}, nil
	}
	r, e := OpenRelational(ctx, conn, password, tunnel)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var version string
	if r.pgConn != nil {
		e = r.pgConn.QueryRow(ctx, "SELECT version()").Scan(&version)
	} else {
		e = r.sqlConn.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	}
	if e != nil {
		return nil, fmt.Errorf("读取数据库版本失败")
	}
	return &TestInfo{Type: conn.Type, Version: version}, nil
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
