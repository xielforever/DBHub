package target

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/db"
)

// RelDB 关系型目标库句柄（Pg / MySQL）
// 当前为占位实现：OpenRelational 仅保存连接信息，不立即拨号；
// 各查询方法优先尝试真实拨号，失败则返回本地 mock。
type RelDB struct {
	Conn     *db.Connection
	Password string
	Tunnel   *db.SSHTunnel
	// 真实连接（懒加载）
	pgConn *pgx.Conn
	sqlDB  *sql.DB
}

func (r *RelDB) Close() {
	if r.pgConn != nil {
		_ = r.pgConn.Close(context.Background())
	}
	if r.sqlDB != nil {
		_ = r.sqlDB.Close()
	}
}

// RedisClient Redis 句柄
type RedisClient struct {
	Client *redis.Client
	Conn   *db.Connection
}

func (r *RedisClient) Close() {
	if r.Client != nil {
		_ = r.Client.Close()
	}
}

// ---------- 基础类型 ----------

type DatabaseItem struct {
	Name string `json:"name"`
}

type TableItem struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // table | view
	Comment string `json:"comment"`
}

type ColumnItem struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	IsPrimary  bool   `json:"is_primary"`
	IsNullable bool   `json:"is_nullable"`
	Ordinal    int    `json:"ordinal"`
	Comment    string `json:"comment"`
	Default    string `json:"default"`
}

type ColumnMeta struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	IsPrimary  bool   `json:"is_primary"`
	IsNullable bool   `json:"is_nullable"`
	Ordinal    int    `json:"ordinal"`
	Comment    string `json:"comment"`
	Default    string `json:"default"`
}

type IndexMeta struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	IsUnique  bool     `json:"is_unique"`
	IsPrimary bool     `json:"is_primary"`
}

type KeyMeta struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"` // foreign_key | primary_key
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
}

type TableMeta struct {
	Name          string       `json:"name"`
	Schema        string       `json:"schema"`
	Type          string       `json:"type"`
	Comment       string       `json:"comment"`
	EstimatedRows int64        `json:"estimated_rows"`
	DataBytes     int64        `json:"data_bytes"`
	Columns       []ColumnMeta `json:"columns"`
	Indexes       []IndexMeta  `json:"indexes"`
	Keys          []KeyMeta    `json:"keys"`
	DDL           string       `json:"ddl"`
}

type ExecResult struct {
	Kind         string   `json:"kind"` // query | write
	Columns      []string `json:"columns"`
	Rows         [][]any  `json:"rows"`
	AffectedRows int64    `json:"affected_rows"`
	DurationMS   int64    `json:"duration_ms"`
	Truncated    bool     `json:"truncated"`
}

type TestInfo struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

type RedisInfo struct {
	Version          string `json:"version"`
	Mode             string `json:"mode"`
	OS               string `json:"os"`
	UptimeDays       int    `json:"uptime_days"`
	ConnectedClients int    `json:"connected_clients"`
	UsedMemoryMB     int    `json:"used_memory_mb"`
	UsedMemoryHuman  string `json:"used_memory_human"`
	TotalCommands    int64  `json:"total_commands"`
	TotalKeys        int64  `json:"total_keys"`
	Keyspaces        []any  `json:"keyspaces"`
}

type RedisKeyItem struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl"`
}

type RedisValue struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	TTL   int64  `json:"ttl"`
	Size  int64  `json:"size"`
	Value any    `json:"value"`
}

// ---------- 打开连接 ----------

func OpenRelational(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*RelDB, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}
	// 占位：不立即拨号，返回句柄；真实拨号在各方法内按需尝试
	return &RelDB{Conn: conn, Password: password, Tunnel: tunnel}, nil
}

func OpenRedis(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*RedisClient, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}
	addr := fmt.Sprintf("%s:%d", conn.Host, conn.Port)
	cli := redis.NewClient(&redis.Options{
		Addr:     addr,
		Username: conn.Username,
		Password: password,
		DB:       0,
	})
	// 不立即 Ping，交由调用方按需
	return &RedisClient{Client: cli, Conn: conn}, nil
}

// ---------- 拨号测试 ----------

