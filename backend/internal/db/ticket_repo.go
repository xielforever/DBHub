package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ticket 变更工单
type Ticket struct {
	ID              int64           `json:"id"`
	Title           string          `json:"title"`
	Type            string          `json:"type"` // ddl | dml | index
	TableName       string          `json:"table"`
	DatabaseName    string          `json:"database_name"`
	ConnectionID    *int64          `json:"connection_id,omitempty"`
	SQLText         string          `json:"sql"`
	Risk            string          `json:"risk"`   // low | medium | high
	Status          string          `json:"status"` // pending | approved | rejected | executed
	RequesterID     int64           `json:"requester_id"`
	Requester       string          `json:"requester"`
	Reason          string          `json:"reason"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ExecutedBy      *int64          `json:"executed_by,omitempty"`
	ExecutedAt      *time.Time      `json:"executed_at,omitempty"`
	ExecutionResult json.RawMessage `json:"execution_result,omitempty"`
	ExecutionError  string          `json:"execution_error,omitempty"`
}

// TicketFilter 列表过滤
type TicketFilter struct {
	Status  string
	Keyword string
	Type    string
	Offset  int
	Limit   int
}

type TicketRepository struct {
	pool *pgxpool.Pool
}

func NewTicketRepository(pool *pgxpool.Pool) *TicketRepository {
	return &TicketRepository{pool: pool}
}

func (r *TicketRepository) Create(ctx context.Context, t *Ticket) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO sys_tickets
		(title, type, table_name, database_name, connection_id, sql_text, risk, status, requester_id, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at, updated_at`,
		t.Title, t.Type, t.TableName, t.DatabaseName, t.ConnectionID, t.SQLText, t.Risk, t.Status, t.RequesterID, t.Reason,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

func (r *TicketRepository) Get(ctx context.Context, id int64) (*Ticket, error) {
	t := &Ticket{}
	var connID *int64
	err := r.pool.QueryRow(ctx, `
		SELECT t.id, t.title, t.type, t.table_name, COALESCE(t.database_name,''), t.connection_id,
		       t.sql_text, t.risk, t.status, t.requester_id, COALESCE(u.username,''),
		       COALESCE(t.reason,''), t.created_at, t.updated_at,t.executed_by,t.executed_at,t.execution_result,COALESCE(t.execution_error,'')
		FROM sys_tickets t
		LEFT JOIN sys_users u ON u.id = t.requester_id
		WHERE t.id = $1`, id).Scan(
		&t.ID, &t.Title, &t.Type, &t.TableName, &t.DatabaseName, &connID,
		&t.SQLText, &t.Risk, &t.Status, &t.RequesterID, &t.Requester,
		&t.Reason, &t.CreatedAt, &t.UpdatedAt, &t.ExecutedBy, &t.ExecutedAt, &t.ExecutionResult, &t.ExecutionError)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.ConnectionID = connID
	return t, nil
}

func (r *TicketRepository) List(ctx context.Context, f TicketFilter) ([]Ticket, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	conds := []string{"1=1"}
	args := []any{}
	add := func(tmpl string, vals ...any) {
		for _, v := range vals {
			args = append(args, v)
			tmpl = strings.Replace(tmpl, "?", fmt.Sprintf("$%d", len(args)), 1)
		}
		conds = append(conds, tmpl)
	}
	if f.Status != "" {
		add("t.status = ?", f.Status)
	}
	if f.Type != "" {
		add("t.type = ?", f.Type)
	}
	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		add("(t.title ILIKE ? OR t.table_name ILIKE ? OR t.sql_text ILIKE ?)", like, like, like)
	}
	where := strings.Join(conds, " AND ")
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sys_tickets t WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计工单失败: %w", err)
	}
	q := fmt.Sprintf(`
		SELECT t.id, t.title, t.type, t.table_name, COALESCE(t.database_name,''), t.connection_id,
		       t.sql_text, t.risk, t.status, t.requester_id, COALESCE(u.username,''),
		       COALESCE(t.reason,''), t.created_at, t.updated_at,t.executed_by,t.executed_at,t.execution_result,COALESCE(t.execution_error,'')
		FROM sys_tickets t
		LEFT JOIN sys_users u ON u.id = t.requester_id
		WHERE %s
		ORDER BY t.created_at DESC
		LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2)
	args = append(args, f.Limit, f.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询工单失败: %w", err)
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		var t Ticket
		var connID *int64
		if err := rows.Scan(&t.ID, &t.Title, &t.Type, &t.TableName, &t.DatabaseName, &connID,
			&t.SQLText, &t.Risk, &t.Status, &t.RequesterID, &t.Requester,
			&t.Reason, &t.CreatedAt, &t.UpdatedAt, &t.ExecutedBy, &t.ExecutedAt, &t.ExecutionResult, &t.ExecutionError); err != nil {
			return nil, 0, err
		}
		t.ConnectionID = connID
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// Review atomically transitions a pending ticket; execution is a separate action.
func (r *TicketRepository) Review(ctx context.Context, id int64, status string, uid int64) error {
	tag, e := r.pool.Exec(ctx, `UPDATE sys_tickets SET status=$2,approved_by=$3,updated_at=NOW() WHERE id=$1 AND status='pending'`, id, status, uid)
	if e == nil && tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return e
}
func (r *TicketRepository) ClaimExecution(ctx context.Context, id, uid int64) error {
	tag, e := r.pool.Exec(ctx, `UPDATE sys_tickets SET status='executing',executed_by=$2,updated_at=NOW() WHERE id=$1 AND status='approved' AND approved_by IS NOT NULL`, id, uid)
	if e == nil && tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return e
}
func (r *TicketRepository) FinishExecution(ctx context.Context, id int64, result any, message string) error {
	status := "executed"
	if message != "" {
		status = "failed"
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return e
	}
	_, e = r.pool.Exec(ctx, `UPDATE sys_tickets SET status=$2,execution_result=$3,execution_error=$4,executed_at=NOW(),updated_at=NOW() WHERE id=$1 AND status='executing'`, id, status, string(raw), message)
	return e
}
