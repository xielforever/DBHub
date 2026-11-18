package db

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

type WebhookDelivery struct {
	ID            string    `json:"id"`
	EventType     string    `json:"event_type"`
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	HTTPStatus    *int      `json:"http_status,omitempty"`
	WorkspaceID   int64     `json:"-"`
	UserID        int64     `json:"-"`
	MemberVersion int64     `json:"-"`
	WorkerToken   string    `json:"-"`
	ReportID      int64     `json:"-"`
	PublicationNo int64     `json:"-"`
	RunID         string    `json:"-"`
	ResourceID    string    `json:"-"`
	CreatedAt     time.Time `json:"-"`
}

func (r *ReportSubscriptionRepository) ClaimWebhook(ctx context.Context) (*WebhookDelivery, error) {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = tx.Exec(ctx, subscriptionQueueLock); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_webhook_deliveries SET status=CASE WHEN attempts<3 THEN 'pending' ELSE 'failed' END,next_attempt_at=now()+interval '30 seconds' WHERE permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND status='sending' AND lease_until<now()`); err != nil {
		return nil, err
	}
	var sending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sys_webhook_deliveries WHERE permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND status='sending'`).Scan(&sending); err != nil {
		return nil, err
	}
	if sending >= 2 {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}
	out := &WebhookDelivery{}
	err = tx.QueryRow(ctx, `WITH claimed AS(UPDATE sys_webhook_deliveries SET status='sending',attempts=attempts+1,worker_token=$1::uuid,lease_until=now()+interval '30 seconds' WHERE id=(SELECT id FROM sys_webhook_deliveries WHERE permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND status='pending' AND next_attempt_at<=now() AND attempts<3 ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *) SELECT w.id::text,w.event_type,w.status,w.attempts,w.last_status,w.workspace_id,w.user_id,r.member_version,w.worker_token::text,r.report_id,r.publication_no,w.run_id::text,w.resource_id,w.created_at FROM claimed w JOIN sys_report_subscription_runs r ON r.id=w.run_id AND r.workspace_id=w.workspace_id`, dashboardUUID()).Scan(&out.ID, &out.EventType, &out.Status, &out.Attempts, &out.HTTPStatus, &out.WorkspaceID, &out.UserID, &out.MemberVersion, &out.WorkerToken, &out.ReportID, &out.PublicationNo, &out.RunID, &out.ResourceID, &out.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, commitErr
			}
			return nil, ErrNotFound
		}
		return nil, managedError(err)
	}
	return out, tx.Commit(ctx)
}
func (r *ReportSubscriptionRepository) FinishWebhook(ctx context.Context, item *WebhookDelivery, success, permanent bool, httpStatus int) error {
	status := "pending"
	if success {
		status = "sent"
	} else if permanent || item.Attempts >= 3 {
		status = "failed"
	}
	tag, err := r.pool.maintenance.Exec(ctx, `UPDATE sys_webhook_deliveries SET status=$3,last_status=NULLIF($4,0),sent_at=CASE WHEN $3='sent' THEN now() ELSE sent_at END,next_attempt_at=now()+make_interval(secs=>30*attempts) WHERE id=$1::uuid AND worker_token=$2::uuid AND status='sending'`, item.ID, item.WorkerToken, status, httpStatus)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAutomationConflict
	}
	return nil
}

func (r *ReportSubscriptionRepository) CheckWebhook(ctx context.Context, item *WebhookDelivery) error {
	var valid bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_webhook_deliveries w JOIN sys_report_subscription_runs r ON r.id=w.run_id AND r.workspace_id=w.workspace_id AND r.user_id=w.user_id JOIN sys_report_subscriptions s ON s.id=r.subscription_id AND s.workspace_id=r.workspace_id AND s.user_id=r.user_id WHERE w.id=$1::uuid AND w.user_id=$2 AND w.worker_token=$3::uuid AND w.permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND w.status='sending' AND w.lease_until>now() AND r.permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND r.status='succeeded' AND r.expires_at>now() AND s.enabled AND NOT s.archived AND s.version=r.subscription_version AND s.member_version=dbhub_member_epoch())`, item.ID, ActorID(ctx), item.WorkerToken).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrAutomationConflict
	}
	return nil
}
func (r *ReportSubscriptionRepository) WebhookStatuses(ctx context.Context, deliveryID int64) ([]WebhookDelivery, error) {
	rows, err := r.pool.Query(ctx, `SELECT w.id::text,w.event_type,w.status,w.attempts,w.last_status FROM sys_webhook_deliveries w JOIN sys_report_subscription_runs r ON r.id=w.run_id AND r.workspace_id=w.workspace_id WHERE r.delivery_id=$1 AND r.user_id=$2 ORDER BY w.created_at,w.id`, deliveryID, ActorID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WebhookDelivery{}
	for rows.Next() {
		var item WebhookDelivery
		if err = rows.Scan(&item.ID, &item.EventType, &item.Status, &item.Attempts, &item.HTTPStatus); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (w WebhookDelivery) ResourcePath() string {
	if _, err := strconv.ParseInt(w.ResourceID, 10, 64); err != nil {
		return ""
	}
	if w.EventType == "report_ready" {
		return "/reports?view=subscriptions&delivery=" + w.ResourceID
	}
	if w.EventType == "metric_opened" || w.EventType == "metric_recovered" {
		return "/reports?view=subscriptions&incident=" + w.ResourceID
	}
	return ""
}