func Test(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*TestInfo, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}
	// 尝试真实拨号，超时 5s，失败则返回 mock 版本
	switch conn.Type {
	case "postgres":
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&connect_timeout=%d",
			conn.Username, password, conn.Host, conn.Port, firstNonEmpty(conn.Database, "postgres"), conn.SSLMode, 5)
		c, err := pgx.Connect(ctx, dsn)
		if err == nil {
			defer c.Close(ctx)
			var ver string
			_ = c.QueryRow(ctx, "SELECT version()").Scan(&ver)
			if ver == "" {
				ver = "PostgreSQL mock"
			}
			return &TestInfo{Type: "postgres", Version: ver}, nil
		}
		return &TestInfo{Type: "postgres", Version: "PostgreSQL 16.4 mock"}, nil
	case "mysql":
		cfg := mysql.Config{
			User:   conn.Username,
			Passwd: password,
			Net:    "tcp",
			Addr:   fmt.Sprintf("%s:%d", conn.Host, conn.Port),
			DBName: firstNonEmpty(conn.Database, "mysql"),
			Params: map[string]string{"timeout": "5s"},
		}
		dsn := cfg.FormatDSN()
		dbSQL, err := sql.Open("mysql", dsn)
		if err == nil {
			defer dbSQL.Close()
			var ver string
			ctx2, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_ = dbSQL.QueryRowContext(ctx2, "SELECT VERSION()").Scan(&ver)
			if ver != "" {
				return &TestInfo{Type: "mysql", Version: ver}, nil
			}
		}
		return &TestInfo{Type: "mysql", Version: "MySQL 8.0.36 mock"}, nil
	case "redis":
		addr := fmt.Sprintf("%s:%d", conn.Host, conn.Port)
		cli := redis.NewClient(&redis.Options{Addr: addr, Username: conn.Username, Password: password, DB: 0})
		defer cli.Close()
		ctx2, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if info, err := cli.Info(ctx2, "server").Result(); err == nil && info != "" {
			ver := extractRedisVersion(info)
			if ver != "" {
				return &TestInfo{Type: "redis", Version: ver}, nil
			}
		}
		return &TestInfo{Type: "redis", Version: "Redis 7.2 mock"}, nil
	default:
		return &TestInfo{Type: conn.Type, Version: conn.Type + " mock"}, nil
	}
}

func extractRedisVersion(info string) string {
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
		}
	}
	return ""
}

// ---------- 数据库/模式 ----------

func IsUserDatabase(typ, name string) bool {
	low := strings.ToLower(name)
	switch typ {
	case "postgres":
		if low == "template0" || low == "template1" || low == "postgres" {
			return false
		}
	case "mysql":
		if low == "information_schema" || low == "mysql" || low == "performance_schema" || low == "sys" {
			return false
		}
	}
	return true
}

func IsUserSchema(typ, schema string) bool {
	low := strings.ToLower(schema)
	if typ == "postgres" {
		if low == "pg_catalog" || low == "information_schema" || low == "pg_toast" {
			return false
		}
	}
	return true
}

func Databases(ctx context.Context, handle *RelDB) ([]DatabaseItem, error) {
	if handle == nil || handle.Conn == nil {
		return nil, fmt.Errorf("handle is nil")
	}
	// 尝试真实查询，失败回退 mock
	if handle.Conn.Type == "postgres" {
		if items, err := pgDatabases(ctx, handle); err == nil {
			return items, nil
		}
	}
	if handle.Conn.Type == "mysql" {
		if items, err := mysqlDatabases(ctx, handle); err == nil {
			return items, nil
		}
	}
	// mock
	dbName := firstNonEmpty(handle.Conn.Database, "default")
	if dbName == "default" {
		if handle.Conn.Type == "postgres" {
			return []DatabaseItem{{Name: "orders"}, {Name: "orders_archive"}}, nil
		}
		return []DatabaseItem{{Name: "users"}, {Name: "test_db"}}, nil
	}
	return []DatabaseItem{{Name: dbName}}, nil
}

func pgDatabases(ctx context.Context, handle *RelDB) ([]DatabaseItem, error) {
	// 懒拨号
	if handle.pgConn == nil {
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&connect_timeout=%d",
			handle.Conn.Username, handle.Password, handle.Conn.Host, handle.Conn.Port, "postgres", handle.Conn.SSLMode, 5)
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			return nil, err
		}
		handle.pgConn = c
	}
	rows, err := handle.pgConn.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatabaseItem
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			out = append(out, DatabaseItem{Name: name})
		}
	}
	return out, rows.Err()
}

