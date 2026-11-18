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
	ID            int64      `json:"id"`
	Kind          string     `json:"kind"`
	Title         string     `json:"title"`
	Message       string     `json:"message"`
	ResourceType  string     `json:"resource_type"`
	ResourceID    int64      `json:"resource_id"`
	CreatedAt     time.Time  `json:"created_at"`
	ReadAt        *time.Time `json:"read_at"`
	LinkExpiresAt *time.Time `json:"link_expires_at,omitempty"`
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
	rows, e := tx.Query(ctx, `SELECT id,kind,title,message,resource_type,
		CASE WHEN link_expires_at<=statement_timestamp() THEN 0 ELSE resource_id END,created_at,read_at,link_expires_at
		FROM sys_notifications WHERE recipient_id=$1 AND (NOT $2 OR read_at IS NULL) ORDER BY id DESC LIMIT $3 OFFSET $4`, ActorID(ctx), unread, limit, offset)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var item Notification
		if e = rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Message, &item.ResourceType, &item.ResourceID, &item.CreatedAt, &item.ReadAt, &item.LinkExpiresAt); e != nil {
			rows.Close()
			return nil, e
		}
		out.Items = append(out.Items, item)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM dbhub_inbox_todos($1,$2)`, ActorID(ctx), ActorReportReviewer(ctx)).Scan(&out.TodoCount); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return out, nil
}

type NotificationDelivery struct {
	ResourceType string  `json:"resource_type"`
	ResourceID   int64   `json:"resource_id"`
	DeliveryID   *string `json:"delivery_id,omitempty"`
}

func (r *AutomationRepository) NotificationDelivery(ctx context.Context, id int64) (NotificationDelivery, error) {
	var out NotificationDelivery
	err := r.pool.QueryRow(ctx, `SELECT n.resource_type,n.resource_id,d.id::text FROM sys_notifications n
		LEFT JOIN sys_report_team_deliveries d ON n.resource_type='team_report_delivery' AND d.workspace_id=n.workspace_id
		AND d.period_id=n.resource_id AND d.user_id=n.recipient_id AND d.status='succeeded' AND d.expires_at>statement_timestamp()
		WHERE n.id=$1 AND n.recipient_id=dbhub_actor() AND n.workspace_id=dbhub_workspace()
		AND n.link_expires_at>statement_timestamp() AND (
		(n.resource_type='team_report_delivery' AND d.id IS NOT NULL) OR
		(n.resource_type='dashboard_delivery' AND EXISTS(SELECT 1 FROM sys_dashboard_deliveries b
			JOIN sys_dashboard_delivery_plans p ON p.workspace_id=b.workspace_id AND p.id=b.plan_id
			WHERE b.workspace_id=n.workspace_id AND b.id=n.resource_id AND b.user_id=n.recipient_id
			AND b.status='succeeded' AND b.expires_at>statement_timestamp() AND b.payload<>''
			AND b.member_version=dbhub_member_epoch() AND dbhub_dashboard_delivery_open(p.id,b.user_id,'recipient')
			AND (p.kind<>'team' OR EXISTS(SELECT 1 FROM sys_dashboard_recipient_access a
				WHERE a.workspace_id=b.workspace_id AND a.plan_id=b.plan_id AND a.user_id=b.user_id
				AND a.enabled AND a.version=b.grant_version AND a.member_version=b.member_version)))) OR
		(n.resource_type='formal_export_delivery' AND EXISTS(SELECT 1 FROM sys_report_export_files f
			WHERE f.workspace_id=n.workspace_id AND f.export_id=n.resource_id AND f.expires_at>statement_timestamp())) OR
		(n.resource_type='report_delivery' AND EXISTS(SELECT 1 FROM sys_report_subscription_runs s
			WHERE s.workspace_id=n.workspace_id AND s.user_id=n.recipient_id AND s.delivery_id=n.resource_id
			AND s.status='succeeded' AND s.expires_at>statement_timestamp() AND s.encrypted_snapshot<>'')))`, id).
		Scan(&out.ResourceType, &out.ResourceID, &out.DeliveryID)
	return out, managedError(err)
}

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
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM dbhub_inbox_todos($1,$2)`, ActorID(ctx), ActorReportReviewer(ctx)).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := tx.Query(ctx, `SELECT kind,id,resource_id,title,created_at,expires_at FROM dbhub_inbox_todos($1,$2) ORDER BY created_at,id LIMIT $3 OFFSET $4`, ActorID(ctx), ActorReportReviewer(ctx), limit, offset)
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
