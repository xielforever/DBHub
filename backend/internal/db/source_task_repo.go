package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SourceTask struct {
	ID              string          `json:"id"`
	WorkspaceID     int64           `json:"workspace_id"`
	ActorID         int64           `json:"actor_id"`
	ConnectionID    int64           `json:"connection_id"`
	ConnectionName  string          `json:"connection_name"`
	Kind            string          `json:"kind"`
	Status          string          `json:"status"`
	Progress        int             `json:"progress"`
	Phase           string          `json:"phase"`
	RequestID       string          `json:"request_id"`
	RequestHash     string          `json:"-"`
	ParentID        *string         `json:"parent_id"`
	ScheduleID      *int64          `json:"schedule_id"`
	MemberVersion   int64           `json:"member_version"`
	ConfigUpdatedAt time.Time       `json:"config_updated_at"`
	WorkerToken     string          `json:"-"`
	LeaseUntil      *time.Time      `json:"lease_until,omitempty"`
	Result          json.RawMessage `json:"result"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at"`
	FinishedAt      *time.Time      `json:"finished_at"`
}

type SourceSchedule struct {
	ID              int64      `json:"id"`
	WorkspaceID     int64      `json:"workspace_id"`
	ActorID         int64      `json:"actor_id"`
	ConnectionID    int64      `json:"connection_id"`
	Kind            string     `json:"kind"`
	IntervalMinutes int        `json:"interval_minutes"`
	Enabled         bool       `json:"enabled"`
	Version         int64      `json:"version"`
	MemberVersion   int64      `json:"member_version"`
	ConfigUpdatedAt time.Time  `json:"config_updated_at"`
	NextRunAt       time.Time  `json:"next_run_at"`
	LastRunAt       *time.Time `json:"last_run_at"`
	LastStatus      string     `json:"last_status"`
}

const sourceQueueLock = `SELECT pg_advisory_xact_lock(hashtextextended(current_database()||':'||current_schema()||':source-task-queue',0))`

func scanSourceTask(row pgx.Row) (*SourceTask, error) {
	var raw []byte
	var token, hash string
	if e := row.Scan(&raw, &token, &hash); errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if e != nil {
		return nil, e
	}
	var out SourceTask
	if e := json.Unmarshal(raw, &out); e != nil {
		return nil, e
	}
	out.WorkerToken, out.RequestHash = token, hash
	return &out, nil
}

const sourceTaskColumns = `to_jsonb(t),COALESCE(t.worker_token::text,''),t.request_hash`

func (r *AutomationRepository) SourceTask(ctx context.Context, id string) (*SourceTask, error) {
	return scanSourceTask(r.pool.QueryRow(ctx, `SELECT `+sourceTaskColumns+` FROM sys_source_tasks t WHERE t.id=$1::uuid AND t.actor_id=$2`, id, ActorID(ctx)))
}

func (r *AutomationRepository) SourceTasks(ctx context.Context, status string, limit, offset int) ([]SourceTask, int64, error) {
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_source_tasks WHERE actor_id=$1 AND ($2='' OR status=$2)`, ActorID(ctx), status).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT `+sourceTaskColumns+` FROM sys_source_tasks t WHERE t.actor_id=$1 AND ($2='' OR t.status=$2) ORDER BY t.created_at DESC,t.id DESC LIMIT $3 OFFSET $4`, ActorID(ctx), status, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []SourceTask{}
	for rows.Next() {
		t, e := scanSourceTask(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, *t)
	}
	return out, total, rows.Err()
}

func taskHash(cid int64, kind, parent string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", cid, kind, parent)))
	return hex.EncodeToString(sum[:])
}

