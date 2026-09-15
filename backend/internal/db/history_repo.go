package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryHistory 查询历史记录。
type QueryHistory struct {
	ID              int64     `json:"id"`
	ConnectionID    int64     `json:"connection_id"`
	UserID          int64     `json:"user_id"`
	DatabaseName    string    `json:"database_name"`
	SQLText         string    `json:"sql_text"`
	Status          int16     `json:"status"`
	AffectedRows    *int64    `json:"affected_rows,omitempty"`
	ExecutionTimeMS *int      `json:"execution_time_ms,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	CreatedAt       time.Time `json:"created_at"`

	// 联表字段
	ConnectionName string `json:"connection_name,omitempty"`
}

// HistoryRepository 查询历史仓储。
type HistoryRepository struct {
	pool *pgxpool.Pool
}

func NewHistoryRepository(pool *pgxpool.Pool) *HistoryRepository {
	return &HistoryRepository{pool: pool}
}

// Create 写入一条执行记录。
func (r *HistoryRepository) Create(ctx context.Context, h *QueryHistory) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO sys_query_history
			(connection_id, user_id, database_name, sql_text, status,
			 affected_rows, execution_time_ms, error_message)
		VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,NULLIF($8,''))
		RETURNING id, created_at`,
		h.ConnectionID, h.UserID, h.DatabaseName, h.SQLText, h.Status,
		h.AffectedRows, h.ExecutionTimeMS, h.ErrorMessage,
	).Scan(&h.ID, &h.CreatedAt)
	if err != nil {
		return fmt.Errorf("写入查询历史失败: %w", err)
	}
	return nil
}

