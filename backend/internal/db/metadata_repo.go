// 数据资产元数据仓储：字典快照、人工标注、收藏与查询热度聚合。
package db

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MetaSnapshot 一张表/视图的采集快照。
type MetaSnapshot struct {
	ID            int64           `json:"id"`
	ConnectionID  int64           `json:"connection_id"`
	DatabaseName  string          `json:"database_name"`
	SchemaName    string          `json:"schema_name"`
	TableName     string          `json:"table_name"`
	TableType     string          `json:"table_type"`
	TableComment  string          `json:"table_comment"`
	EstimatedRows int64           `json:"estimated_rows"`
	DataBytes     int64           `json:"data_bytes"`
	RawColumns    json.RawMessage `json:"raw_columns"`
	RawIndexes    json.RawMessage `json:"raw_indexes"`
	RawKeys       json.RawMessage `json:"raw_keys"`
	DDLText       string          `json:"ddl_text"`
	SyncedAt      time.Time       `json:"synced_at"`
}

// AssetAnnotation 表级或列级人工标注。
type AssetAnnotation struct {
	ID           int64    `json:"id"`
	ConnectionID int64    `json:"connection_id"`
	DatabaseName string   `json:"database_name"`
	SchemaName   string   `json:"schema_name"`
	TableName    string   `json:"table_name"`
	ColumnName   string   `json:"column_name"`
	OwnerUserID  *int64   `json:"owner_user_id"`
	BusinessDesc string   `json:"business_desc"`
	Tags         []string `json:"tags"`
	Sensitivity  string   `json:"sensitivity"`
	StarredBy    []int64  `json:"starred_by"`
	UpdatedBy    *int64   `json:"updated_by"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableListFilter 资产列表过滤。
type TableListFilter struct {
	ConnectionID int64
	Database     string
	Schema       string
	Keyword      string
	ObjectType   string // table | view，空为全部
	Sensitivity  string // normal | sensitive | confidential
	NoOwner      bool
	StarredBy    int64 // >0 时仅返回收藏
	Offset       int
	Limit        int
}

// TableListItem 列表行：快照摘要 + 表级标注 + 热度。
type TableListItem struct {
	MetaSnapshot
	OwnerID            *int64   `json:"owner_user_id"`
	OwnerName          string   `json:"owner_name"`
	BusinessDesc       string   `json:"business_desc"`
	Tags               []string `json:"tags"`
	Sensitivity        string   `json:"sensitivity"`
	Starred            bool     `json:"starred"`
	QueryCount30d      int64    `json:"query_count_30d"`
	SensitiveColCount  int      `json:"sensitive_columns"`
}

// MetadataRepository 资产元数据仓储。
type MetadataRepository struct {
	pool *pgxpool.Pool
}

func NewMetadataRepository(pool *pgxpool.Pool) *MetadataRepository {
	return &MetadataRepository{pool: pool}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

// UpsertSnapshot 单表快照 upsert（按对象四元组唯一）。
func (r *MetadataRepository) UpsertSnapshot(ctx context.Context, s *MetaSnapshot) error {
	if s.RawColumns == nil {
		s.RawColumns = json.RawMessage("[]")
	}
	if s.RawIndexes == nil {
		s.RawIndexes = json.RawMessage("[]")
	}
	if s.RawKeys == nil {
		s.RawKeys = json.RawMessage("[]")
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO sys_meta_snapshots
			(connection_id, database_name, schema_name, table_name, table_type,
			 table_comment, estimated_rows, data_bytes, raw_columns, raw_indexes,
			 raw_keys, ddl_text, synced_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12, NOW())
		ON CONFLICT ON CONSTRAINT uq_meta_snapshot_obj DO UPDATE SET
			table_type = EXCLUDED.table_type,
			table_comment = EXCLUDED.table_comment,
			estimated_rows = EXCLUDED.estimated_rows,
			data_bytes = EXCLUDED.data_bytes,
			raw_columns = EXCLUDED.raw_columns,
			raw_indexes = EXCLUDED.raw_indexes,
			raw_keys = EXCLUDED.raw_keys,
			ddl_text = EXCLUDED.ddl_text,
			synced_at = NOW()
		RETURNING id, synced_at`,
		s.ConnectionID, s.DatabaseName, s.SchemaName, s.TableName, s.TableType,
		s.TableComment, s.EstimatedRows, s.DataBytes, s.RawColumns, s.RawIndexes,
		s.RawKeys, s.DDLText,
	).Scan(&s.ID, &s.SyncedAt)
}