// enqueueSourceTask runs under the shared queue lock; row visibility and source
// ownership still use the caller's restricted runtime transaction.
func enqueueSourceTask(ctx context.Context, tx pgx.Tx, conn *Connection, kind, requestID, parent string, schedule *int64) (*SourceTask, error) {
	hash := taskHash(conn.ID, kind, parent)
	existing, e := scanSourceTask(tx.QueryRow(ctx, `SELECT `+sourceTaskColumns+` FROM sys_source_tasks t WHERE t.actor_id=$1 AND t.request_id=$2::uuid`, ActorID(ctx), requestID))
	if e == nil {
		if existing.RequestHash != hash {
			return nil, ErrAutomationConflict
		}
		return existing, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return nil, e
	}
	var userCount, workspaceCount int
	var capacity bool
	if e = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE actor_id=$1),count(*),dbhub_source_queue_capacity() FROM sys_source_tasks WHERE status IN ('queued','running','cancel_requested')`, ActorID(ctx)).Scan(&userCount, &workspaceCount, &capacity); e != nil {
		return nil, e
	}
	// RLS intentionally hides other users. Workspace capacity is checked through
	// the narrowly scoped helper below, not an owner fallback connection.
	if e = tx.QueryRow(ctx, `SELECT dbhub_source_workspace_capacity()`).Scan(&capacity); e != nil {
		return nil, e
	}
	if !capacity || userCount >= 10 {
		return nil, ErrAutomationLimit
	}
	if e = tx.QueryRow(ctx, `SELECT dbhub_source_queue_capacity()`).Scan(&capacity); e != nil {
		return nil, e
	}
	if !capacity {
		return nil, ErrAutomationLimit
	}
	var active bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_source_tasks WHERE connection_id=$1 AND kind=$2 AND status IN ('queued','running','cancel_requested'))`, conn.ID, kind).Scan(&active); e != nil {
		return nil, e
	}
	if active {
		return nil, ErrAutomationConflict
	}
	var parentID any
	if parent != "" {
		parentID = parent
	}
	id := dashboardUUID()
	out, e := scanSourceTask(tx.QueryRow(ctx, `INSERT INTO sys_source_tasks AS t(id,workspace_id,actor_id,connection_id,connection_name,kind,request_id,request_hash,parent_id,schedule_id,member_version,config_updated_at) VALUES($1::uuid,$2,$3,$4,$5,$6,$7::uuid,$8,$9::uuid,$10,$11,$12) RETURNING `+sourceTaskColumns, id, WorkspaceID(ctx), ActorID(ctx), conn.ID, conn.Name, kind, requestID, hash, parentID, schedule, MemberEpoch(ctx), conn.UpdatedAt))
	return out, e
}

func (r *AutomationRepository) EnqueueSourceTask(ctx context.Context, conn *Connection, kind, requestID, parent string) (*SourceTask, error) {
	if kind != "check" && kind != "collect" {
		return nil, ErrAutomationInput
	}
	if conn.UserID != ActorID(ctx) {
		return nil, ErrNotFound
	}
	if kind == "collect" {
		role, _ := actorRole(ctx)
		if role != "admin" && role != "developer" {
			return nil, ErrAutomationDenied
		}
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, e = tx.Exec(ctx, sourceQueueLock); e != nil {
		return nil, e
	}
	if parent != "" {
		var status string
		if e = tx.QueryRow(ctx, `SELECT status FROM sys_source_tasks WHERE id=$1::uuid AND actor_id=$2`, parent, ActorID(ctx)).Scan(&status); errors.Is(e, pgx.ErrNoRows) {
			return nil, ErrNotFound
		} else if e != nil {
			return nil, e
		}
		if status == "queued" || status == "running" || status == "cancel_requested" || status == "succeeded" {
			return nil, ErrAutomationConflict
		}
	}
	out, e := enqueueSourceTask(ctx, tx, conn, kind, requestID, parent, nil)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return out, nil
}

func (r *AutomationRepository) CancelSourceTask(ctx context.Context, id string) (*SourceTask, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	t, e := scanSourceTask(tx.QueryRow(ctx, `SELECT `+sourceTaskColumns+` FROM sys_source_tasks t WHERE t.id=$1::uuid AND t.actor_id=$2 FOR UPDATE`, id, ActorID(ctx)))
	if e != nil {
		return nil, e
	}
	status, phase := t.Status, t.Phase
	if status == "queued" {
		status, phase = "cancelled", "已取消，未启动目标操作"
	} else if status == "running" {
		status, phase = "cancel_requested", "正在取消，请等待任务确认"
	} else if status != "cancel_requested" && status != "cancelled" {
		return nil, ErrAutomationConflict
	}
	out, e := scanSourceTask(tx.QueryRow(ctx, `UPDATE sys_source_tasks t SET status=$2,phase=$3,finished_at=CASE WHEN $2='cancelled' THEN now() ELSE finished_at END WHERE id=$1::uuid RETURNING `+sourceTaskColumns, id, status, phase))
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return out, nil
}

// ClaimSourceTask is a scheduler-only maintenance operation. All target access
// subsequently rebinds this actor to a current workspace membership.
func (r *AutomationRepository) ClaimSourceTask(ctx context.Context) (*SourceTask, error) {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, sourceQueueLock); e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `UPDATE sys_source_tasks SET status=CASE WHEN status='cancel_requested' THEN 'cancelled' ELSE 'interrupted' END,phase=CASE WHEN status='cancel_requested' THEN '取消已确认' ELSE '运行租约已过期，请核实后手动重试' END,finished_at=now() WHERE status IN ('running','cancel_requested') AND lease_until<now()`)
	if e != nil {
		return nil, e
	}
	var running int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_source_tasks WHERE status IN ('running','cancel_requested')`).Scan(&running); e != nil {
		return nil, e
	}
	if running >= 2 {
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return nil, ErrNotFound
	}
	out, e := scanSourceTask(tx.QueryRow(ctx, `UPDATE sys_source_tasks t SET status='running',phase='验证身份与配置',progress=5,started_at=now(),worker_token=$1::uuid,lease_until=now()+interval '5 minutes' WHERE id=(SELECT id FROM sys_source_tasks WHERE status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+sourceTaskColumns, dashboardUUID()))
	if e != nil {
		if errors.Is(e, ErrNotFound) {
			if e = tx.Commit(ctx); e != nil {
				return nil, e
			}
			return nil, ErrNotFound
		}
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return out, nil
}

