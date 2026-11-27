package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	crypto "github.com/user/dbhub/pkg/crypto"
)

type ReportSubscriptionRepository struct {
	pool    *ScopedPool
	cipher  *crypto.Cipher
	reports *ManagedReportRepository
}

func (r *ConnectionRepository) ReportSubscriptions() *ReportSubscriptionRepository {
	return &ReportSubscriptionRepository{r.pool, r.cipher, r.ManagedReports()}
}
func (r *ReportSubscriptionRepository) decode(encrypted string, value any) error {
	plain, err := r.cipher.Decrypt(encrypted)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewBufferString(plain))
	decoder.UseNumber()
	return decoder.Decode(value)
}

const subscriptionColumns = `s.id,s.version,s.archived,s.encrypted_definition,s.next_run_at,s.last_run_at,s.last_status,
	s.enabled AND COALESCE(` + subscriptionReviewReady + `,false),s.review_stamp<>'',s.review_until`

func (r *ReportSubscriptionRepository) scan(row interface{ Scan(...any) error }) (*ReportSubscription, error) {
	out := &ReportSubscription{}
	var encrypted string
	var enabled bool
	if err := row.Scan(&out.ID, &out.Version, &out.Archived, &encrypted, &out.NextRunAt, &out.LastRunAt, &out.LastStatus, &enabled, &out.ReviewRequired, &out.ReviewUntil); err != nil {
		return nil, managedError(err)
	}
	if err := r.decode(encrypted, &out.Definition); err != nil {
		return nil, err
	}
	out.Definition.Enabled = enabled
	return out, nil
}
func (r *ReportSubscriptionRepository) List(ctx context.Context, archived bool) ([]ReportSubscription, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+subscriptionColumns+` FROM sys_report_subscriptions s WHERE s.user_id=$1 AND ($2 OR NOT archived) ORDER BY s.id DESC LIMIT 100`, ActorID(ctx), archived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportSubscription{}
	for rows.Next() {
		item, e := r.scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}
func (r *ReportSubscriptionRepository) Get(ctx context.Context, id int64) (*ReportSubscription, error) {
	return r.scan(r.pool.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM sys_report_subscriptions s WHERE s.id=$1 AND s.user_id=$2`, id, ActorID(ctx)))
}
func (r *ReportSubscriptionRepository) Save(ctx context.Context, key string, id, version int64, d ReportSubscriptionDefinition, webhookConfigured bool) (ReportCommandResult, error) {
	if err := validateSubscriptionDefinition(d); err != nil {
		return ReportCommandResult{}, err
	}
	if d.Webhook && !webhookConfigured {
		return ReportCommandResult{}, fmt.Errorf("%w: 环境尚未配置外部投递接口", ErrAutomationInput)
	}
	x, err := r.reports.ResolvePublished(ctx, d.ReportID, d.PublicationNo)
	if err != nil {
		return ReportCommandResult{}, err
	}
	if err = r.reports.PrepareBusinessReport(ctx, x); err != nil {
		return ReportCommandResult{}, err
	}
	params, err := ResolveReportParameters(x.Definition.Parameters, d.Params, time.Now())
	if err != nil {
		return ReportCommandResult{}, fmt.Errorf("%w: %s", ErrAutomationInput, err)
	}
	if x.BusinessPlan != nil {
		if _, err = x.BusinessPlan.BindReportParameters(params); err != nil {
			return ReportCommandResult{}, fmt.Errorf("%w: %s", ErrAutomationInput, err)
		}
	}
	if err = ValidateMetricAlertRule(d.Alert, x.Definition.DataContract); err != nil {
		return ReportCommandResult{}, err
	}
	return r.reports.command(ctx, key, "subscription-save", []any{id, version, d}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("subscription-capacity:%d", WorkspaceID(ctx))); e != nil {
			return out, e
		}
		enrolled, err := r.subscriptionReviewEnrolled(ctx, tx, x)
		if err != nil {
			return out, err
		}
		if id > 0 {
			var previousReport int64
			var governed bool
			err = tx.QueryRow(ctx, `SELECT report_id,review_stamp<>'' FROM sys_report_subscriptions
				WHERE id=$1 AND user_id=dbhub_actor() AND version=$2 AND NOT archived FOR UPDATE`, id, version).
				Scan(&previousReport, &governed)
			if errors.Is(err, pgx.ErrNoRows) {
				return out, ErrAutomationConflict
			}
			if err != nil {
				return out, err
			}
			if governed && !enrolled {
				return out, ErrManagedConflict
			}
			enrolled = enrolled && (governed || previousReport != d.ReportID)
		}
		stamp, status := "", ""
		if enrolled {
			d.Params, d.Enabled = params, false
			stamp, status = subscriptionConfirmationStamp(x, params), "review_required"
		}
		encrypted, err := r.cipher.Encrypt(string(canonicalReportJSON(d)))
		if err != nil {
			return out, err
		}
		if id == 0 {
			var count int
			if e := tx.QueryRow(ctx, `SELECT count(*) FROM sys_report_subscriptions WHERE user_id=$1 AND NOT archived`, ActorID(ctx)).Scan(&count); e != nil {
				return out, e
			}
			if count >= 20 {
				return out, ErrAutomationLimit
			}
			err = tx.QueryRow(ctx, `INSERT INTO sys_report_subscriptions(user_id,report_id,publication_no,name,encrypted_definition,interval_minutes,enabled,review_stamp,last_status)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id,version`, ActorID(ctx), d.ReportID, d.PublicationNo, d.Name, encrypted, d.IntervalMinutes, d.Enabled, stamp, status).Scan(&out.ID, &out.Version)
		} else {
			err = tx.QueryRow(ctx, `UPDATE sys_report_subscriptions SET report_id=$4,publication_no=$5,name=$6,encrypted_definition=$7,
				interval_minutes=$8,enabled=$9,version=version+1,member_version=dbhub_member_epoch(),next_run_at=now(),updated_at=now(),
				review_stamp=$10,review_until=NULL,last_status=$11 WHERE id=$1 AND user_id=$2 AND version=$3 AND NOT archived
				RETURNING id,version`, id, ActorID(ctx), version, d.ReportID, d.PublicationNo, d.Name, encrypted, d.IntervalMinutes, d.Enabled, stamp, status).Scan(&out.ID, &out.Version)
			if errors.Is(err, pgx.ErrNoRows) {
				return out, ErrAutomationConflict
			}
			if err == nil {
				err = r.supersedeIncidents(ctx, tx, id)
			}
		}
		return out, err
	})
}
func (r *ReportSubscriptionRepository) supersedeIncidents(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx, `WITH changed AS(UPDATE sys_metric_incidents SET status='superseded',version=version+1,closed_at=now() WHERE subscription_id=$1 AND status IN('open','acknowledged') RETURNING id) INSERT INTO sys_metric_incident_events(incident_id,event_type,comment) SELECT id,'configuration_changed','订阅配置已变化，本次跟踪已结束；不代表业务指标恢复。' FROM changed`, id)
	return err
}
func (r *ReportSubscriptionRepository) Archive(ctx context.Context, key string, id, version int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "subscription-archive", []int64{id, version}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		err := tx.QueryRow(ctx, `UPDATE sys_report_subscriptions SET archived=true,enabled=false,version=version+1,updated_at=now() WHERE id=$1 AND user_id=$2 AND version=$3 AND NOT archived RETURNING version`, id, ActorID(ctx), version).Scan(&out.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrAutomationConflict
		}
		if err == nil {
			err = r.supersedeIncidents(ctx, tx, id)
		}
		return out, err
	})
}

const subscriptionRunColumns = `r.delivery_id,r.id::text,r.subscription_id,r.subscription_version,r.workspace_id,r.user_id,r.member_version,COALESCE(r.worker_token::text,''),r.request_id::text,r.report_id,r.publication_no,r.name,r.encrypted_definition,r.status,r.message,r.row_count,r.snapshot_hash,r.created_at,r.finished_at,r.expires_at,r.permission_bridge`

func (r *ReportSubscriptionRepository) scanRun(row interface{ Scan(...any) error }) (*ReportSubscriptionRun, error) {
	out := &ReportSubscriptionRun{}
	var encrypted string
	err := row.Scan(&out.DeliveryID, &out.ID, &out.SubscriptionID, &out.SubscriptionVersion, &out.WorkspaceID, &out.UserID, &out.MemberVersion, &out.WorkerToken, &out.RequestID, &out.ReportID, &out.PublicationNo, &out.Name, &encrypted, &out.Status, &out.Message, &out.Rows, &out.SHA256, &out.CreatedAt, &out.FinishedAt, &out.ExpiresAt, &out.PermissionBridge)
	if err != nil {
		return nil, managedError(err)
	}
	out.PermissionSupported = supportedPermissionBridge(out.PermissionBridge)
	if !out.PermissionSupported {
		out.Message = unsupportedPermissionMessage
		return out, nil
	}
	if err = r.decode(encrypted, &out.Definition); err != nil {
		return nil, err
	}
	return out, nil
}
func (r *ReportSubscriptionRepository) Runs(ctx context.Context, limit, offset int) ([]ReportSubscriptionRun, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_report_subscription_runs WHERE user_id=$1`, ActorID(ctx)).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+subscriptionRunColumns+` FROM sys_report_subscription_runs r WHERE r.user_id=$1 ORDER BY r.created_at DESC,r.id DESC LIMIT $2 OFFSET $3`, ActorID(ctx), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []ReportSubscriptionRun{}
	for rows.Next() {
		item, e := r.scanRun(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, *item)
	}
	return out, total, rows.Err()
}
func (r *ReportSubscriptionRepository) Snapshot(ctx context.Context, id string) (*ReportSubscriptionSnapshot, error) {
	var encrypted, hash string
	var report, pub int64
	err := r.pool.QueryRow(ctx, `SELECT encrypted_snapshot,snapshot_hash,report_id,publication_no FROM sys_report_subscription_runs WHERE id=$1::uuid AND user_id=$2 AND permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND status='succeeded' AND expires_at>now() AND encrypted_snapshot<>''`, id, ActorID(ctx)).Scan(&encrypted, &hash, &report, &pub)
	if err != nil {
		return nil, managedError(err)
	}
	x, err := r.reports.ResolvePublished(ctx, report, pub)
	if err != nil {
		return nil, err
	}
	var out ReportSubscriptionSnapshot
	if err = r.decode(encrypted, &out); err != nil {
		return nil, err
	}
	if reportDigest(out) != hash || out.WorkspaceID != WorkspaceID(ctx) || out.RequesterID != ActorID(ctx) || out.RunID != id || out.ReportID != report || out.PublicationNo != pub {
		return nil, fmt.Errorf("订阅快照完整性校验失败")
	}
	if err = r.reports.checkBusinessReportSnapshot(ctx, x, out.BusinessPolicyStamp); err != nil {
		return nil, err
	}
	out.BusinessPolicyStamp = ""
	return &out, nil
}
func (r *ReportSubscriptionRepository) Delivery(ctx context.Context, id int64) (*ReportSubscriptionSnapshot, error) {
	var runID string
	err := r.pool.QueryRow(ctx, `SELECT id::text FROM sys_report_subscription_runs WHERE delivery_id=$1 AND user_id=$2`, id, ActorID(ctx)).Scan(&runID)
	if err != nil {
		return nil, managedError(err)
	}
	return r.Snapshot(ctx, runID)
}
