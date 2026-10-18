package db

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrAutomationInput = errors.New("任务参数无效")
var ErrAutomationConflict = errors.New("状态已变化或已有同类任务，请刷新后核对")
var ErrAutomationLimit = errors.New("后台任务队列已满，请稍后再试")
var ErrAutomationDenied = errors.New("当前身份无权执行此任务")

// AutomationRepository exposes scoped user operations separately from the
// bounded maintenance methods used by the scheduler. No credentials are stored.
type AutomationRepository struct{ pool *ScopedPool }

func NewAutomationRepository(users *UserRepository) *AutomationRepository {
	return &AutomationRepository{pool: users.pool}
}

type Notification struct {
	ID           int64      `json:"id"`
	Kind         string     `json:"kind"`
	Title        string     `json:"title"`
	Message      string     `json:"message"`
	ResourceType string     `json:"resource_type"`
	ResourceID   int64      `json:"resource_id"`
	CreatedAt    time.Time  `json:"created_at"`
	ReadAt       *time.Time `json:"read_at"`
}

type Inbox struct {
	Items     []Notification `json:"items"`
	Total     int64          `json:"total"`
	Unread    int64          `json:"unread"`
	ThroughID int64          `json:"through_id"`
	TodoCount int64          `json:"todo_count"`
}

func (r *AutomationRepository) Inbox(ctx context.Context, unread bool, limit, offset int) (*Inbox, error) {
	out := &Inbox{Items: []Notification{}}
	tx, e := r.pool.BeginSnapshot(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	e = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE NOT $1 OR read_at IS NULL),count(*) FILTER(WHERE read_at IS NULL),COALESCE(max(id),0) FROM sys_notifications WHERE recipient_id=$2`, unread, ActorID(ctx)).Scan(&out.Total, &out.Unread, &out.ThroughID)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT id,kind,title,message,resource_type,resource_id,created_at,read_at FROM sys_notifications WHERE recipient_id=$1 AND (NOT $2 OR read_at IS NULL) ORDER BY id DESC LIMIT $3 OFFSET $4`, ActorID(ctx), unread, limit, offset)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var item Notification
		if e = rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Message, &item.ResourceType, &item.ResourceID, &item.CreatedAt, &item.ReadAt); e != nil {
			rows.Close()
			return nil, e
		}
		out.Items = append(out.Items, item)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM (`+todoSQL+`) todos`, ActorID(ctx), ActorAdmin(ctx)).Scan(&out.TodoCount); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return out, nil
}

// Pending work is derived from current domain state. Reading a notice does not
// decide a review, and stale notifications never keep completed work pending.
const todoSQL = `
 SELECT 'report_review'::text AS kind,rv.id,r.id AS resource_id,'报表待审批'::text AS title,v.created_at AS created_at,NULL::timestamptz AS expires_at
 FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id
 JOIN sys_managed_reports r ON r.id=v.report_id AND r.workspace_id=v.workspace_id
 WHERE $2 AND rv.status='pending'
 UNION ALL
 SELECT 'dashboard_review',rv.id,rv.dashboard_id,'仪表盘待审阅',v.created_at,rv.expires_at
 FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id
 JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id AND d.workspace_id=rv.workspace_id
 WHERE rv.reviewer_id=$1 AND rv.status='pending' AND rv.expires_at>now() AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch
 UNION ALL
 SELECT 'access_request',a.id,COALESCE(a.dashboard_id,a.report_id),'报表权限申请',a.created_at,a.expires_at
 FROM sys_access_requests a WHERE $2 AND (a.status='pending' OR (a.status='processing' AND a.assigned_to=$1)) AND a.expires_at>now()`

type Todo struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	ResourceID int64      `json:"resource_id"`
	Title      string     `json:"title"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

func (r *AutomationRepository) Todos(ctx context.Context, limit, offset int) ([]Todo, int64, error) {
	tx, e := r.pool.BeginSnapshot(ctx)
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var total int64
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM (`+todoSQL+`) todos`, ActorID(ctx), ActorAdmin(ctx)).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := tx.Query(ctx, `SELECT kind,id,resource_id,title,created_at,expires_at FROM (`+todoSQL+`) todos ORDER BY created_at,id LIMIT $3 OFFSET $4`, ActorID(ctx), ActorAdmin(ctx), limit, offset)
	if e != nil {
		return nil, 0, e
	}
	out := []Todo{}
	for rows.Next() {
		var item Todo
		if e = rows.Scan(&item.Kind, &item.ID, &item.ResourceID, &item.Title, &item.CreatedAt, &item.ExpiresAt); e != nil {
			rows.Close()
			return nil, 0, e
		}
		out = append(out, item)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, 0, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, 0, e
	}
	return out, total, nil
}

func (r *AutomationRepository) ReadNotifications(ctx context.Context, ids []int64, through int64) (int64, error) {
	if len(ids) > 100 || through < 0 || (len(ids) == 0 && through == 0) || (len(ids) > 0 && through > 0) {
		return 0, ErrAutomationInput
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, ErrAutomationInput
		}
	}
	tag, e := r.pool.Exec(ctx, `UPDATE sys_notifications SET read_at=now() WHERE recipient_id=$1 AND read_at IS NULL AND (($2>0 AND id<=$2) OR id=ANY($3::bigint[]))`, ActorID(ctx), through, ids)
	return tag.RowsAffected(), e
}

func automationWrap(input error, detail string) error { return fmt.Errorf("%w: %s", input, detail) }
