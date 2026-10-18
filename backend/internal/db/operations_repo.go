package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
)

type OperationState struct {
	Name          string          `json:"name"`
	Status        string          `json:"status"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	LastSuccessAt *time.Time      `json:"last_success_at"`
	Details       json.RawMessage `json:"details"`
	UpdatedAt     time.Time       `json:"updated_at"`
}
type OperationAlert struct {
	Code       string    `json:"code"`
	Active     bool      `json:"active"`
	ObservedAt time.Time `json:"observed_at"`
	ChangedAt  time.Time `json:"changed_at"`
}

// Only platform-admin API handlers call this method; runtime sessions have no
// RLS policy on these platform tables, including within a selected workspace.
func (r *AutomationRepository) Operations(ctx context.Context) ([]OperationState, []OperationAlert, error) {
	rows, e := r.pool.maintenance.Query(ctx, `SELECT to_jsonb(s) FROM sys_operations s ORDER BY name`)
	if e != nil {
		return nil, nil, e
	}
	states := []OperationState{}
	for rows.Next() {
		var raw []byte
		var s OperationState
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, nil, e
		}
		if e = json.Unmarshal(raw, &s); e != nil {
			rows.Close()
			return nil, nil, e
		}
		states = append(states, s)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, nil, e
	}
	rows, e = r.pool.maintenance.Query(ctx, `SELECT code,active,observed_at,changed_at FROM sys_operation_alerts ORDER BY code`)
	if e != nil {
		return nil, nil, e
	}
	defer rows.Close()
	alerts := []OperationAlert{}
	for rows.Next() {
		var a OperationAlert
		if e = rows.Scan(&a.Code, &a.Active, &a.ObservedAt, &a.ChangedAt); e != nil {
			return nil, nil, e
		}
		alerts = append(alerts, a)
	}
	return states, alerts, rows.Err()
}

func (r *AutomationRepository) SetOperationalAlert(ctx context.Context, code string, active bool, title, message string) error {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `INSERT INTO sys_operation_alerts(code) VALUES($1) ON CONFLICT DO NOTHING`, code); e != nil {
		return e
	}
	var wasActive bool
	var epoch int64
	if e = tx.QueryRow(ctx, `SELECT active,epoch FROM sys_operation_alerts WHERE code=$1 FOR UPDATE`, code).Scan(&wasActive, &epoch); e != nil {
		return e
	}
	if wasActive == active {
		_, e = tx.Exec(ctx, `UPDATE sys_operation_alerts SET observed_at=now() WHERE code=$1`, code)
		if e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	epoch++
	if _, e = tx.Exec(ctx, `UPDATE sys_operation_alerts SET active=$2,epoch=$3,observed_at=now(),changed_at=now() WHERE code=$1`, code, active, epoch); e != nil {
		return e
	}
	if !active {
		title = "告警已恢复：" + title
		message = "当前检查已恢复正常，请在运维状态中核对。"
	}
	_, e = tx.Exec(ctx, `SELECT dbhub_notify(m.workspace_id,m.user_id,'operations:'||$1||':'||$2::bigint::text,'operations',$3,$4,'operations',0) FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id JOIN sys_workspaces w ON w.id=m.workspace_id WHERE m.active AND u.is_active AND u.is_platform_admin AND w.enabled`, code, epoch, title, message)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// PurgeRetention performs at most 500 rows per table per hourly run. Configuration
// is opt-in for audit/history; unknown outcomes and immutable domain history stay.
func (r *AutomationRepository) PurgeRetention(ctx context.Context, cfg config.OperationsConfig) (map[string]int64, error) {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var lock bool
	if e = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended(current_database()||':'||current_schema()||':retention',0))`).Scan(&lock); e != nil {
		return nil, e
	}
	if !lock {
		return nil, nil
	}
	var recent bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_operations WHERE name='retention' AND last_success_at>now()-interval '1 hour')`).Scan(&recent); e != nil {
		return nil, e
	}
	if recent {
		return nil, nil
	}
	counts := map[string]int64{"audit": 0, "query_history": 0, "read_notifications": 0, "source_checks": 0, "source_tasks": 0}
	if cfg.AuditRetentionDays > 0 {
		tag, e := tx.Exec(ctx, `DELETE FROM sys_audit_logs a USING (SELECT tableoid AS oid,ctid AS tid FROM sys_audit_logs WHERE created_at<now()-make_interval(days=>$1) AND COALESCE(context->>'outcome','') NOT IN ('unknown','in_progress') ORDER BY created_at LIMIT 500 FOR UPDATE SKIP LOCKED) old WHERE a.tableoid=old.oid AND a.ctid=old.tid`, cfg.AuditRetentionDays)
		if e != nil {
			return nil, e
		}
		counts["audit"] = tag.RowsAffected()
	}
	if cfg.HistoryRetentionDays > 0 {
		tag, e := tx.Exec(ctx, `DELETE FROM sys_query_history WHERE id IN (SELECT h.id FROM sys_query_history h WHERE h.created_at<now()-make_interval(days=>$1) AND NOT EXISTS(SELECT 1 FROM sys_query_executions e WHERE e.workspace_id=h.workspace_id AND e.user_id=h.user_id AND e.connection_id=h.connection_id AND e.status IN ('queued','running','unknown')) ORDER BY h.created_at LIMIT 500 FOR UPDATE OF h SKIP LOCKED)`, cfg.HistoryRetentionDays)
		if e != nil {
			return nil, e
		}
		counts["query_history"] = tag.RowsAffected()
		tag, e = tx.Exec(ctx, `DELETE FROM sys_source_check_history WHERE id IN (SELECT h.id FROM sys_source_check_history h LEFT JOIN sys_source_tasks t ON t.id=h.task_id WHERE h.created_at<now()-make_interval(days=>$1) AND (h.task_id IS NULL OR (t.status IN ('succeeded','failed','cancelled','stale','skipped') AND t.finished_at<now()-make_interval(days=>$1))) ORDER BY h.id LIMIT 500 FOR UPDATE OF h SKIP LOCKED)`, cfg.HistoryRetentionDays)
		if e != nil {
			return nil, e
		}
		counts["source_checks"] = tag.RowsAffected()
		tag, e = tx.Exec(ctx, `DELETE FROM sys_source_tasks WHERE id IN (SELECT t.id FROM sys_source_tasks t WHERE t.status IN ('succeeded','failed','cancelled','stale','skipped') AND t.finished_at<now()-make_interval(days=>$1) AND NOT EXISTS(SELECT 1 FROM sys_source_check_history h WHERE h.task_id=t.id) AND NOT EXISTS(SELECT 1 FROM sys_source_tasks child WHERE child.parent_id=t.id) ORDER BY t.finished_at LIMIT 500 FOR UPDATE SKIP LOCKED)`, cfg.HistoryRetentionDays)
		if e != nil {
			return nil, e
		}
		counts["source_tasks"] = tag.RowsAffected()
	}
	if cfg.ReadNotificationRetentionDays > 0 {
		tag, e := tx.Exec(ctx, `DELETE FROM sys_notifications WHERE id IN (SELECT id FROM sys_notifications WHERE created_at<now()-make_interval(days=>$1) AND read_at IS NOT NULL AND read_at<now()-make_interval(days=>$1) ORDER BY id LIMIT 500 FOR UPDATE SKIP LOCKED)`, cfg.ReadNotificationRetentionDays)
		if e != nil {
			return nil, e
		}
		counts["read_notifications"] = tag.RowsAffected()
	}
	raw, _ := json.Marshal(counts)
	_, e = tx.Exec(ctx, `INSERT INTO sys_operations(name,status,finished_at,last_success_at,details) VALUES('retention','succeeded',now(),now(),$1::jsonb) ON CONFLICT(name) DO UPDATE SET status='succeeded',started_at=now(),finished_at=now(),last_success_at=now(),details=EXCLUDED.details,updated_at=now()`, string(raw))
	if e != nil {
		return nil, e
	}
	if counts["audit"]+counts["query_history"]+counts["read_notifications"]+counts["source_checks"]+counts["source_tasks"] > 0 {
		_, e = tx.Exec(ctx, `INSERT INTO sys_audit_logs(event_id,created_at,workspace_id,action,resource_type,status,context) VALUES($1::uuid,now(),0,'RETENTION_PURGE','OPERATIONS',1,jsonb_build_object('actor_username','系统维护','actor_role','system','outcome','succeeded','deleted_counts',$2::jsonb))`, dashboardUUID(), string(raw))
		if e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return counts, nil
}

func (r *AutomationRepository) WorkerHeartbeat(ctx context.Context) error {
	_, e := r.pool.maintenance.Exec(ctx, `INSERT INTO sys_operations(name,status,finished_at,last_success_at,details) VALUES('source_worker','succeeded',now(),now(),jsonb_build_object('global_workers',2)) ON CONFLICT(name) DO UPDATE SET status='succeeded',finished_at=now(),last_success_at=now(),updated_at=now()`)
	return e
}

func (r *AutomationRepository) BackupState(ctx context.Context) (*OperationState, error) {
	var raw []byte
	e := r.pool.maintenance.QueryRow(ctx, `SELECT to_jsonb(s) FROM sys_operations s WHERE name='backup'`).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var out OperationState
	e = json.Unmarshal(raw, &out)
	return &out, e
}