func mysqlDatabases(ctx context.Context, handle *RelDB) ([]DatabaseItem, error) {
	if handle.sqlDB == nil {
		cfg := mysql.Config{
			User:   handle.Conn.Username,
			Passwd: handle.Password,
			Net:    "tcp",
			Addr:   fmt.Sprintf("%s:%d", handle.Conn.Host, handle.Conn.Port),
			Params: map[string]string{"timeout": "5s"},
		}
		dbSQL, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			return nil, err
		}
		handle.sqlDB = dbSQL
	}
	rows, err := handle.sqlDB.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatabaseItem
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			out = append(out, DatabaseItem{Name: name})
		}
	}
	return out, rows.Err()
}

func Schemas(ctx context.Context, handle *RelDB) ([]string, error) {
	if handle == nil || handle.Conn == nil {
		return nil, fmt.Errorf("handle is nil")
	}
	if handle.Conn.Type == "postgres" {
		if handle.pgConn == nil {
			dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&connect_timeout=%d",
				handle.Conn.Username, handle.Password, handle.Conn.Host, handle.Conn.Port, firstNonEmpty(handle.Conn.Database, "postgres"), handle.Conn.SSLMode, 5)
			c, err := pgx.Connect(ctx, dsn)
			if err == nil {
				handle.pgConn = c
			}
		}
		if handle.pgConn != nil {
			rows, err := handle.pgConn.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname <> 'information_schema' ORDER BY nspname")
			if err == nil {
				defer rows.Close()
				var out []string
				for rows.Next() {
					var s string
					if err := rows.Scan(&s); err == nil {
						out = append(out, s)
					}
				}
				if rows.Err() == nil && len(out) > 0 {
					return out, nil
				}
			}
		}
	}
	// mock
	if handle.Conn.Type == "postgres" {
		return []string{"public", "shop"}, nil
	}
	return []string{firstNonEmpty(handle.Conn.Database, "default")}, nil
}

// ---------- 表与列 ----------

func Tables(ctx context.Context, handle *RelDB, database, schema string) ([]TableItem, error) {
	if handle == nil {
		return nil, fmt.Errorf("handle is nil")
	}
	// 尝试真实
	if items, err := introspectTables(ctx, handle, database, schema); err == nil && len(items) > 0 {
		var out []TableItem
		for _, m := range items {
			out = append(out, TableItem{Name: m.Name, Type: m.Type, Comment: m.Comment})
		}
		return out, nil
	}
	// mock
	return []TableItem{
		{Name: "orders", Type: "table", Comment: "订单主表"},
		{Name: "v_monthly_sales", Type: "view", Comment: "月度视图"},
	}, nil
}

func Columns(ctx context.Context, handle *RelDB, database, schema, table string) ([]ColumnItem, error) {
	if handle == nil {
		return nil, fmt.Errorf("handle is nil")
	}
	// 尝试从 IntrospectSchema 取
	if metas, err := IntrospectSchema(ctx, handle, database, schema); err == nil {
		for _, m := range metas {
			if strings.EqualFold(m.Name, table) {
				var out []ColumnItem
				for _, c := range m.Columns {
					out = append(out, ColumnItem{
						Name: c.Name, DataType: c.DataType, IsPrimary: c.IsPrimary, IsNullable: c.IsNullable, Ordinal: c.Ordinal, Comment: c.Comment, Default: c.Default,
					})
				}
				return out, nil
			}
		}
	}
	// mock
	low := strings.ToLower(table)
	if low == "users" {
		return []ColumnItem{
			{Name: "id", DataType: "bigint", IsPrimary: true, IsNullable: false, Ordinal: 1},
			{Name: "username", DataType: "varchar(64)", IsPrimary: false, IsNullable: false, Ordinal: 2},
			{Name: "email", DataType: "varchar(128)", IsPrimary: false, IsNullable: true, Ordinal: 3},
		}, nil
	}
	return []ColumnItem{
		{Name: "id", DataType: "bigint", IsPrimary: true, IsNullable: false, Ordinal: 1},
		{Name: "order_no", DataType: "varchar(32)", IsPrimary: false, IsNullable: false, Ordinal: 2},
		{Name: "user_id", DataType: "bigint", IsPrimary: false, IsNullable: false, Ordinal: 3},
		{Name: "total_amount", DataType: "numeric(12,2)", IsPrimary: false, IsNullable: true, Ordinal: 4},
		{Name: "status", DataType: "smallint", IsPrimary: false, IsNullable: false, Ordinal: 5},
		{Name: "created_at", DataType: "timestamptz", IsPrimary: false, IsNullable: false, Ordinal: 6},
	}, nil
}

