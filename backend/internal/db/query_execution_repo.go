package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueryExecution 是持久化执行回执，不保存查询行或可以用于重新执行的 SQL。
type QueryExecution struct {
	RequestID     string    `json:"request_id"`
	ConnectionID  int64     `json:"connection_id"`
	Database      string    `json:"database"`
	Status        string    `json:"status"`
	Transactional bool      `json:"transactional"`
	Kind          string    `json:"kind,omitempty"`
	AffectedRows  int64     `json:"affected_rows"`
	DurationMS    int64     `json:"duration_ms"`
	Message       string    `json:"message,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	RequestHash   string    `json:"-"`
}

// ClaimExecution 通过唯一约束在多进程间原子登记。同一编号永不重新执行。
func (r *QueryControls) ClaimExecution(ctx context.Context, user int64, id, hash string, connection int64, database string, transactional bool) (bool, error) {
	tag, err := r.pool.Exec(ctx, `INSERT INTO sys_query_executions(user_id,request_id,request_hash,connection_id,database_name,transactional)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(user_id,request_id) DO NOTHING`, user, id, hash, connection, database, transactional)
	return err == nil && tag.RowsAffected() == 1, err
}

func (r *QueryControls) Execution(ctx context.Context, user int64, id string) (*QueryExecution, error) {
	e := &QueryExecution{}
	err := r.pool.QueryRow(ctx, `SELECT request_id::text,connection_id,database_name,status,transactional,kind,affected_rows,duration_ms,message,created_at,updated_at,request_hash
		FROM sys_query_executions WHERE user_id=$1 AND request_id=$2`, user, id).Scan(
		&e.RequestID, &e.ConnectionID, &e.Database, &e.Status, &e.Transactional, &e.Kind, &e.AffectedRows, &e.DurationMS, &e.Message, &e.CreatedAt, &e.UpdatedAt, &e.RequestHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// 最长执行时间 120 秒，排队 30 秒；超过 5 分钟仍未落回执时保守处理。
	// 仅改变展示状态，不释放登记，也不把未知结果交给另一进程重跑。
	if (e.Status == "queued" || e.Status == "running") && time.Since(e.UpdatedAt) > 5*time.Minute {
		e.Status = "unknown"
		e.Message = "执行回执未完成，请核实目标数据库；该请求不会自动重新执行。"
	}
	return e, nil
}

func (r *QueryControls) StartExecution(ctx context.Context, user int64, id string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE sys_query_executions SET status='running',updated_at=NOW()
		WHERE user_id=$1 AND request_id=$2 AND status='queued'`, user, id)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrDuplicate
	}
	return err
}

func (r *QueryControls) FinishExecution(ctx context.Context, user int64, e *QueryExecution) error {
	tag, err := r.pool.Exec(ctx, `UPDATE sys_query_executions SET status=$3,kind=$4,affected_rows=$5,duration_ms=$6,message=$7,updated_at=NOW()
		WHERE user_id=$1 AND request_id=$2 AND status IN ('queued','running')`, user, e.RequestID, e.Status, e.Kind, e.AffectedRows, e.DurationMS, e.Message)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrDuplicate
	}
	return err
}

// ExecutionReceiptCounts 是近 24 小时创建的回执状态；不等价于用户待办数量。
type ExecutionReceiptCounts struct {
	Queued  int64 `json:"queued"`
	Running int64 `json:"running"`
	Unknown int64 `json:"unknown"`
	Stale   int64 `json:"stale"`
}

type MetadataPoolMetrics struct {
	MaxConnections          int32   `json:"max_connections"`
	TotalConnections        int32   `json:"total_connections"`
	AcquiredConnections     int32   `json:"acquired_connections"`
	IdleConnections         int32   `json:"idle_connections"`
	AcquireCount            int64   `json:"acquire_total"`
	AcquireDurationSeconds  float64 `json:"acquire_duration_seconds_total"`
	EmptyAcquireCount       int64   `json:"empty_acquire_total"`
	EmptyAcquireWaitSeconds float64 `json:"empty_acquire_wait_seconds_total"`
	CanceledAcquireCount    int64   `json:"canceled_acquire_total"`
}

func (r *QueryControls) MetadataPoolMetrics() MetadataPoolMetrics {
	s := r.pool.Stat()
	return MetadataPoolMetrics{s.MaxConns(), s.TotalConns(), s.AcquiredConns(), s.IdleConns(), s.AcquireCount(), s.AcquireDuration().Seconds(), s.EmptyAcquireCount(), s.EmptyAcquireWaitTime().Seconds(), s.CanceledAcquireCount()}
}

func (r *QueryControls) ExecutionReceiptCounts(ctx context.Context) (ExecutionReceiptCounts, error) {
	var c ExecutionReceiptCounts
	err := r.pool.QueryRow(ctx, `SELECT
		COUNT(*) FILTER(WHERE status='queued' AND updated_at>=NOW()-INTERVAL '5 minutes'),
		COUNT(*) FILTER(WHERE status='running' AND updated_at>=NOW()-INTERVAL '5 minutes'),
		COUNT(*) FILTER(WHERE status='unknown' OR (status IN ('queued','running') AND updated_at<NOW()-INTERVAL '5 minutes')),
		COUNT(*) FILTER(WHERE status IN ('queued','running') AND updated_at<NOW()-INTERVAL '5 minutes')
		FROM sys_query_executions WHERE status IN ('queued','running','unknown') AND created_at>=NOW()-INTERVAL '24 hours'`).Scan(&c.Queued, &c.Running, &c.Unknown, &c.Stale)
	return c, err
}
