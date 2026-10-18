package db

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LineageEdge 血缘边（缓存表 sys_meta_lineage 一行）
type LineageEdge struct {
	ID                     int64     `json:"id"`
	UpstreamConnectionID   int64     `json:"upstream_connection_id"`
	UpstreamDatabase       string    `json:"upstream_database"`
	UpstreamSchema         string    `json:"upstream_schema"`
	UpstreamTable          string    `json:"upstream_table"`
	DownstreamConnectionID int64     `json:"downstream_connection_id"`
	DownstreamDatabase     string    `json:"downstream_database"`
	DownstreamSchema       string    `json:"downstream_schema"`
	DownstreamTable        string    `json:"downstream_table"`
	EdgeKind               string    `json:"edge_kind"` // fk | sql | report | dashboard
	Fingerprint            string    `json:"fingerprint"`
	SourceHistoryID        *int64    `json:"source_history_id,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// LineageRepository 血缘缓存仓储
type LineageRepository struct {
	pool *ScopedPool
}

func NewLineageRepository(pool *pgxpool.Pool) *LineageRepository {
	return &LineageRepository{pool: scopedPool(pool)}
}

// Fingerprint 生成边指纹（上下游四元组+kind）
func LineageFingerprint(upConn int64, upDB, upSchema, upTable string, downConn int64, downDB, downSchema, downTable, kind, sqlNorm string) string {
	raw := fmt.Sprintf("%d|%s|%s|%s|%d|%s|%s|%s|%s|%s",
		upConn, strings.ToLower(upDB), strings.ToLower(upSchema), strings.ToLower(upTable),
		downConn, strings.ToLower(downDB), strings.ToLower(downSchema), strings.ToLower(downTable),
		strings.ToLower(kind), strings.ToLower(strings.TrimSpace(sqlNorm)))
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:8])
}

// UpsertEdge 插入或更新一条血缘边（幂等）
func (r *LineageRepository) UpsertEdge(ctx context.Context, e *LineageEdge) error {
	if e.EdgeKind == "" {
		e.EdgeKind = "sql"
	}
	if e.Fingerprint == "" {
		e.Fingerprint = LineageFingerprint(
			e.UpstreamConnectionID, e.UpstreamDatabase, e.UpstreamSchema, e.UpstreamTable,
			e.DownstreamConnectionID, e.DownstreamDatabase, e.DownstreamSchema, e.DownstreamTable,
			e.EdgeKind, e.Fingerprint,
		)
	}
	// sys_meta_lineage 已在 0012 扩展唯一索引 uq_lineage_edge_v2
	err := r.pool.QueryRow(ctx, `
		INSERT INTO sys_meta_lineage
			(upstream_connection_id, upstream_database, upstream_schema, upstream_table,
			 downstream_connection_id, downstream_database, downstream_schema, downstream_table,
			 edge_kind, sql_fingerprint, source_history_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, NOW(), NOW())
		ON CONFLICT (upstream_connection_id, upstream_database, upstream_schema, upstream_table,
		             downstream_connection_id, downstream_database, downstream_schema, downstream_table,
		             edge_kind, sql_fingerprint) DO UPDATE SET
			source_history_id = COALESCE(EXCLUDED.source_history_id, sys_meta_lineage.source_history_id),
			updated_at = NOW()
		RETURNING id, created_at, updated_at`,
		e.UpstreamConnectionID, e.UpstreamDatabase, e.UpstreamSchema, e.UpstreamTable,
		e.DownstreamConnectionID, e.DownstreamDatabase, e.DownstreamSchema, e.DownstreamTable,
		e.EdgeKind, e.Fingerprint, e.SourceHistoryID,
	).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		// 兼容旧唯一约束 uq_lineage_edge（无 fingerprint）
		// 降级重试不带 fingerprint 的冲突处理
		if strings.Contains(err.Error(), "uq_lineage_edge") {
			return r.pool.QueryRow(ctx, `
				INSERT INTO sys_meta_lineage
					(upstream_connection_id, upstream_database, upstream_schema, upstream_table,
					 downstream_connection_id, downstream_database, downstream_schema, downstream_table,
					 edge_kind, sql_fingerprint, source_history_id, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, NOW(), NOW())
				ON CONFLICT ON CONSTRAINT uq_lineage_edge DO UPDATE SET
					sql_fingerprint = EXCLUDED.sql_fingerprint,
					source_history_id = COALESCE(EXCLUDED.source_history_id, sys_meta_lineage.source_history_id),
					updated_at = NOW()
				RETURNING id, created_at, updated_at`,
				e.UpstreamConnectionID, e.UpstreamDatabase, e.UpstreamSchema, e.UpstreamTable,
				e.DownstreamConnectionID, e.DownstreamDatabase, e.DownstreamSchema, e.DownstreamTable,
				e.EdgeKind, e.Fingerprint, e.SourceHistoryID,
			).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
		}
		return fmt.Errorf("upsert lineage edge failed: %w", err)
	}
	return nil
}

// ListDirectEdges 查询以某表为中心的一度上游或下游边
func (r *LineageRepository) ListDirectEdges(ctx context.Context, connID int64, database, schema, table, direction string) ([]LineageEdge, error) {
	database = strings.TrimSpace(database)
	schema = strings.TrimSpace(schema)
	table = strings.TrimSpace(table)
	if table == "" {
		return nil, fmt.Errorf("table 必填")
	}
	var q string
	var args []any
	if direction == "upstream" {
		// 该表为下游，查其上游
		q = `SELECT id, upstream_connection_id, upstream_database, upstream_schema, upstream_table,
		            downstream_connection_id, downstream_database, downstream_schema, downstream_table,
		            edge_kind, sql_fingerprint, source_history_id, created_at, updated_at
		     FROM sys_meta_lineage
		     WHERE downstream_table ILIKE $1`
		args = append(args, table)
		if connID > 0 {
			q += fmt.Sprintf(` AND downstream_connection_id = $%d`, len(args)+1)
			args = append(args, connID)
		}
		if database != "" {
			q += fmt.Sprintf(` AND downstream_database ILIKE $%d`, len(args)+1)
			args = append(args, database)
		}
		if schema != "" {
			q += fmt.Sprintf(` AND downstream_schema ILIKE $%d`, len(args)+1)
			args = append(args, schema)
		}
	} else {
		// 该表为上游，查其下游
		q = `SELECT id, upstream_connection_id, upstream_database, upstream_schema, upstream_table,
		            downstream_connection_id, downstream_database, downstream_schema, downstream_table,
		            edge_kind, sql_fingerprint, source_history_id, created_at, updated_at
		     FROM sys_meta_lineage
		     WHERE upstream_table ILIKE $1`
		args = append(args, table)
		if connID > 0 {
			q += fmt.Sprintf(` AND upstream_connection_id = $%d`, len(args)+1)
			args = append(args, connID)
		}
		if database != "" {
			q += fmt.Sprintf(` AND upstream_database ILIKE $%d`, len(args)+1)
			args = append(args, database)
		}
		if schema != "" {
			q += fmt.Sprintf(` AND upstream_schema ILIKE $%d`, len(args)+1)
			args = append(args, schema)
		}
	}
	q += ` AND ` + connectionScope(ctx, "upstream_connection_id") + ` AND ` + connectionScope(ctx, "downstream_connection_id")
	q += ` ORDER BY updated_at DESC LIMIT 100`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LineageEdge
	for rows.Next() {
		var e LineageEdge
		if err := rows.Scan(&e.ID, &e.UpstreamConnectionID, &e.UpstreamDatabase, &e.UpstreamSchema, &e.UpstreamTable,
			&e.DownstreamConnectionID, &e.DownstreamDatabase, &e.DownstreamSchema, &e.DownstreamTable,
			&e.EdgeKind, &e.Fingerprint, &e.SourceHistoryID, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListAllEdgesForTable 查询与某表相关的全部边（上游+下游），用于图谱 BFS 初始集合
func (r *LineageRepository) ListAllEdgesForTable(ctx context.Context, connID int64, database, schema, table string) ([]LineageEdge, error) {
	up, err := r.ListDirectEdges(ctx, connID, database, schema, table, "upstream")
	if err != nil {
		return nil, err
	}
	down, err := r.ListDirectEdges(ctx, connID, database, schema, table, "downstream")
	if err != nil {
		return nil, err
	}
	return append(up, down...), nil
}

// RecentEdges 取最近更新的 N 条边（调试/监控）
func (r *LineageRepository) RecentEdges(ctx context.Context, limit int) ([]LineageEdge, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `SELECT id, upstream_connection_id, upstream_database, upstream_schema, upstream_table,
		downstream_connection_id, downstream_database, downstream_schema, downstream_table,
		edge_kind, sql_fingerprint, source_history_id, created_at, updated_at
		FROM sys_meta_lineage WHERE `+connectionScope(ctx, "upstream_connection_id")+` AND `+connectionScope(ctx, "downstream_connection_id")+` ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LineageEdge
	for rows.Next() {
		var e LineageEdge
		if err := rows.Scan(&e.ID, &e.UpstreamConnectionID, &e.UpstreamDatabase, &e.UpstreamSchema, &e.UpstreamTable,
			&e.DownstreamConnectionID, &e.DownstreamDatabase, &e.DownstreamSchema, &e.DownstreamTable,
			&e.EdgeKind, &e.Fingerprint, &e.SourceHistoryID, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneBySource 删除指定历史溯源已失效的边（可选）
func (r *LineageRepository) PruneBySource(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_meta_lineage WHERE updated_at < NOW() - $1::interval`, fmt.Sprintf("%d seconds", int(olderThan.Seconds())))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Count 统计边总数
func (r *LineageRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sys_meta_lineage WHERE `+connectionScope(ctx, "upstream_connection_id")+` AND `+connectionScope(ctx, "downstream_connection_id")).Scan(&n)
	return n, err
}
