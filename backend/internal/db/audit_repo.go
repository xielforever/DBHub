package db

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/user/dbhub/internal/auditctx"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditLog 操作审计记录。
type AuditLog struct {
	Context      auditctx.Data  `json:"context"`
	EventID      string         `json:"event_id,omitempty"`
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

// Create inserts a stable event identity; replay after an uncertain ACK is safe.
func (r *AuditRepository) Create(ctx context.Context, a *AuditLog) error {
	if a.EventID == "" || a.CreatedAt.IsZero() {
		return fmt.Errorf("审计事件缺少持久化标识或时间")
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sys_audit_logs
			(user_id, connection_id, trace_id, action, resource_type, resource_name,
			 ip_address, user_agent, status, error_message, duration_ms, params, context, event_id, created_at)
		VALUES ($1,$2,NULLIF($3,''),$4,$5,NULLIF($6,''),
		        NULLIF($7,'')::inet,NULLIF($8,''),$9,NULLIF($10,''),$11,$12,$13::jsonb,$14::uuid,$15)
		ON CONFLICT (event_id, created_at) DO NOTHING`,
		a.UserID, a.ConnectionID, a.TraceID, a.Action, a.ResourceType, a.ResourceName,
		a.IPAddress, a.UserAgent, a.Status, a.ErrorMessage, a.DurationMS, nil, auditJSON(a.Context), a.EventID, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("写入审计日志失败: %w", err)
	}
	return nil
}

// AuditFilter 审计查询过滤。
type AuditFilter struct {
	DatabaseAccount string
	Outcome         string
	Username        string
	Action          string
	ResourceType    string
	Status          string // success | failed
	Start           time.Time
	End             time.Time
}

// List 分页查询审计日志（管理员接口）。
func (r *AuditRepository) List(ctx context.Context, f AuditFilter, limit, offset int) ([]AuditLog, int64, error) {
	where := `WHERE ($1 = '' OR COALESCE(NULLIF(a.context->>'actor_username',''),u.username,'') ILIKE '%' || $1 || '%')
		AND ($2 = '' OR a.action = $2)
		AND ($3 = '' OR a.resource_type = $3)
		AND ($4 = '' OR a.status::text = $4)
		AND ($5::timestamptz IS NULL OR a.created_at >= $5)
		AND ($6::timestamptz IS NULL OR a.created_at <= $6) AND ($7='' OR a.context->>'database_account' ILIKE '%'||$7||'%') AND ($8='' OR a.context->>'outcome'=$8)`
	args := []any{f.Username, f.Action, f.ResourceType, f.Status, nilTime(f.Start), nilTime(f.End), f.DatabaseAccount, f.Outcome}

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
		       COALESCE(NULLIF(a.context->>'actor_username',''),u.username,''),a.context,COALESCE(a.event_id::text,'')
		FROM sys_audit_logs a
		LEFT JOIN sys_users u ON u.id = a.user_id
		`+where+`
		ORDER BY a.id DESC
		LIMIT $9 OFFSET $10`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询审计日志失败: %w", err)
	}
	defer rows.Close()

	out := make([]AuditLog, 0, limit)
	for rows.Next() {
		var a AuditLog
		var raw []byte
		if err := rows.Scan(&a.ID, &a.UserID, &a.ConnectionID, &a.TraceID,
			&a.Action, &a.ResourceType, &a.ResourceName, &a.IPAddress, &a.UserAgent,
			&a.Status, &a.ErrorMessage, &a.DurationMS, &a.Params, &a.CreatedAt, &a.Username, &raw, &a.EventID); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal(raw, &a.Context); err != nil {
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

func auditJSON(d auditctx.Data) string {
	// PostgreSQL JSONB rejects U+0000. Unpersisted connection tests can carry
	// such metadata; preserve it as explicitly quoted text instead of poisoning
	// the durable queue. This also repairs delivery of older queued snapshots.
	d.ActorUsername = auditText(d.ActorUsername)
	d.ActorRole = auditText(d.ActorRole)
	d.SourceFamilyID = auditText(d.SourceFamilyID)
	d.DatabaseAccount = auditText(d.DatabaseAccount)
	d.Driver = auditText(d.Driver)
	d.ExecutionRequestID = auditText(d.ExecutionRequestID)
	d.TransactionID = auditText(d.TransactionID)
	d.JobID = auditText(d.JobID)
	d.AuthFailure = auditText(d.AuthFailure)
	d.Route = auditText(d.Route)
	d.Outcome = auditText(d.Outcome)
	raw, _ := json.Marshal(d)
	return string(raw)
}

func auditText(value string) string {
	if strings.ContainsRune(value, 0) {
		return strconv.Quote(value)
	}
	return value
}
