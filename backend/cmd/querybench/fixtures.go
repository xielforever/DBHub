package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

type actor struct {
	id    int64
	token string
}
type target struct {
	id                             int64
	kind, table, database, version string
}
type fixtures struct {
	c       *apiClient
	marker  string
	actors  []actor
	targets []target
	pg      *pgx.Conn
	my      *sql.DB
}
type databaseConnections struct {
	Total  int `json:"total"`
	Active int `json:"active"`
}

func (f *fixtures) execute(ctx context.Context, t target, query string, params map[string]any) error {
	var result struct {
		Kind string `json:"kind"`
	}
	return f.c.control(ctx, "POST", "/query/execute", map[string]any{"request_id": newID(), "connection_id": t.id, "database": t.database, "sql": query, "params": params}, &result)
}
func (f *fixtures) prepare(ctx context.Context, rows int) error {
	f.marker = "dbhub_bench_" + nonce()
	password := nonce() + "!Aa9"
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("%s_u%d", f.marker, i)
		var user struct {
			ID int64 `json:"id"`
		}
		if err := f.c.control(ctx, "POST", "/users", map[string]any{"username": name, "password": password, "role": "developer"}, &user); err != nil {
			return err
		}
		f.actors = append(f.actors, actor{id: user.ID})
		token, err := f.c.login(ctx, name, password)
		if err != nil {
			return err
		}
		f.actors[len(f.actors)-1].token = token
	}
	pgConfig, err := pgx.ParseConfig(os.Getenv("TEST_POSTGRES_DSN"))
	if err != nil {
		return fmt.Errorf("缺少有效的 PostgreSQL 测试配置")
	}
	myConfig, err := mysql.ParseDSN(os.Getenv("TEST_MYSQL_DSN"))
	if err != nil || myConfig.DBName == "" {
		return fmt.Errorf("缺少有效的 MySQL 测试配置")
	}
	myHost, myPortRaw, err := net.SplitHostPort(myConfig.Addr)
	if err != nil {
		return fmt.Errorf("MySQL 测试地址无效")
	}
	myPort, err := strconv.Atoi(myPortRaw)
	if err != nil {
		return fmt.Errorf("MySQL 测试端口无效")
	}
	for _, spec := range []struct {
		kind, host, db, user, password string
		port                           int
	}{
		{"postgres", pgConfig.Host, pgConfig.Database, pgConfig.User, pgConfig.Password, int(pgConfig.Port)},
		{"mysql", myHost, myConfig.DBName, myConfig.User, myConfig.Passwd, myPort},
	} {
		var conn struct {
			ID int64 `json:"id"`
		}
		if err := f.c.control(ctx, "POST", "/connections", map[string]any{"name": f.marker + "_" + spec.kind, "type": spec.kind, "host": spec.host, "port": spec.port, "database": spec.db, "username": spec.user, "password": spec.password, "ssl_mode": "disable", "environment": "dev", "connection_timeout": 5}, &conn); err != nil {
			return err
		}
		t := target{id: conn.ID, kind: spec.kind, table: f.marker, database: spec.db}
		f.targets = append(f.targets, t)
		if err := f.execute(ctx, t, "CREATE TABLE "+t.table+" (id INT PRIMARY KEY, amount INT NOT NULL, label VARCHAR(32) NOT NULL)", nil); err != nil {
			return err
		}
		if t.kind == "postgres" {
			if err := f.execute(ctx, t, "INSERT INTO "+t.table+" SELECT n,(n*3)%1000,'row-'||lpad(n::text,6,'0') FROM generate_series(1,CAST(:rows AS INTEGER)) AS n", map[string]any{"rows": rows}); err != nil {
				return err
			}
		} else {
			for start := 1; start <= rows; start += 250 {
				params := map[string]any{}
				values := []string{}
				for id := start; id < start+250 && id <= rows; id++ {
					key := strconv.Itoa(id)
					values = append(values, "(:i"+key+",:a"+key+",:l"+key+")")
					params["i"+key] = id
					params["a"+key] = (id * 3) % 1000
					params["l"+key] = fmt.Sprintf("row-%06d", id)
				}
				if err := f.execute(ctx, t, "INSERT INTO "+t.table+" VALUES "+strings.Join(values, ","), params); err != nil {
					return err
				}
			}
		}
		var result struct {
			Rows [][]string `json:"rows"`
		}
		if err := f.c.control(ctx, "POST", "/query/execute", map[string]any{"request_id": newID(), "connection_id": t.id, "database": t.database, "sql": "SELECT VERSION()"}, &result); err != nil {
			return err
		}
		if len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
			return fmt.Errorf("数据库版本响应无效")
		}
		f.targets[len(f.targets)-1].version = result.Rows[0][0]
	}
	pgConfig.RuntimeParams["application_name"] = "dbhub_querybench_observer"
	f.pg, err = pgx.ConnectConfig(ctx, pgConfig)
	if err != nil {
		return fmt.Errorf("PostgreSQL 连接观测器不可用")
	}
	f.my, err = sql.Open("mysql", os.Getenv("TEST_MYSQL_DSN"))
	if err != nil {
		return fmt.Errorf("MySQL 连接观测器不可用")
	}
	f.my.SetMaxOpenConns(1)
	f.my.SetMaxIdleConns(1)
	if f.my.PingContext(ctx) != nil {
		return fmt.Errorf("MySQL 连接观测器不可用")
	}
	return nil
}
func (f *fixtures) connections(ctx context.Context) (map[string]databaseConnections, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	counts := map[string]databaseConnections{}
	var pgCount databaseConnections
	if err := f.pg.QueryRow(ctx, "SELECT COUNT(*), COUNT(*) FILTER(WHERE state='active') FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend'").Scan(&pgCount.Total, &pgCount.Active); err != nil {
		return nil, fmt.Errorf("PostgreSQL 连接采样失败")
	}
	counts["postgres"] = pgCount
	rows, err := f.my.QueryContext(ctx, "SHOW GLOBAL STATUS WHERE Variable_name IN ('Threads_connected','Threads_running')")
	if err != nil {
		return nil, fmt.Errorf("MySQL 连接采样失败")
	}
	defer rows.Close()
	var myCount databaseConnections
	for rows.Next() {
		var key string
		var value int
		if rows.Scan(&key, &value) != nil {
			return nil, fmt.Errorf("MySQL 连接指标无效")
		}
		if key == "Threads_connected" {
			myCount.Total = value
		} else if key == "Threads_running" {
			myCount.Active = value
		}
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("MySQL 连接指标读取失败")
	}
	counts["mysql"] = myCount
	return counts, nil
}
func (f *fixtures) cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	failed := false
	if f.pg != nil {
		_ = f.pg.Close(ctx)
	}
	if f.my != nil {
		_ = f.my.Close()
	}
	for _, t := range f.targets {
		if err := f.execute(ctx, t, "DROP TABLE IF EXISTS "+t.table, nil); err != nil {
			failed = true
		}
		if err := f.c.control(ctx, "DELETE", fmt.Sprintf("/connections/%d", t.id), nil, nil); err != nil {
			failed = true
		}
	}
	for _, a := range f.actors {
		if a.token != "" {
			r := f.c.call(ctx, "POST", "/api/v1/auth/logout", a.token, nil)
			if r.err != nil || r.status != 200 {
				failed = true
			}
		}
		if err := f.c.control(ctx, "DELETE", fmt.Sprintf("/users/%d", a.id), nil, nil); err != nil {
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("部分临时测试对象清理失败，请检查该次压测的临时对象")
	}
	return nil
}
func numeric(raw json.RawMessage) (int, error) {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strconv.Atoi(text)
	}
	return 0, fmt.Errorf("数值结果无效")
}