// PruneDatabase 删除指定库本次未采集到的过期快照（按表名集合）。
func (r *MetadataRepository) PruneDatabase(ctx context.Context, connID int64, database string, keep []string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM sys_meta_snapshots
		WHERE connection_id = $1 AND database_name = $2
		  AND ($3 = '{}' OR table_name <> ALL($3))`,
		connID, database, keep)
	return err
}

// ListTables 资产列表（带表级标注与 Owner 用户名）。
func (r *MetadataRepository) ListTables(ctx context.Context, f TableListFilter) ([]TableListItem, int64, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	// 条件模板用 "?" 占位，渲染时按参数实际顺序编号；每个条件可携带多个参数
	// （关键字条件三个占位符共用同一 LIKE 参数）。count 与 list 两个查询
	// 参数不同（list 的 SELECT 固定以 $1 传当前用户做收藏标记），分别渲染。
	type wcond struct {
		tmpl string
		vals []any
		star bool // 收藏条件特例：list 复用 $1，count 末尾追加用户参数
	}
	conds := []wcond{}
	add := func(tmpl string, vals ...any) { conds = append(conds, wcond{tmpl: tmpl, vals: vals}) }
	if f.ConnectionID > 0 {
		add("s.connection_id = ?", f.ConnectionID)
	}
	if f.Database != "" {
		add("s.database_name = ?", f.Database)
	}
	if f.Schema != "" {
		add("s.schema_name = ?", f.Schema)
	}
	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		add("(s.table_name ILIKE ? OR s.table_comment ILIKE ? OR COALESCE(a.business_desc,'') ILIKE ?)",
			like, like, like)
	}
	if f.ObjectType != "" {
		add("s.table_type = ?", f.ObjectType)
	}
	if f.Sensitivity != "" {
		add("COALESCE(a.sensitivity, 'normal') = ?", f.Sensitivity)
	}
	if f.NoOwner {
		add("a.owner_user_id IS NULL")
	}
	if f.StarredBy > 0 {
		conds = append(conds, wcond{star: true})
	}
	// render 渲染 WHERE：starAsFirst 时 $1 固定为当前用户（list 查询 SELECT 也用）。
	render := func(starAsFirst bool) (string, []any) {
		args := make([]any, 0, len(conds)+3)
		parts := []string{"1=1"}
		if starAsFirst {
			args = append(args, f.StarredBy)
		}
		expand := func(tmpl string, vals []any) string {
			out := tmpl
			for _, v := range vals {
				args = append(args, v)
				out = strings.Replace(out, "?", fmt.Sprintf("$%d", len(args)), 1)
			}
			return out
		}
		for _, c := range conds {
			switch {
			case c.star && starAsFirst:
				parts = append(parts, "$1 = ANY(a.starred_by)")
			case c.star:
				args = append(args, f.StarredBy)
				parts = append(parts, fmt.Sprintf("$%d = ANY(a.starred_by)", len(args)))
			default:
				parts = append(parts, expand(c.tmpl, c.vals))
			}
		}
		return strings.Join(parts, " AND "), args
	}
	from := `
		FROM sys_meta_snapshots s
		LEFT JOIN sys_asset_annotations a
		  ON a.connection_id = s.connection_id
		 AND a.database_name = s.database_name
		 AND a.schema_name = s.schema_name
		 AND a.table_name = s.table_name
		 AND a.column_name = ''
		LEFT JOIN sys_users u ON u.id = a.owner_user_id`

	countWhere, countArgs := render(false)
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) `+from+` WHERE `+countWhere, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计资产列表失败: %w", err)
	}

	listWhere, args := render(true)
	listSQL := `
		SELECT s.id, s.connection_id, s.database_name, s.schema_name, s.table_name,
		       s.table_type, s.table_comment, s.estimated_rows, s.data_bytes,
		       s.raw_columns, s.raw_indexes, s.raw_keys, s.ddl_text, s.synced_at,
		       a.owner_user_id, COALESCE(u.username, ''),
		       COALESCE(a.business_desc, ''),
		       COALESCE(a.tags, '{}'),
		       COALESCE(a.sensitivity, 'normal'),
		       COALESCE($1 = ANY(a.starred_by), FALSE),
		       (SELECT COUNT(*) FROM sys_asset_annotations c
		         WHERE c.connection_id = s.connection_id
		           AND c.database_name = s.database_name
		           AND c.schema_name = s.schema_name
		           AND c.table_name = s.table_name
		           AND c.column_name <> ''
		           AND c.sensitivity <> 'normal')
		` + from + `
		WHERE ` + listWhere + `
		ORDER BY s.database_name, s.schema_name, s.table_name
		LIMIT $` + fmt.Sprint(len(args)+1) + ` OFFSET $` + fmt.Sprint(len(args)+2)
	args = append(args, f.Limit, f.Offset)
	rows, err := r.pool.Query(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询资产列表失败: %w", err)
	}
	defer rows.Close()
	items := make([]TableListItem, 0, f.Limit)
	for rows.Next() {
		var it TableListItem
		if err := rows.Scan(
			&it.ID, &it.ConnectionID, &it.DatabaseName, &it.SchemaName, &it.TableName,
			&it.TableType, &it.TableComment, &it.EstimatedRows, &it.DataBytes,
			&it.RawColumns, &it.RawIndexes, &it.RawKeys, &it.DDLText, &it.SyncedAt,
			&it.OwnerID, &it.OwnerName, &it.BusinessDesc, &it.Tags, &it.Sensitivity,
			&it.Starred, &it.SensitiveColCount,
		); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	return items, total, rows.Err()
}