func (r *AutomationRepository) SourceTaskRunning(ctx context.Context, t *SourceTask) (bool, error) {
	var active bool
	e := r.pool.maintenance.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_source_tasks WHERE id=$1::uuid AND worker_token=$2::uuid AND status='running' AND lease_until>now())`, t.ID, t.WorkerToken).Scan(&active)
	return active, e
}
func (r *AutomationRepository) SourceTaskProgress(ctx context.Context, t *SourceTask, progress int, phase string) error {
	if progress < 5 || progress > 95 {
		return ErrAutomationInput
	}
	_, e := r.pool.maintenance.Exec(ctx, `UPDATE sys_source_tasks SET progress=GREATEST(progress,$3),phase=$4 WHERE id=$1::uuid AND worker_token=$2::uuid AND status='running' AND lease_until>now()`, t.ID, t.WorkerToken, progress, phase)
	return e
}

func (r *AutomationRepository) FinishSourceTask(ctx context.Context, t *SourceTask, status, phase string, result any, check *ConnectionCheck) (*SourceTask, error) {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	current, e := scanSourceTask(tx.QueryRow(ctx, `SELECT `+sourceTaskColumns+` FROM sys_source_tasks t WHERE id=$1::uuid FOR UPDATE`, t.ID))
	if e != nil {
		return nil, e
	}
	if current.WorkerToken != t.WorkerToken {
		return nil, ErrAutomationConflict
	}
	if current.Status != "running" && current.Status != "cancel_requested" {
		return current, nil
	}
	if current.LeaseUntil == nil || !current.LeaseUntil.After(time.Now()) {
		return nil, ErrAutomationConflict
	}
	if current.Status == "cancel_requested" {
		status, phase = "cancelled", "取消已确认"
		check = nil
		result = map[string]any{}
	}
	if check != nil {
		var valid bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_connections c JOIN sys_workspace_members m ON m.workspace_id=c.workspace_id AND m.user_id=c.user_id JOIN sys_users u ON u.id=m.user_id JOIN sys_workspaces w ON w.id=m.workspace_id WHERE c.workspace_id=$1 AND c.id=$2 AND c.user_id=$3 AND c.updated_at=$4 AND m.version=$5 AND m.active AND u.is_active AND w.enabled)`, t.WorkspaceID, t.ConnectionID, t.ActorID, t.ConfigUpdatedAt, t.MemberVersion).Scan(&valid); e != nil {
			return nil, e
		}
		if !valid {
			status, phase = "stale", "身份或配置已变化，本次检查不更新当前状态"
		} else {
			raw, _ := json.Marshal(check)
			_, e = tx.Exec(ctx, `INSERT INTO sys_connection_checks(connection_id,config_updated_at,started_at,result,workspace_id) VALUES($1,$2,$3,$4::jsonb,$5) ON CONFLICT(connection_id) DO UPDATE SET config_updated_at=EXCLUDED.config_updated_at,started_at=EXCLUDED.started_at,result=EXCLUDED.result WHERE sys_connection_checks.started_at<=EXCLUDED.started_at`, t.ConnectionID, t.ConfigUpdatedAt, check.StartedAt, string(raw), t.WorkspaceID)
			if e != nil {
				return nil, e
			}
		}
		if t.ConnectionID > 0 {
			raw, _ := json.Marshal(check)
			_, e = tx.Exec(ctx, `INSERT INTO sys_source_check_history(workspace_id,actor_id,connection_id,task_id,config_updated_at,result) SELECT $1,$2,c.id,$3::uuid,$4,$5::jsonb FROM sys_connections c WHERE c.id=$6 AND c.workspace_id=$1 ON CONFLICT(task_id) DO NOTHING`, t.WorkspaceID, t.ActorID, t.ID, t.ConfigUpdatedAt, string(raw), t.ConnectionID)
			if e != nil {
				return nil, e
			}
		}
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return nil, e
	}
	out, e := scanSourceTask(tx.QueryRow(ctx, `UPDATE sys_source_tasks t SET status=$3,phase=$4,progress=100,result=$5::jsonb,finished_at=now() WHERE id=$1::uuid AND worker_token=$2::uuid RETURNING `+sourceTaskColumns, t.ID, t.WorkerToken, status, phase, string(raw)))
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return out, nil
}