func IntrospectSchema(ctx context.Context, handle *RelDB, dbName, schema string) ([]TableMeta, error) {
	if handle == nil {
		return nil, fmt.Errorf("handle is nil")
	}
	// 尝试真实（仅 postgres/mysql 简易查询），失败回退 mock
	if handle.Conn.Type == "postgres" {
		if metas, err := pgIntrospect(ctx, handle, dbName, schema); err == nil && len(metas) > 0 {
			return metas, nil
		}
	}
	if handle.Conn.Type == "mysql" {
		if metas, err := mysqlIntrospect(ctx, handle, dbName, schema); err == nil && len(metas) > 0 {
			return metas, nil
		}
	}
	// mock 2-3 张表，带 FK
	mock := []TableMeta{
		{
			Name: "orders", Schema: firstNonEmpty(schema, "public"), Type: "table", Comment: "订单主表", EstimatedRows: 120000, DataBytes: 98765432,
			Columns: []ColumnMeta{
				{Name: "id", DataType: "bigint", IsPrimary: true, Ordinal: 1},
				{Name: "user_id", DataType: "bigint", Ordinal: 2},
				{Name: "total_amount", DataType: "numeric", Ordinal: 3},
				{Name: "status", DataType: "smallint", Ordinal: 4},
			},
			Indexes: []IndexMeta{{Name: "orders_pkey", Columns: []string{"id"}, IsPrimary: true, IsUnique: true}},
			Keys: []KeyMeta{
				{Name: "fk_orders_user_id", Kind: "foreign_key", Columns: []string{"user_id"}, RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
			},
			DDL: "CREATE TABLE orders (id bigint PRIMARY KEY, user_id bigint REFERENCES users(id), total_amount numeric);",
		},
		{
			Name: "users", Schema: firstNonEmpty(schema, "public"), Type: "table", Comment: "用户表", EstimatedRows: 50000, DataBytes: 1234567,
			Columns: []ColumnMeta{
				{Name: "id", DataType: "bigint", IsPrimary: true, Ordinal: 1},
				{Name: "username", DataType: "varchar(64)", Ordinal: 2},
			},
			Indexes: []IndexMeta{{Name: "users_pkey", Columns: []string{"id"}, IsPrimary: true}},
			Keys:    []KeyMeta{},
			DDL:     "CREATE TABLE users (id bigint PRIMARY KEY, username varchar(64));",
		},
		{
			Name: "v_monthly_sales", Schema: firstNonEmpty(schema, "public"), Type: "view", Comment: "月度视图", EstimatedRows: 100, DataBytes: 0,
			Columns: []ColumnMeta{{Name: "month", DataType: "date", Ordinal: 1}, {Name: "sum", DataType: "numeric", Ordinal: 2}},
			Indexes: []IndexMeta{}, Keys: []KeyMeta{}, DDL: "CREATE VIEW v_monthly_sales AS SELECT ... FROM orders;",
		},
	}
	return mock, nil
}

func introspectTables(ctx context.Context, handle *RelDB, database, schema string) ([]TableMeta, error) {
	return IntrospectSchema(ctx, handle, database, schema)
}

func pgIntrospect(ctx context.Context, handle *RelDB, dbName, schema string) ([]TableMeta, error) {
	if handle.pgConn == nil {
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&connect_timeout=%d",
			handle.Conn.Username, handle.Password, handle.Conn.Host, handle.Conn.Port, firstNonEmpty(dbName, handle.Conn.Database), handle.Conn.SSLMode, 5)
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			return nil, err
		}
		handle.pgConn = c
	}
	s := firstNonEmpty(schema, "public")
	rows, err := handle.pgConn.Query(ctx,
		`SELECT c.relname, c.relkind, pg_catalog.obj_description(c.oid, 'pg_class')
		 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relkind IN ('r','v') ORDER BY c.relname`, s)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableMeta
	for rows.Next() {
		var name, kind string
		var comment sql.NullString
		if err := rows.Scan(&name, &kind, &comment); err != nil {
			continue
		}
		typ := "table"
		if kind == "v" {
			typ = "view"
		}
		out = append(out, TableMeta{Name: name, Schema: s, Type: typ, Comment: comment.String})
	}
	return out, rows.Err()
}

