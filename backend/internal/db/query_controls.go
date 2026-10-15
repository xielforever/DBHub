package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type QueryControls struct{ pool *pgxpool.Pool }

func NewQueryControls(pool *pgxpool.Pool) *QueryControls { return &QueryControls{pool} }

type QuerySettings struct {
	TimeoutMS        int  `json:"timeout_ms"`
	MaxRows          int  `json:"max_rows"`
	BlockUnsafeWrite bool `json:"block_unsafe_write"`
}

func (r *QueryControls) Settings(ctx context.Context, uid int64) (QuerySettings, error) {
	s := QuerySettings{30000, 1000, true}
	rows, e := r.pool.Query(ctx, `SELECT timeout_ms,max_rows,block_unsafe_write FROM sys_query_settings WHERE user_id=$1`, uid)
	if e != nil {
		return s, e
	}
	defer rows.Close()
	if rows.Next() {
		e = rows.Scan(&s.TimeoutMS, &s.MaxRows, &s.BlockUnsafeWrite)
	}
	return s, e
}
func (r *QueryControls) SaveSettings(ctx context.Context, uid int64, s QuerySettings) error {
	_, e := r.pool.Exec(ctx, `INSERT INTO sys_query_settings(user_id,timeout_ms,max_rows,block_unsafe_write) VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET timeout_ms=$2,max_rows=$3,block_unsafe_write=$4,updated_at=NOW()`, uid, s.TimeoutMS, s.MaxRows, s.BlockUnsafeWrite)
	return e
}

type RowCondition struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}
type QueryPolicy struct {
	ID           int64             `json:"id"`
	ConnectionID int64             `json:"connection_id"`
	Database     string            `json:"database_name"`
	Schema       string            `json:"schema_name"`
	Table        string            `json:"table_name"`
	Role         string            `json:"role_code"`
	Columns      map[string]string `json:"column_permissions"`
	Conditions   []RowCondition    `json:"row_conditions"`
}

func (r *QueryControls) Policies(ctx context.Context, cid int64, role string) ([]QueryPolicy, error) {
	rows, e := r.pool.Query(ctx, `SELECT id,connection_id,database_name,schema_name,table_name,role_code,column_permissions,row_conditions FROM sys_query_policies WHERE connection_id=$1 AND ($2='' OR role_code=$2) ORDER BY id`, cid, role)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []QueryPolicy{}
	for rows.Next() {
		var p QueryPolicy
		var cols, conds []byte
		if e = rows.Scan(&p.ID, &p.ConnectionID, &p.Database, &p.Schema, &p.Table, &p.Role, &cols, &conds); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(cols, &p.Columns); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(conds, &p.Conditions); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *QueryControls) SavePolicy(ctx context.Context, p *QueryPolicy, uid int64) error {
	cols, _ := json.Marshal(p.Columns)
	conds, _ := json.Marshal(p.Conditions)
	return r.pool.QueryRow(ctx, `INSERT INTO sys_query_policies(connection_id,database_name,schema_name,table_name,role_code,column_permissions,row_conditions,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(connection_id,database_name,schema_name,table_name,role_code) DO UPDATE SET column_permissions=$6,row_conditions=$7,updated_by=$8,updated_at=NOW() RETURNING id`, p.ConnectionID, p.Database, p.Schema, p.Table, p.Role, string(cols), string(conds), uid).Scan(&p.ID)
}
func (r *QueryControls) DeletePolicy(ctx context.Context, id int64) error {
	tag, e := r.pool.Exec(ctx, `DELETE FROM sys_query_policies WHERE id=$1`, id)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}

type QueryShare struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	ExpireAt    time.Time `json:"expire_at"`
	Revoked     bool      `json:"revoked"`
	AccessCount int       `json:"access_count"`
	CreatedAt   time.Time `json:"created_at"`
}

func (r *QueryControls) CreateShare(ctx context.Context, uid int64, token, name string, payload any, expiry time.Time) (QueryShare, error) {
	s := QueryShare{Name: name, ExpireAt: expiry}
	raw, e := json.Marshal(payload)
	if e != nil {
		return s, e
	}
	e = r.pool.QueryRow(ctx, `INSERT INTO sys_query_shares(token_hash,user_id,name,payload,expire_at)VALUES($1,$2,$3,$4,$5) RETURNING id,created_at`, HashToken(token), uid, name, string(raw), expiry).Scan(&s.ID, &s.CreatedAt)
	return s, e
}
func (r *QueryControls) ListShares(ctx context.Context, uid int64) ([]QueryShare, error) {
	rows, e := r.pool.Query(ctx, `SELECT id,name,expire_at,revoked,access_count,created_at FROM sys_query_shares WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`, uid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []QueryShare{}
	for rows.Next() {
		var s QueryShare
		if e = rows.Scan(&s.ID, &s.Name, &s.ExpireAt, &s.Revoked, &s.AccessCount, &s.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *QueryControls) RevokeShare(ctx context.Context, uid, id int64) error {
	tag, e := r.pool.Exec(ctx, `UPDATE sys_query_shares SET revoked=TRUE WHERE id=$1 AND user_id=$2`, id, uid)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}
func (r *QueryControls) ReadShare(ctx context.Context, token string) (json.RawMessage, error) {
	var raw []byte
	e := r.pool.QueryRow(ctx, `UPDATE sys_query_shares SET access_count=access_count+1 WHERE token_hash=$1 AND NOT revoked AND expire_at>NOW() RETURNING payload`, HashToken(token)).Scan(&raw)
	return raw, e
}
