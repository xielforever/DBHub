package db

import (
	"context"
	"encoding/json"
	"time"
)

type ExportRequest struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Format    string    `json:"format"`
	Rows      int       `json:"rows"`
	Reason    string    `json:"reason"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func (r *QueryControls) CreateExport(ctx context.Context, uid, cid int64, format, reason string, count int, payload any) (int64, error) {
	raw, e := json.Marshal(payload)
	if e != nil {
		return 0, e
	}
	var id int64
	e = r.pool.QueryRow(ctx, `INSERT INTO sys_export_requests(user_id,connection_id,format,row_count,reason,payload)VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, uid, cid, format, count, reason, string(raw)).Scan(&id)
	return id, e
}
func (r *QueryControls) ListExports(ctx context.Context, uid int64, admin bool) ([]ExportRequest, error) {
	rows, e := r.pool.Query(ctx, `SELECT id,user_id,format,row_count,reason,status,created_at FROM sys_export_requests WHERE user_id=$1 OR $2 ORDER BY created_at DESC LIMIT 100`, uid, admin)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ExportRequest{}
	for rows.Next() {
		var x ExportRequest
		if e = rows.Scan(&x.ID, &x.UserID, &x.Format, &x.Rows, &x.Reason, &x.Status, &x.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (r *QueryControls) ReviewExport(ctx context.Context, id, uid int64, status string) error {
	tag, e := r.pool.Exec(ctx, `UPDATE sys_export_requests SET status=$2,reviewed_by=$3 WHERE id=$1 AND status='pending'`, id, status, uid)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}
func (r *QueryControls) DownloadExport(ctx context.Context, id, uid int64, admin bool) (string, json.RawMessage, int64, error) {
	var format string
	var raw []byte
	var connectionID int64
	e := r.pool.QueryRow(ctx, `SELECT format,payload,connection_id FROM sys_export_requests WHERE id=$1 AND (user_id=$2 OR $3) AND status='approved' AND `+connectionScope(ctx, "connection_id"), id, uid, false).Scan(&format, &raw, &connectionID)
	return format, raw, connectionID, e
}
