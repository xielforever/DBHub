package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditLog 操作审计记录。
type AuditLog struct {
	ID           int64          `json:"id"`
	UserID       *int64         `json:"user_id"`
	ConnectionID *int64         `json:"connection_id"`
	TraceID      string         `json:"trace_id"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceName string         `json:"resource_name"`
	IPAddress    string         `json:"ip_address"`
	UserAgent    string         `json:"user_agent"`
	Status       int16          `json:"status"`
	ErrorMessage string         `json:"error_message,omitempty"`
	DurationMS   *int           `json:"duration_ms,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`

	// 联表字段
	Username string `json:"username"`
}

// AuditRepository 审计日志仓储。
type AuditRepository struct {
	pool *pgxpool.Pool
}

func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository {
	return &AuditRepository{pool: pool}
}

// Create 写入一条审计日志（调用方通常在请求结束后调用，失败只影响日志本身）。
func (r *AuditRepository) Create(ctx context.Context, a *AuditLog) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sys_audit_logs
			(user_id, connection_id, trace_id, action, resource_type, resource_name,
			 ip_address, user_agent, status, error_message, duration_ms, params)
		VALUES ($1,$2,NULLIF($3,''),$4,$5,NULLIF($6,''),
		        NULLIF($7,'')::inet,NULLIF($8,''),$9,NULLIF($10,''),$11,$12)`,
		a.UserID, a.ConnectionID, a.TraceID, a.Action, a.ResourceType, a.ResourceName,
		a.IPAddress, a.UserAgent, a.Status, a.ErrorMessage, a.DurationMS, a.Params)
	if err != nil {
		return fmt.Errorf("写入审计日志失败: %w", err)
	}
	return nil
}

// AuditFilter 审计查询过滤。
type AuditFilter struct {
	Username     string
	Action       string
	ResourceType string
	Status       string // success | failed
	Start        time.Time
	End          time.Time
}

// List 分页查询审计日志（管理员接口）。
func (r *AuditRepository) List(ctx context.Context, f AuditFilter, limit, offset int) ([]AuditLog, int64, error) {
	where := `WHERE ($1 = '' OR u.username ILIKE '%' || $1 || '%')
		AND ($2 = '' OR a.action = $2)
		AND ($3 = '' OR a.resource_type = $3)
		AND ($4 = '' OR a.status::text = $4)
		AND ($5::timestamptz IS NULL OR a.created_at >= $5)
		AND ($6::timestamptz IS NULL OR a.created_at <= $6)`
	args := []any{f.Username, f.Action, f.ResourceType, f.Status, nilTime(f.Start), nilTime(f.End)}

	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sys_audit_logs a LEFT JOIN sys_users u ON u.id = a.user_id `+where,
		args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计审计日志失败: %w", err)
	}

	args = append(args, limit, offset)
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.user_id, a.connection_id, COALESCE(a.trace_id, ''),
		       a.action, COALESCE(a.resource_type, ''), COALESCE(a.resource_name, ''),
		       COALESCE(host(a.ip_address), ''), COALESCE(a.user_agent, ''),
		       a.status, COALESCE(a.error_message, ''), a.duration_ms,
		       COALESCE(a.params::jsonb, '{}'::jsonb), a.created_at,
		       COALESCE(u.username, '')
		FROM sys_audit_logs a
		LEFT JOIN sys_users u ON u.id = a.user_id
		`+where+`
		ORDER BY a.id DESC
		LIMIT $7 OFFSET $8`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询审计日志失败: %w", err)
	}
	defer rows.Close()

	out := make([]AuditLog, 0, limit)
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.UserID, &a.ConnectionID, &a.TraceID,
			&a.Action, &a.ResourceType, &a.ResourceName, &a.IPAddress, &a.UserAgent,
			&a.Status, &a.ErrorMessage, &a.DurationMS, &a.Params, &a.CreatedAt, &a.Username); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// ActionCount 近 n 天按动作统计（仪表盘用）。
func (r *AuditRepository) TotalCount(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sys_audit_logs`).Scan(&n)
	return n, err
}

func nilTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
