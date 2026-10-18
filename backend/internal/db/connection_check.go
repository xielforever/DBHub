package db

import (
	"context"
	"encoding/json"
	"github.com/user/dbhub/internal/connector"
	"time"
)

// ConnectionCheck 只保存连通性与版本结果，禁止保存配置、凭据或原始驱动错误。
type ConnectionCheck struct {
	OK            bool                  `json:"ok"`
	Type          string                `json:"type"`
	Version       string                `json:"version"`
	ElapsedMS     int64                 `json:"elapsed_ms"`
	Compatibility json.RawMessage       `json:"compatibility,omitempty"`
	Error         string                `json:"error,omitempty"`
	Diagnostic    *connector.Diagnostic `json:"diagnostic,omitempty"`
	StartedAt     time.Time             `json:"started_at"`
	CheckedAt     time.Time             `json:"checked_at"`
}

// SaveCheck 忽略旧配置的结果，且较早开始的慢测试不能覆盖更新的检查。
func (r *ConnectionRepository) SaveCheck(ctx context.Context, connectionID int64, version time.Time, result *ConnectionCheck) (bool, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `INSERT INTO sys_connection_checks(connection_id,config_updated_at,started_at,result)
		SELECT id,updated_at,$3,$4::jsonb FROM sys_connections WHERE id=$1 AND updated_at=$2 AND user_id=$5 FOR SHARE
		ON CONFLICT(connection_id) DO UPDATE SET config_updated_at=EXCLUDED.config_updated_at,
		started_at=EXCLUDED.started_at,result=EXCLUDED.result
		WHERE sys_connection_checks.config_updated_at <> EXCLUDED.config_updated_at
		OR sys_connection_checks.started_at <= EXCLUDED.started_at`, connectionID, version, result.StartedAt, string(raw), ActorID(ctx))
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sys_source_check_history(workspace_id,actor_id,connection_id,config_updated_at,result) SELECT workspace_id,user_id,id,$2,$3::jsonb FROM sys_connections WHERE id=$1 AND user_id=$4`, connectionID, version, string(raw), ActorID(ctx))
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