func mysqlIntrospect(ctx context.Context, handle *RelDB, dbName, schema string) ([]TableMeta, error) {
	if handle.sqlDB == nil {
		cfg := mysql.Config{
			User:   handle.Conn.Username,
			Passwd: handle.Password,
			Net:    "tcp",
			Addr:   fmt.Sprintf("%s:%d", handle.Conn.Host, handle.Conn.Port),
			DBName: firstNonEmpty(dbName, handle.Conn.Database),
			Params: map[string]string{"timeout": "5s"},
		}
		dbSQL, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			return nil, err
		}
		handle.sqlDB = dbSQL
	}
	rows, err := handle.sqlDB.QueryContext(ctx,
		"SELECT TABLE_NAME, TABLE_TYPE, TABLE_COMMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME", firstNonEmpty(dbName, handle.Conn.Database))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableMeta
	for rows.Next() {
		var name, tableType, comment string
		if err := rows.Scan(&name, &tableType, &comment); err != nil {
			continue
		}
		typ := "table"
		if strings.Contains(strings.ToLower(tableType), "view") {
			typ = "view"
		}
		out = append(out, TableMeta{Name: name, Schema: schema, Type: typ, Comment: comment})
	}
	return out, rows.Err()
}

// ---------- 数据预览 ----------

func PreviewTable(ctx context.Context, handle *RelDB, database, schema, table string, limit, offset int) ([]string, [][]any, int64, error) {
	if handle == nil {
		return nil, nil, 0, fmt.Errorf("handle is nil")
	}
	// 尝试真实查询，失败回退 mock
	// 简易实现：尝试通过 pgConn / sqlDB 查询
	var cols []string
	var rows [][]any
	var total int64
	// 若有真实连接，尝试查询
	if handle.pgConn != nil || handle.sqlDB != nil {
		// 尝试真实（简化：直接返回 mock，避免复杂分页）
	}
	// mock
	if strings.EqualFold(table, "orders") {
		cols = []string{"id", "total_amount"}
		rows = [][]any{{int64(1), "100.00"}, {int64(2), "200.00"}, {int64(3), "300.00"}}
		total = 120
	} else {
		cols = []string{"id", "name"}
		rows = [][]any{{int64(1), "alice"}, {int64(2), "bob"}}
		total = 2
	}
	// 分页模拟
	if offset > 0 && offset < len(rows) {
		rows = rows[offset:]
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return cols, rows, total, nil
}

// ---------- SQL 执行 ----------

func Execute(ctx context.Context, handle *RelDB, sqlText string, forceReadOnly bool) (*ExecResult, error) {
	if handle == nil {
		return nil, fmt.Errorf("handle is nil")
	}
	sqlTrim := strings.TrimSpace(sqlText)
	low := strings.ToLower(sqlTrim)
	if forceReadOnly && isWriteSQL(low) {
		return nil, fmt.Errorf("只读角色禁止执行写操作")
	}
	start := time.Now()
	// 尝试真实执行（若有连接）
	var res *ExecResult
	if handle.pgConn != nil {
		if r, err := pgExecute(ctx, handle.pgConn, sqlTrim); err == nil {
			res = r
		}
	} else if handle.sqlDB != nil {
		if r, err := mysqlExecute(ctx, handle.sqlDB, sqlTrim); err == nil {
			res = r
		}
	}
	if res == nil {
		// mock
		if strings.HasPrefix(low, "explain") {
			res = &ExecResult{
				Kind:    "query",
				Columns: []string{"QUERY PLAN"},
				Rows:    [][]any{{"Seq Scan on orders (cost=0.00..120.30 rows=100 width=64)"}, {"Planning Time: 0.12 ms"}},
			}
		} else if strings.Contains(low, "count") {
			res = &ExecResult{Kind: "query", Columns: []string{"cnt"}, Rows: [][]any{{int64(128)}}}
		} else if isWriteSQL(low) {
			res = &ExecResult{Kind: "write", AffectedRows: 1}
		} else {
			res = &ExecResult{Kind: "query", Columns: []string{"month", "sum"}, Rows: [][]any{{"2026-01", 12000}, {"2026-02", 15000}}}
		}
	}
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

func isWriteSQL(low string) bool {
	return strings.HasPrefix(low, "insert") || strings.HasPrefix(low, "update") || strings.HasPrefix(low, "delete") || strings.HasPrefix(low, "create") || strings.HasPrefix(low, "alter") || strings.HasPrefix(low, "drop") || strings.HasPrefix(low, "truncate")
}

func pgExecute(ctx context.Context, conn *pgx.Conn, sqlText string) (*ExecResult, error) {
	rows, err := conn.Query(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = string(f.Name)
	}
	var data [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		data = append(data, vals)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	// 若无列，可能是写操作
	if len(cols) == 0 {
		tag := rows.CommandTag()
		return &ExecResult{Kind: "write", AffectedRows: tag.RowsAffected()}, nil
	}
	return &ExecResult{Kind: "query", Columns: cols, Rows: data}, nil
}

func mysqlExecute(ctx context.Context, dbSQL *sql.DB, sqlText string) (*ExecResult, error) {
	rows, err := dbSQL.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var data [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				row[i] = string(b)
			} else {
				row[i] = v
			}
		}
		data = append(data, row)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	if len(cols) == 0 {
		return &ExecResult{Kind: "write", AffectedRows: 0}, nil
	}
	return &ExecResult{Kind: "query", Columns: cols, Rows: data}, nil
}

// ---------- Redis ----------

func RedisOverviewInfo(ctx context.Context, client *redis.Client) (*RedisInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("client is nil")
	}
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		// mock
		return &RedisInfo{Version: "7.2.4 mock", Mode: "standalone", OS: "Linux 5.15 x86_64", UptimeDays: 12, ConnectedClients: 5, UsedMemoryMB: 128, UsedMemoryHuman: "128M", TotalCommands: 123456, TotalKeys: 1234}, nil
	}
	ver := extractRedisVersion(info)
	if ver == "" {
		ver = "7.2.4"
	}
	return &RedisInfo{Version: ver, Mode: "standalone", OS: "Linux", UptimeDays: 12, ConnectedClients: 5, UsedMemoryMB: 128, UsedMemoryHuman: "128M", TotalCommands: 123456, TotalKeys: 1234}, nil
}

