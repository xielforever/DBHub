package target

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxResultRows 查询接口最多返回的数据行数，超出时截断并标记 truncated。
const maxResultRows = 1000

// Result SQL 执行结果。查询语句携带列与行，写语句携带影响行数。
type Result struct {
	Kind         string   `json:"kind"` // query | write
	Columns      []string `json:"columns,omitempty"`
	Rows         [][]any  `json:"rows,omitempty"`
	AffectedRows int64    `json:"affected_rows,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
	DurationMS   int64    `json:"duration_ms"`
}

// NameItem 仅含名称的列表项（数据库列表等）。
type NameItem struct {
	Name string `json:"name"`
}

// TableInfo 表/视图摘要。
type TableInfo struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Type   string `json:"type"` // table | view | ...
}

// ColumnInfo 列结构信息。
type ColumnInfo struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	IsNullable bool   `json:"is_nullable"`
	IsPrimary  bool   `json:"is_primary"`
	Default    string `json:"default,omitempty"`
	Ordinal    int    `json:"ordinal"`
}

// readKeywords 视为只读查询的起始关键字（小写匹配）。
var readKeywords = map[string]bool{
	"select": true, "with": true, "show": true, "describe": true,
	"desc": true, "explain": true, "values": true, "table": true,
}

// stripLeadingComments 去除 SQL 头部的行/块注释，便于语句分类。
func stripLeadingComments(s string) string {
	for {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "--") {
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(s, "/*") {
			if i := strings.Index(s[2:], "*/"); i >= 0 {
				s = s[2+i+2:]
				continue
			}
			return ""
		}
		return s
	}
}

// firstKeyword 提取首关键字。
func firstKeyword(sqlText string) string {
	trimmed := stripLeadingComments(sqlText)
	var kw strings.Builder
	for _, r := range trimmed {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' {
			break
		}
		kw.WriteRune(r)
	}
	return strings.ToLower(kw.String())
}

// ensureSingleStatement 拒绝多语句批量执行，配合只读角色做安全兜底。
func ensureSingleStatement(sqlText string) error {
	trimmed := strings.TrimSpace(sqlText)
	body := strings.TrimSuffix(strings.TrimSpace(trimmed), ";")
	if strings.Contains(body, ";") {
		return errors.New("一次只允许执行一条 SQL 语句")
	}
	return nil
}

// Execute 在目标库执行 SQL。forceReadOnly=true 时（只读角色）仅允许只读语句，
// 除路由层 RBAC 外再提供一道引擎内防护。
func Execute(ctx context.Context, h *RelDB, sqlText string, forceReadOnly bool) (*Result, error) {
	if err := ensureSingleStatement(sqlText); err != nil {
		return nil, err
	}
	kw := firstKeyword(sqlText)
	isRead := readKeywords[kw]
	if forceReadOnly && !isRead {
		return nil, fmt.Errorf("只读角色禁止执行非查询语句: %s", strings.ToUpper(kw))
	}

	start := time.Now()
	if isRead {
		rows, err := h.db.QueryContext(ctx, sqlText)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		out := make([][]any, 0, 64)
		truncated := false
		for rows.Next() {
			if len(out) >= maxResultRows {
				truncated = true
				break
			}
			raw := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range raw {
				ptrs[i] = &raw[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, err
			}
			row := make([]any, len(cols))
			for i, v := range raw {
				row[i] = normalizeValue(v)
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return &Result{
			Kind: "query", Columns: cols, Rows: out, Truncated: truncated,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil
	}

	res, err := h.db.ExecContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	return &Result{
		Kind: "write", AffectedRows: affected,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// normalizeValue 把驱动返回值统一转为前端可展示的 JSON 友好类型。
func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return coerceBytes(t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	default:
		return v
	}
}

// coerceBytes 对 MySQL 常见的 []byte 结果做数值/布尔的尽力还原。
func coerceBytes(b []byte) any {
	s := string(b)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && s == strconv.FormatInt(n, 10) {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && strings.ContainsAny(s, ".eE") {
		return f
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}

// currentDatabase 返回连接当前所在库。
func currentDatabase(ctx context.Context, h *RelDB) string {
	q := "SELECT DATABASE()"
	if h.Kind == "postgres" {
		q = "SELECT current_database()"
	}
	var name sql.NullString
	if err := h.db.QueryRowContext(ctx, q).Scan(&name); err != nil {
		return ""
	}
	return name.String
}

// Databases 列出目标实例上的数据库。
func Databases(ctx context.Context, h *RelDB) ([]NameItem, error) {
	var q string
	switch h.Kind {
	case "mysql":
		q = "SHOW DATABASES"
	case "postgres":
		q = "SELECT datname FROM pg_database WHERE datallowconn ORDER BY 1"
	}
	rows, err := h.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NameItem, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, NameItem{Name: name})
	}
	return out, rows.Err()
}

// Schemas 列出模式（MySQL 下列出 database/schema）。
func Schemas(ctx context.Context, h *RelDB) ([]string, error) {
	var q string
	switch h.Kind {
	case "mysql":
		q = "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY 1"
	case "postgres":
		q = `SELECT nspname FROM pg_namespace
			WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema'
			ORDER BY 1`
	}
	rows, err := h.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Tables 列出指定库/模式下的表与视图。
func Tables(ctx context.Context, h *RelDB, database, schema string) ([]TableInfo, error) {
	var q string
	args := []any{}
	switch h.Kind {
	case "mysql":
		q = `SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE
			FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = IFNULL(NULLIF(?, ''), DATABASE())
			ORDER BY TABLE_NAME`
		args = append(args, firstNonEmpty(schema, database, h.Database))
	case "postgres":
		q = `SELECT table_schema, table_name, table_type
			FROM information_schema.tables
			WHERE table_schema = IFNULL(NULLIF($1, ''), 'public')
			ORDER BY table_name`
		args = append(args, schema)
	}
	rows, err := h.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TableInfo, 0)
	for rows.Next() {
		var t TableInfo
		var rawType string
		if err := rows.Scan(&t.Schema, &t.Name, &rawType); err != nil {
			return nil, err
		}
		switch rawType {
		case "BASE TABLE":
			t.Type = "table"
		case "VIEW":
			t.Type = "view"
		default:
			t.Type = strings.ToLower(strings.ReplaceAll(rawType, " ", "_"))
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Columns 返回表的列结构。
func Columns(ctx context.Context, h *RelDB, database, schema, table string) ([]ColumnInfo, error) {
	var q string
	args := []any{}
	switch h.Kind {
	case "mysql":
		q = `SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_KEY,
			IFNULL(COLUMN_DEFAULT, ''), ORDINAL_POSITION
			FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = IFNULL(NULLIF(?, ''), DATABASE()) AND TABLE_NAME = ?
			ORDER BY ORDINAL_POSITION`
		args = append(args, firstNonEmpty(schema, database, h.Database), table)
	case "postgres":
		q = `SELECT c.column_name, c.data_type, c.is_nullable,
			COALESCE((
				SELECT 'PRI' FROM information_schema.key_column_usage k
				JOIN information_schema.table_constraints tc
				  ON tc.constraint_name = k.constraint_name
				 AND tc.constraint_schema = k.constraint_schema
				WHERE tc.constraint_type = 'PRIMARY KEY'
				  AND k.table_schema = c.table_schema AND k.table_name = c.table_name
				  AND k.column_name = c.column_name
				LIMIT 1), ''),
			COALESCE(c.column_default, ''), c.ordinal_position
			FROM information_schema.columns c
			WHERE c.table_schema = IFNULL(NULLIF($1, ''), 'public') AND c.table_name = $2
			ORDER BY c.ordinal_position`
		args = append(args, schema, table)
	}
	rows, err := h.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ColumnInfo, 0)
	for rows.Next() {
		var c ColumnInfo
		var nullable, pri string
		if err := rows.Scan(&c.Name, &c.DataType, &nullable, &pri, &c.Default, &c.Ordinal); err != nil {
			return nil, err
		}
		c.IsNullable = strings.EqualFold(nullable, "YES")
		c.IsPrimary = pri == "PRI"
		out = append(out, c)
	}
	return out, rows.Err()
}

// PreviewTable 分页预览表数据，返回列、行与总行数。
func PreviewTable(ctx context.Context, h *RelDB, database, schema, table string, limit, offset int) ([]string, [][]any, int64, error) {
	sch := schema
	if h.Kind == "mysql" && sch == "" {
		sch = firstNonEmpty(database, h.Database)
	}
	ident := quoteQualified(h, sch, table)

	var total int64
	countQ := "SELECT COUNT(*) FROM " + ident
	if err := h.db.QueryRowContext(ctx, countQ).Scan(&total); err != nil {
		return nil, nil, 0, err
	}

	var dataQ string
	if h.Kind == "postgres" {
		dataQ = "SELECT * FROM " + ident + " LIMIT $1 OFFSET $2"
	} else {
		dataQ = "SELECT * FROM " + ident + " LIMIT ? OFFSET ?"
	}
	rows, err := h.db.QueryContext(ctx, dataQ, limit, offset)
	if err != nil {
		return nil, nil, 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, 0, err
	}
	out := make([][]any, 0, limit)
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, 0, err
		}
		row := make([]any, len(cols))
		for i, v := range raw {
			row[i] = normalizeValue(v)
		}
		out = append(out, row)
	}
	return cols, out, total, rows.Err()
}

// quoteIdent 按方言引用标识符。
func quoteIdent(h *RelDB, id string) string {
	if h.Kind == "mysql" {
		return "`" + strings.ReplaceAll(id, "`", "``") + "`"
	}
	return "\"" + strings.ReplaceAll(id, "\"", "\"\"") + "\""
}

func quoteQualified(h *RelDB, schema, table string) string {
	t := quoteIdent(h, table)
	if schema != "" {
		return quoteIdent(h, schema) + "." + t
	}
	return t
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