func (r *AutomationRepository) SaveSourceSchedule(ctx context.Context, conn *Connection, kind string, interval int, enabled bool, version int64) (*SourceSchedule, error) {
	if (kind != "check" && kind != "collect") || interval < 5 || interval > 10080 || kind == "collect" && interval < 60 {
		return nil, ErrAutomationInput
	}
	if conn.UserID != ActorID(ctx) {
		return nil, ErrNotFound
	}
	role, _ := actorRole(ctx)
	if kind == "collect" && role != "admin" && role != "developer" {
		return nil, ErrAutomationDenied
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, e = tx.Exec(ctx, sourceQueueLock); e != nil {
		return nil, e
	}
	if version == 0 {
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_source_schedules WHERE actor_id=$1`, ActorID(ctx)).Scan(&count); e != nil {
			return nil, e
		}
		if count >= 200 {
			return nil, ErrAutomationLimit
		}
	}
	var raw []byte
	e = tx.QueryRow(ctx, `INSERT INTO sys_source_schedules AS s(workspace_id,actor_id,connection_id,kind,interval_minutes,enabled,member_version,config_updated_at,next_run_at) SELECT $1,$2,$3,$4,$5,$6,$7,$8,now()+make_interval(mins=>$5) WHERE $9=0 ON CONFLICT(workspace_id,actor_id,connection_id,kind) DO UPDATE SET interval_minutes=EXCLUDED.interval_minutes,enabled=EXCLUDED.enabled,member_version=EXCLUDED.member_version,config_updated_at=EXCLUDED.config_updated_at,next_run_at=EXCLUDED.next_run_at,last_status='',version=s.version+1,updated_at=now() WHERE s.version=$9 RETURNING to_jsonb(s)`, WorkspaceID(ctx), ActorID(ctx), conn.ID, kind, interval, enabled, MemberEpoch(ctx), conn.UpdatedAt, version).Scan(&raw)
	// An existing schedule is updated separately so INSERT's version=0 guard
	// cannot accidentally suppress its ON CONFLICT branch.
	if errors.Is(e, pgx.ErrNoRows) && version > 0 {
		e = tx.QueryRow(ctx, `UPDATE sys_source_schedules s SET interval_minutes=$4,enabled=$5,member_version=$6,config_updated_at=$7,next_run_at=now()+make_interval(mins=>$4),last_status='',version=version+1,updated_at=now() WHERE actor_id=$1 AND connection_id=$2 AND kind=$3 AND version=$8 RETURNING to_jsonb(s)`, ActorID(ctx), conn.ID, kind, interval, enabled, MemberEpoch(ctx), conn.UpdatedAt, version).Scan(&raw)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrAutomationConflict
	}
	if e != nil {
		return nil, e
	}
	var out SourceSchedule
	e = json.Unmarshal(raw, &out)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &out, nil
}

func (r *AutomationRepository) SourceSchedules(ctx context.Context) ([]SourceSchedule, error) {
	rows, e := r.pool.Query(ctx, `SELECT to_jsonb(s) FROM sys_source_schedules s WHERE actor_id=$1 ORDER BY id DESC LIMIT 200`, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []SourceSchedule{}
	for rows.Next() {
		var raw []byte
		var s SourceSchedule
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &s); e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *AutomationRepository) DueSourceSchedules(ctx context.Context) ([]SourceSchedule, error) {
	rows, e := r.pool.maintenance.Query(ctx, `SELECT to_jsonb(s) FROM sys_source_schedules s WHERE enabled AND next_run_at<=now() ORDER BY next_run_at LIMIT 20`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []SourceSchedule{}
	for rows.Next() {
		var raw []byte
		var s SourceSchedule
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &s); e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *AutomationRepository) PauseSourceSchedule(ctx context.Context, s SourceSchedule) error {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE sys_source_schedules SET enabled=false,last_status='身份、授权或配置已变化，请重新保存计划',version=version+1,updated_at=now() WHERE workspace_id=$1 AND id=$2 AND version=$3`, s.WorkspaceID, s.ID, s.Version)
	if e != nil {
		return e
	}
	if tag.RowsAffected() > 0 {
		_, e = tx.Exec(ctx, `SELECT dbhub_notify($1,$2,$3,'task','数据源定时计划已暂停','身份、授权或配置已变化，请检查后重新保存计划。','source_tasks',0)`, s.WorkspaceID, s.ActorID, fmt.Sprintf("source-schedule:%d:%d:paused", s.ID, s.Version+1))
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (r *AutomationRepository) TickSourceSchedule(ctx context.Context, s SourceSchedule, conn *Connection) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, e = tx.Exec(ctx, sourceQueueLock); e != nil {
		return e
	}
	var id int64
	e = tx.QueryRow(ctx, `SELECT id FROM sys_source_schedules WHERE id=$1 AND actor_id=$2 AND enabled AND version=$3 AND next_run_at<=now() FOR UPDATE`, s.ID, ActorID(ctx), s.Version).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	_, enqueueErr := enqueueSourceTask(ctx, tx, conn, s.Kind, dashboardUUID(), "", &s.ID)
	lastStatus := "已加入队列"
	if errors.Is(enqueueErr, ErrAutomationConflict) {
		lastStatus = "已有同类任务，已跳过本次"
	} else if errors.Is(enqueueErr, ErrAutomationLimit) {
		lastStatus = "队列已满，已跳过本次"
	} else if enqueueErr != nil {
		return enqueueErr
	}
	_, e = tx.Exec(ctx, `UPDATE sys_source_schedules SET next_run_at=now()+make_interval(mins=>interval_minutes),last_run_at=now(),last_status=$2,updated_at=now() WHERE id=$1`, s.ID, lastStatus)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (r *AutomationRepository) SourceCheckHistory(ctx context.Context, cid int64, limit, offset int) ([]json.RawMessage, int64, error) {
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_source_check_history WHERE actor_id=$1 AND ($2=0 OR connection_id=$2)`, ActorID(ctx), cid).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT to_jsonb(h)||jsonb_build_object('current_config',COALESCE(c.updated_at=h.config_updated_at,false),'connection_name',COALESCE(c.name,'')) FROM sys_source_check_history h LEFT JOIN sys_connections c ON c.id=h.connection_id AND c.workspace_id=h.workspace_id WHERE h.actor_id=$1 AND ($2=0 OR h.connection_id=$2) ORDER BY h.id DESC LIMIT $3 OFFSET $4`, ActorID(ctx), cid, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if e = rows.Scan(&raw); e != nil {
			return nil, 0, e
		}
		out = append(out, raw)
	}
	return out, total, rows.Err()
}

type sourceTaskScope struct{ ID, Token string }
type sourceTaskContextKey struct{}

func WithSourceTask(ctx context.Context, t *SourceTask) context.Context {
	return context.WithValue(ctx, sourceTaskContextKey{}, sourceTaskScope{t.ID, t.WorkerToken})
}

// Called within the metadata publication transaction. Serializes with cancel
// and lease recovery so a late worker cannot publish after losing ownership.
func guardSourceTaskPublish(ctx context.Context, tx pgx.Tx) (*sourceTaskScope, error) {
	scope, ok := ctx.Value(sourceTaskContextKey{}).(sourceTaskScope)
	if !ok {
		return nil, nil
	}
	var active bool
	e := tx.QueryRow(ctx, `SELECT status='running' AND worker_token=$2::uuid AND lease_until>now() FROM sys_source_tasks WHERE id=$1::uuid AND actor_id=$3 FOR UPDATE`, scope.ID, scope.Token, ActorID(ctx)).Scan(&active)
	if e != nil || !active {
		return nil, ErrAutomationConflict
	}
	return &scope, nil
}