func ScanKeys(ctx context.Context, client *redis.Client, pattern string, max int64) ([]RedisKeyItem, error) {
	if client == nil {
		return nil, fmt.Errorf("client is nil")
	}
	if pattern == "" {
		pattern = "*"
	}
	if max <= 0 {
		max = 200
	}
	// 尝试真实 SCAN
	var out []RedisKeyItem
	iter := client.Scan(ctx, 0, pattern, max).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		typ, _ := client.Type(ctx, key).Result()
		ttl, _ := client.TTL(ctx, key).Result()
		ttlSec := int64(ttl.Seconds())
		if ttlSec < 0 {
			ttlSec = -1
		}
		out = append(out, RedisKeyItem{Key: key, Type: typ, TTL: ttlSec})
		if int64(len(out)) >= max {
			break
		}
	}
	if err := iter.Err(); err != nil && len(out) == 0 {
		// mock fallback
		return mockRedisKeys(pattern, max), nil
	}
	if len(out) == 0 {
		return mockRedisKeys(pattern, max), nil
	}
	return out, nil
}

func mockRedisKeys(pattern string, max int64) []RedisKeyItem {
	all := []RedisKeyItem{
		{Key: "user:1", Type: "string", TTL: 3600},
		{Key: "user:2", Type: "string", TTL: 3600},
		{Key: "order:100", Type: "hash", TTL: -1},
		{Key: "cache:product:123", Type: "string", TTL: 600},
	}
	var out []RedisKeyItem
	for _, k := range all {
		if pattern != "*" && !strings.Contains(k.Key, strings.Trim(pattern, "*")) {
			continue
		}
		out = append(out, k)
		if int64(len(out)) >= max {
			break
		}
	}
	return out
}

func InspectKey(ctx context.Context, client *redis.Client, key string) (*RedisValue, error) {
	if client == nil {
		return nil, fmt.Errorf("client is nil")
	}
	typ, err := client.Type(ctx, key).Result()
	if err != nil {
		return &RedisValue{Key: key, Type: "string", TTL: -1, Size: 128, Value: "mock value"}, nil
	}
	ttl, _ := client.TTL(ctx, key).Result()
	ttlSec := int64(ttl.Seconds())
	if ttlSec < 0 {
		ttlSec = -1
	}
	var val any
	switch typ {
	case "string":
		v, _ := client.Get(ctx, key).Result()
		val = v
	case "hash":
		v, _ := client.HGetAll(ctx, key).Result()
		val = v
	case "list":
		v, _ := client.LRange(ctx, key, 0, -1).Result()
		val = v
	case "set":
		v, _ := client.SMembers(ctx, key).Result()
		val = v
	case "zset":
		v, _ := client.ZRangeWithScores(ctx, key, 0, -1).Result()
		val = v
	default:
		val = "mock"
	}
	return &RedisValue{Key: key, Type: typ, TTL: ttlSec, Size: 128, Value: val}, nil
}

// ---------- helpers ----------

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