// List 分页查询历史（管理员可看全部），可按连接/状态过滤。
func (r *HistoryRepository) List(ctx context.Context, userID int64, isAdmin bool, connectionID int64, statusFilter string, limit, offset int) ([]QueryHistory, int64, error) {
	statusArg := ""
	if statusFilter == "success" {
		statusArg = "1"
	} else if statusFilter == "failed" {
		statusArg = "0"
	}
	args := []any{userID, isAdmin, connectionID, statusArg}

	where := `WHERE ($2 OR h.user_id = $1)
		AND ($3 = 0 OR h.connection_id = $3)
		AND ($4 = '' OR h.status::text = $4)`

	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_query_history h `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计查询历史失败: %w", err)
	}

	args = append(args, limit, offset)
	rows, err := r.pool.Query(ctx, `
		SELECT h.id, h.connection_id, h.user_id, COALESCE(h.database_name, ''),
		       h.sql_text, h.status, h.affected_rows, h.execution_time_ms,
		       COALESCE(h.error_message, ''), h.created_at, COALESCE(c.name, '')
		FROM sys_query_history h
		LEFT JOIN sys_connections c ON c.id = h.connection_id
		`+where+`
		ORDER BY h.id DESC
		LIMIT $5 OFFSET $6`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询历史列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]QueryHistory, 0, limit)
	for rows.Next() {
		var h QueryHistory
		if err := rows.Scan(&h.ID, &h.ConnectionID, &h.UserID, &h.DatabaseName,
			&h.SQLText, &h.Status, &h.AffectedRows, &h.ExecutionTimeMS,
			&h.ErrorMessage, &h.CreatedAt, &h.ConnectionName); err != nil {
			return nil, 0, err
		}
		out = append(out, h)
	}
	return out, total, rows.Err()
}

// Delete 删除单条历史（仅本人或管理员）。
func (r *HistoryRepository) Delete(ctx context.Context, id, userID int64, isAdmin bool) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sys_query_history WHERE id = $1 AND ($2 OR user_id = $3)`,
		id, isAdmin, userID)
	if err != nil {
		return fmt.Errorf("删除查询历史失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Clear 清空当前用户的历史（管理员可指定全部）。
func (r *HistoryRepository) Clear(ctx context.Context, userID int64, isAdmin bool) error {
	if isAdmin {
		_, err := r.pool.Exec(ctx, `TRUNCATE sys_query_history`)
		return err
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM sys_query_history WHERE user_id = $1`, userID)
	return err
}

// DailyTrend 近 n 天每天的查询总量、失败量与慢查询量（阈值 200ms）。
type DailyTrend struct {
	Date   string `json:"date"`
	Total  int64  `json:"total"`
	Failed int64  `json:"failed"`
	Slow   int64  `json:"slow"`
}

// SlowThresholdMS 慢查询阈值（毫秒）。
const SlowThresholdMS = 200

// DailyTrend 近 n 天每天的查询总量、失败量、慢查询量。
func (r *HistoryRepository) DailyTrend(ctx context.Context, days int) ([]DailyTrend, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.d::date::text,
		       COUNT(h.id),
		       COUNT(h.id) FILTER (WHERE h.status = 0),
		       COUNT(h.id) FILTER (WHERE h.execution_time_ms >= $2)
		FROM generate_series(CURRENT_DATE - ($1::int - 1), CURRENT_DATE, '1 day') AS d(d)
		LEFT JOIN sys_query_history h ON h.created_at::date = d.d::date
		GROUP BY d.d
		ORDER BY d.d`, days, SlowThresholdMS)
	if err != nil {
		return nil, fmt.Errorf("查询每日趋势失败: %w", err)
	}
	defer rows.Close()
	out := make([]DailyTrend, 0, days)
	for rows.Next() {
		var t DailyTrend
		if err := rows.Scan(&t.Date, &t.Total, &t.Failed, &t.Slow); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ConnRankItem 数据源查询量排行条目。
type ConnRankItem struct {
	ConnectionID int64  `json:"connection_id"`
	Name         string `json:"name"`
	Count        int64  `json:"count"`
}

// ConnectionRank 近 n 天查询量最高的数据源 Top N。
func (r *HistoryRepository) ConnectionRank(ctx context.Context, days, limit int) ([]ConnRankItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT h.connection_id, COALESCE(c.name, '#' || h.connection_id), COUNT(*) AS n
		FROM sys_query_history h
		LEFT JOIN sys_connections c ON c.id = h.connection_id
		WHERE h.created_at >= CURRENT_DATE - ($1::int - 1)
		GROUP BY h.connection_id, c.name
		ORDER BY n DESC
		LIMIT $2`, days, limit)
	if err != nil {
		return nil, fmt.Errorf("查询数据源排行失败: %w", err)
	}
	defer rows.Close()
	out := make([]ConnRankItem, 0, limit)
	for rows.Next() {
		var it ConnRankItem
		if err := rows.Scan(&it.ConnectionID, &it.Name, &it.Count); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// TodayStats 返回今日查询数与活跃用户数。
func (r *HistoryRepository) TodayStats(ctx context.Context) (todayQueries int64, activeUsers int64, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM sys_query_history WHERE created_at >= CURRENT_DATE),
			(SELECT COUNT(DISTINCT user_id) FROM sys_query_history WHERE created_at >= CURRENT_DATE)
	`).Scan(&todayQueries, &activeUsers)
	return
}

// RecentQueries 取最近的执行记录（普通用户仅本人，admin 全部）。
func (r *HistoryRepository) RecentQueries(ctx context.Context, userID int64, isAdmin bool, limit int) ([]QueryHistory, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT h.id, h.connection_id, h.user_id, COALESCE(h.database_name, ''),
		       h.sql_text, h.status, h.affected_rows, h.execution_time_ms,
		       COALESCE(h.error_message, ''), h.created_at, COALESCE(c.name, '')
		FROM sys_query_history h
		LEFT JOIN sys_connections c ON c.id = h.connection_id
		WHERE $2 OR h.user_id = $1
		ORDER BY h.id DESC
		LIMIT $3`, userID, isAdmin, limit)
	if err != nil {
		return nil, fmt.Errorf("查询最近执行记录失败: %w", err)
	}
	defer rows.Close()
	out := make([]QueryHistory, 0, limit)
	for rows.Next() {
		var h QueryHistory
		if err := rows.Scan(&h.ID, &h.ConnectionID, &h.UserID, &h.DatabaseName,
			&h.SQLText, &h.Status, &h.AffectedRows, &h.ExecutionTimeMS,
			&h.ErrorMessage, &h.CreatedAt, &h.ConnectionName); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