// GetSnapshot 取单表完整快照。
func (r *MetadataRepository) GetSnapshot(ctx context.Context, connID int64, database, schema, table string) (*MetaSnapshot, error) {
	s := &MetaSnapshot{}
	err := r.pool.QueryRow(ctx, `
		SELECT id, connection_id, database_name, schema_name, table_name, table_type,
		       table_comment, estimated_rows, data_bytes, raw_columns, raw_indexes,
		       raw_keys, ddl_text, synced_at
		FROM sys_meta_snapshots
		WHERE connection_id = $1 AND database_name = $2
		  AND schema_name = COALESCE(NULLIF($3, ''), schema_name) AND table_name = $4`,
		connID, database, schema, table,
	).Scan(&s.ID, &s.ConnectionID, &s.DatabaseName, &s.SchemaName, &s.TableName,
		&s.TableType, &s.TableComment, &s.EstimatedRows, &s.DataBytes,
		&s.RawColumns, &s.RawIndexes, &s.RawKeys, &s.DDLText, &s.SyncedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// ListSnapshots 取一个连接（或全部连接）的全部快照摘要（树/统计/搜索用）。
func (r *MetadataRepository) ListSnapshots(ctx context.Context, connID int64) ([]MetaSnapshot, error) {
	q := `SELECT id, connection_id, database_name, schema_name, table_name, table_type,
	             table_comment, estimated_rows, data_bytes, '[]'::jsonb,
	             '[]'::jsonb, '[]'::jsonb, '', synced_at
	      FROM sys_meta_snapshots`
	args := []any{}
	if connID > 0 {
		q += ` WHERE connection_id = $1`
		args = append(args, connID)
	}
	q += ` ORDER BY database_name, schema_name, table_name`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MetaSnapshot, 0)
	for rows.Next() {
		var s MetaSnapshot
		if err := rows.Scan(&s.ID, &s.ConnectionID, &s.DatabaseName, &s.SchemaName, &s.TableName,
			&s.TableType, &s.TableComment, &s.EstimatedRows, &s.DataBytes,
			&s.RawColumns, &s.RawIndexes, &s.RawKeys, &s.DDLText, &s.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ColumnHit 字段搜索命中行。
type ColumnHit struct {
	ConnectionID int64  `json:"connection_id"`
	DatabaseName string `json:"database"`
	SchemaName   string `json:"schema"`
	TableName    string `json:"table"`
	ColumnName   string `json:"column"`
	DataType     string `json:"data_type"`
	Comment      string `json:"comment"`
}

// SearchColumns 在快照 raw_columns（JSONB）中按列名/注释 ILIKE 检索。
func (r *MetadataRepository) SearchColumns(ctx context.Context, keyword string, limit int) ([]ColumnHit, error) {
	if limit <= 0 || limit > 50 {
		limit = 8
	}
	like := "%" + keyword + "%"
	rows, err := r.pool.Query(ctx, `
		SELECT s.connection_id, s.database_name, s.schema_name, s.table_name,
		       c.col->>'name', c.col->>'data_type', COALESCE(c.col->>'comment', '')
		FROM sys_meta_snapshots s,
		     LATERAL jsonb_array_elements(s.raw_columns) AS c(col)
		WHERE c.col->>'name' ILIKE $1 OR c.col->>'comment' ILIKE $1
		ORDER BY s.connection_id, s.database_name, s.schema_name, s.table_name
		LIMIT $2`, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ColumnHit, 0)
	for rows.Next() {
		var h ColumnHit
		if err := rows.Scan(&h.ConnectionID, &h.DatabaseName, &h.SchemaName, &h.TableName,
			&h.ColumnName, &h.DataType, &h.Comment); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// UpsertAnnotation 表级/列级标注 upsert（仅更新人工字段）。
type AnnotationInput struct {
	ConnectionID int64
	DatabaseName string
	SchemaName   string
	TableName    string
	ColumnName   string
	OwnerUserID  *int64
	BusinessDesc string
	Tags         []string
	Sensitivity  string
	UpdatedBy    int64
}

func (r *MetadataRepository) UpsertAnnotation(ctx context.Context, in *AnnotationInput) error {
	if in.Sensitivity == "" {
		in.Sensitivity = "normal"
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sys_asset_annotations
			(connection_id, database_name, schema_name, table_name, column_name,
			 owner_user_id, business_desc, tags, sensitivity, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT ON CONSTRAINT uq_asset_annotation_obj DO UPDATE SET
			owner_user_id = EXCLUDED.owner_user_id,
			business_desc = EXCLUDED.business_desc,
			tags = EXCLUDED.tags,
			sensitivity = EXCLUDED.sensitivity,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()`,
		in.ConnectionID, in.DatabaseName, in.SchemaName, in.TableName, in.ColumnName,
		in.OwnerUserID, in.BusinessDesc, in.Tags, in.Sensitivity, in.UpdatedBy)
	return err
}

// ListAnnotations 批量取若干表的全部标注（表级与列级）。
func (r *MetadataRepository) ListAnnotations(ctx context.Context, connID int64, database string) ([]AssetAnnotation, error) {
	q := `SELECT id, connection_id, database_name, schema_name, table_name, column_name,
	             owner_user_id, business_desc, tags, sensitivity,
	             COALESCE(starred_by, '{}'), updated_by, updated_at
	      FROM sys_asset_annotations WHERE connection_id = $1`
	args := []any{connID}
	if database != "" {
		q += ` AND database_name = $2`
		args = append(args, database)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnnotations(rows)
}

// SetStar 收藏/取消收藏（幂等数组增删）。
func (r *MetadataRepository) SetStar(ctx context.Context, connID int64, database, schema, table string, userID int64, star bool) error {
	// 先确保表级标注行存在，再更新数组。
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sys_asset_annotations
			(connection_id, database_name, schema_name, table_name, column_name, updated_by)
		VALUES ($1,$2,$3,$4,'',$5)
		ON CONFLICT ON CONSTRAINT uq_asset_annotation_obj DO NOTHING`,
		connID, database, schema, table, userID)
	if err != nil {
		return err
	}
	if star {
		_, err = r.pool.Exec(ctx, `
			UPDATE sys_asset_annotations SET starred_by = (
				SELECT COALESCE(array_agg(DISTINCT x), '{}')
				FROM unnest(array_append(COALESCE(starred_by,'{}'), $5)) x)
			WHERE connection_id=$1 AND database_name=$2 AND schema_name=$3
			  AND table_name=$4 AND column_name=''`,
			connID, database, schema, table, userID)
	} else {
		_, err = r.pool.Exec(ctx, `
			UPDATE sys_asset_annotations
			SET starred_by = array_remove(COALESCE(starred_by,'{}'), $5)
			WHERE connection_id=$1 AND database_name=$2 AND schema_name=$3
			  AND table_name=$4 AND column_name=''`,
			connID, database, schema, table, userID)
	}
	return err
}

// OverviewCounts 资产概览统计。
type OverviewCounts struct {
	ConnectionTotal int64 `json:"connection_total"`
	TableTotal      int64 `json:"table_total"`
	ViewTotal       int64 `json:"view_total"`
	OwnedTables     int64 `json:"owned_tables"`
	SensitiveFields int64 `json:"sensitive_fields"`
}

func (r *MetadataRepository) Overview(ctx context.Context) (*OverviewCounts, error) {
	var o OverviewCounts
	err := r.pool.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM sys_connections),
		  (SELECT COUNT(*) FROM sys_meta_snapshots WHERE table_type='table'),
		  (SELECT COUNT(*) FROM sys_meta_snapshots WHERE table_type='view'),
		  (SELECT COUNT(DISTINCT (s.connection_id,s.database_name,s.schema_name,s.table_name))
		     FROM sys_meta_snapshots s
		     JOIN sys_asset_annotations a
		       ON a.connection_id=s.connection_id AND a.database_name=s.database_name
		      AND a.schema_name=s.schema_name AND a.table_name=s.table_name
		      AND a.column_name=''
		    WHERE a.owner_user_id IS NOT NULL),
		  (SELECT COUNT(*) FROM sys_asset_annotations
		    WHERE column_name <> '' AND sensitivity <> 'normal')`).Scan(
		&o.ConnectionTotal, &o.TableTotal, &o.ViewTotal, &o.OwnedTables, &o.SensitiveFields)
	return &o, err
}

// HeatItem 热度条目。
type HeatItem struct {
	ConnectionID int64  `json:"connection_id"`
	Database     string `json:"database"`
	Schema       string `json:"schema"`
	Table        string `json:"table"`
	Count        int64  `json:"count"`
}

// HeatMap 近 N 天表查询热度：聚合查询历史，正则词边界匹配，避免子串误报。
// 规模控制：每连接最多回溯 5000 条成功 SQL。
func (r *MetadataRepository) HeatMap(ctx context.Context, days int) (map[string]int64, error) {
	if days <= 0 {
		days = 30
	}
	snaps, err := r.ListSnapshots(ctx, 0)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return map[string]int64{}, nil
	}
	connRows := map[int64][]MetaSnapshot{}
	for _, s := range snaps {
		connRows[s.ConnectionID] = append(connRows[s.ConnectionID], s)
	}
	heat := make(map[string]int64)
	type histRow struct {
		conn int64
		sql  string
	}
	rows, err := r.pool.Query(ctx, `
		SELECT connection_id, sql_text FROM sys_query_history
		WHERE status = 1 AND created_at >= NOW() - make_interval(days => $1)
		ORDER BY created_at DESC LIMIT 5000`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hist := make([]histRow, 0, 2048)
	for rows.Next() {
		var h histRow
		if err := rows.Scan(&h.conn, &h.sql); err != nil {
			return nil, err
		}
		hist = append(hist, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for connID, tables := range connRows {
		patterns := make([]struct {
			key string
			re  *regexp.Regexp
		}, 0, len(tables))
		for _, t := range tables {
			pat := `(?i)(?:\b` + regexp.QuoteMeta(t.SchemaName) + `\s*\.\s*)?\b` + regexp.QuoteMeta(t.TableName) + `\b`
			re, err := regexp.Compile(pat)
			if err != nil {
				continue
			}
			key := heatKey(connID, t.DatabaseName, t.SchemaName, t.TableName)
			patterns = append(patterns, struct {
				key string
				re  *regexp.Regexp
			}{key, re})
		}
		for _, h := range hist {
			if h.conn != connID {
				continue
			}
			for _, p := range patterns {
				if p.re.MatchString(h.sql) {
					heat[p.key]++
				}
			}
		}
	}
	return heat, nil
}

func heatKey(connID int64, db, schema, table string) string {
	return fmt.Sprintf("%d|%s|%s|%s", connID, db, schema, table)
}

// HeatKeyOf 供 handler 拼装相同键。
func HeatKeyOf(connID int64, db, schema, table string) string {
	return heatKey(connID, db, schema, table)
}

func scanAnnotations(rows pgx.Rows) ([]AssetAnnotation, error) {
	out := make([]AssetAnnotation, 0)
	for rows.Next() {
		var a AssetAnnotation
		if err := rows.Scan(&a.ID, &a.ConnectionID, &a.DatabaseName, &a.SchemaName, &a.TableName,
			&a.ColumnName, &a.OwnerUserID, &a.BusinessDesc, &a.Tags, &a.Sensitivity,
			&a.StarredBy, &a.UpdatedBy, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
