package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (r *ReportSubscriptionRepository) FinishRun(ctx context.Context, run *ReportSubscriptionRun, snapshot *ReportSubscriptionSnapshot) error {
	if snapshot == nil || snapshot.WorkspaceID != WorkspaceID(ctx) || snapshot.RequesterID != ActorID(ctx) || snapshot.RequesterID != run.UserID || snapshot.RunID != run.ID || snapshot.ReportID != run.ReportID || snapshot.PublicationNo != run.PublicationNo {
		return ErrAutomationInput
	}
	x, err := r.reports.ResolvePublished(ctx, run.ReportID, run.PublicationNo)
	if err != nil {
		return err
	}
	if err = r.reports.checkBusinessReportSnapshot(ctx, x, snapshot.BusinessPolicyStamp); err != nil {
		return err
	}
	active, value, err := EvaluateMetricAlert(run.Definition.Alert, &snapshot.Result)
	if err != nil {
		return err
	}
	raw := canonicalReportJSON(snapshot)
	if len(raw) > 5<<20 {
		return fmt.Errorf("订阅快照超过大小限制")
	}
	encrypted, err := r.cipher.Encrypt(string(raw))
	if err != nil {
		return err
	}
	hash := reportDigest(snapshot)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var status, storedHash string
	var valid bool
	err = tx.QueryRow(ctx, `SELECT r.status,r.snapshot_hash,s.enabled AND NOT s.archived AND COALESCE(`+subscriptionReviewReady+`,false) AND s.version=r.subscription_version AND s.member_version=dbhub_member_epoch() AND s.member_version=r.member_version AND r.worker_token=$3::uuid AND r.lease_until>now() FROM sys_report_subscription_runs r JOIN sys_report_subscriptions s ON s.id=r.subscription_id AND s.workspace_id=r.workspace_id WHERE r.id=$1::uuid AND r.user_id=$2 FOR UPDATE OF r,s`, run.ID, ActorID(ctx), run.WorkerToken).Scan(&status, &storedHash, &valid)
	if err != nil {
		return managedError(err)
	}
	if status == "succeeded" && storedHash == hash {
		return tx.Commit(ctx)
	}
	if status != "running" || !valid {
		return ErrAutomationConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_report_subscription_runs SET status='succeeded',message='受控查询快照已送达；完整导出仍须独立审批。',encrypted_snapshot=$2,snapshot_hash=$3,row_count=$4,finished_at=now() WHERE id=$1::uuid`, run.ID, encrypted, hash, len(snapshot.Result.Rows)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_report_subscriptions SET last_status='succeeded',last_run_at=now() WHERE id=$1 AND version=$2`, run.SubscriptionID, run.SubscriptionVersion); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT dbhub_notify_delivery($1,$2,$3,'订阅报表已送达','已保存受控查询快照，请登录查看当前授权范围。','report_delivery',$4)`, WorkspaceID(ctx), ActorID(ctx), "subscription-ready:"+run.ID, run.DeliveryID); err != nil {
		return err
	}
	if run.Definition.Webhook {
		if err = r.enqueueWebhook(ctx, tx, run, "report_ready", strconv.FormatInt(run.DeliveryID, 10)); err != nil {
			return err
		}
	}
	if run.Definition.Alert != nil {
		if err = r.observeMetric(ctx, tx, run, active, value, snapshot.BusinessPolicyStamp); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (r *ReportSubscriptionRepository) enqueueWebhook(ctx context.Context, tx pgx.Tx, run *ReportSubscriptionRun, kind, resourceID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO sys_webhook_deliveries(id,workspace_id,user_id,run_id,event_type,resource_id) VALUES($1::uuid,$2,$3,$4::uuid,$5,$6) ON CONFLICT(run_id,event_type) DO NOTHING`, dashboardUUID(), WorkspaceID(ctx), ActorID(ctx), run.ID, kind, resourceID)
	return err
}
func (r *ReportSubscriptionRepository) observeMetric(ctx context.Context, tx pgx.Tx, run *ReportSubscriptionRun, active bool, value, policy string) error {
	encryptedValue, err := r.cipher.Encrypt(value)
	if err != nil {
		return err
	}
	var id int64
	var previousActive bool
	err = tx.QueryRow(ctx, `SELECT id,condition_active FROM sys_metric_incidents WHERE subscription_id=$1 AND status IN('open','acknowledged') FOR UPDATE`, run.SubscriptionID).Scan(&id, &previousActive)
	opened := false
	if err == pgx.ErrNoRows {
		if !active {
			return nil
		}
		encryptedRule, e := r.cipher.Encrypt(string(canonicalReportJSON(run.Definition.Alert)))
		if e != nil {
			return e
		}
		err = tx.QueryRow(ctx, `INSERT INTO sys_metric_incidents(workspace_id,user_id,subscription_id,subscription_version,report_id,publication_no,name,encrypted_rule,encrypted_value,first_run_id,last_run_id,value_policy_stamp) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::uuid,$10::uuid,$11) RETURNING id`, WorkspaceID(ctx), ActorID(ctx), run.SubscriptionID, run.SubscriptionVersion, run.ReportID, run.PublicationNo, run.Name, encryptedRule, encryptedValue, run.ID, policy).Scan(&id)
		if err != nil {
			return err
		}
		opened = true
	} else if err != nil {
		return err
	} else {
		if _, err = tx.Exec(ctx, `UPDATE sys_metric_incidents SET condition_active=$2,encrypted_value=$3,last_run_id=$4::uuid,last_seen_at=now(),recovered_at=CASE WHEN $2 THEN NULL WHEN condition_active THEN now() ELSE recovered_at END,version=version+1,value_policy_stamp=$5 WHERE id=$1`, id, active, encryptedValue, run.ID, policy); err != nil {
			return err
		}
	}
	kind, title, body := "", "", ""
	if opened {
		kind = "metric_opened"
		title = "业务指标达到告警阈值"
		body = "请查看指标口径和快照，确认后跟踪处理。"
	} else if previousActive && !active {
		kind = "metric_recovered"
		title = "业务指标已回到正常范围"
		body = "指标已恢复，请核对后补充处理记录并关闭跟踪。"
	} else if !previousActive && active {
		kind = "metric_opened"
		title = "业务指标再次达到告警阈值"
		body = "已有跟踪尚未关闭，请继续处理。"
	}
	if kind != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO sys_metric_incident_events(incident_id,event_type,run_id,encrypted_observed_value,observed_policy_stamp)
			VALUES($1,$2,$3::uuid,$4,$5)`, id, kind, run.ID, encryptedValue, policy); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `SELECT dbhub_notify($1,$2,$3,'metric_alert',$4,$5,'metric_incident',$6)`, WorkspaceID(ctx), ActorID(ctx), kind+":"+run.ID, title, body, id); err != nil {
			return err
		}
		if run.Definition.Webhook {
			if err = r.enqueueWebhook(ctx, tx, run, kind, strconv.FormatInt(id, 10)); err != nil {
				return err
			}
		}
	}
	return nil
}

const incidentColumns = `i.id,i.subscription_id,i.report_id,i.publication_no,i.name,i.encrypted_rule,i.encrypted_value,i.status,i.condition_active,i.version,i.first_run_id::text,i.last_run_id::text,i.opened_at,i.last_seen_at,i.recovered_at,i.closed_at,i.value_policy_stamp`

func (r *ReportSubscriptionRepository) scanIncident(row interface{ Scan(...any) error }) (*MetricIncident, error) {
	out := &MetricIncident{}
	var encryptedRule, encryptedValue string
	err := row.Scan(&out.ID, &out.SubscriptionID, &out.ReportID, &out.PublicationNo, &out.Name, &encryptedRule, &encryptedValue, &out.Status, &out.Active, &out.Version, &out.FirstRunID, &out.LastRunID, &out.OpenedAt, &out.LastSeenAt, &out.RecoveredAt, &out.ClosedAt, &out.valuePolicyStamp)
	if err != nil {
		return nil, managedError(err)
	}
	if err = r.decode(encryptedRule, &out.Rule); err != nil {
		return nil, err
	}
	out.Value, err = r.cipher.Decrypt(encryptedValue)
	return out, err
}
func (r *ReportSubscriptionRepository) Incidents(ctx context.Context, limit, offset int) ([]MetricIncident, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_metric_incidents WHERE user_id=$1`, ActorID(ctx)).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT `+incidentColumns+` FROM sys_metric_incidents i WHERE i.user_id=$1 ORDER BY i.id DESC LIMIT $2 OFFSET $3`, ActorID(ctx), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items := []MetricIncident{}
	for rows.Next() {
		item, e := r.scanIncident(rows)
		if e != nil {
			rows.Close()
			return nil, 0, e
		}
		items = append(items, *item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	// Authorization is checked before any numeric value leaves this repository.
	allowed := []MetricIncident{}
	for _, item := range items {
		x, err := r.reports.ResolvePublished(ctx, item.ReportID, item.PublicationNo)
		if err == nil && r.reports.checkBusinessReportSnapshot(ctx, x, item.valuePolicyStamp) == nil {
			allowed = append(allowed, item)
		}
	}
	return allowed, total, nil
}
func (r *ReportSubscriptionRepository) Incident(ctx context.Context, id int64) (*MetricIncident, []MetricIncidentEvent, error) {
	item, err := r.scanIncident(r.pool.QueryRow(ctx, `SELECT `+incidentColumns+` FROM sys_metric_incidents i WHERE i.id=$1 AND i.user_id=$2`, id, ActorID(ctx)))
	if err != nil {
		return nil, nil, err
	}
	x, err := r.reports.ResolvePublished(ctx, item.ReportID, item.PublicationNo)
	if err != nil {
		return nil, nil, err
	}
	if err = r.reports.checkBusinessReportSnapshot(ctx, x, item.valuePolicyStamp); err != nil {
		return nil, nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id,event_type,comment,created_at,encrypted_observed_value,observed_policy_stamp FROM sys_metric_incident_events WHERE incident_id=$1 ORDER BY id DESC LIMIT 100`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	events := []MetricIncidentEvent{}
	for rows.Next() {
		var event MetricIncidentEvent
		var encrypted, policy string
		if err = rows.Scan(&event.ID, &event.Type, &event.Comment, &event.CreatedAt, &encrypted, &policy); err != nil {
			return nil, nil, err
		}
		if encrypted != "" && policy == x.BusinessPolicyStamp() {
			value, err := r.cipher.Decrypt(encrypted)
			if err != nil {
				return nil, nil, err
			}
			event.ObservedValue = &value
		}
		events = append(events, event)
	}
	return item, events, rows.Err()
}
func (r *ReportSubscriptionRepository) IncidentAction(ctx context.Context, key string, id, version int64, action, comment string) (ReportCommandResult, error) {
	if action != "acknowledge" && action != "comment" && action != "close" || strings.TrimSpace(comment) == "" || strings.ContainsRune(comment, 0) || len([]rune(comment)) > 1000 {
		return ReportCommandResult{}, ErrAutomationInput
	}
	_, _, err := r.Incident(ctx, id)
	if err != nil {
		return ReportCommandResult{}, err
	}
	return r.reports.command(ctx, key, "metric-incident-action", []any{id, version, action, comment}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: id}
		var status string
		var active bool
		var current int64
		if err := tx.QueryRow(ctx, `SELECT status,condition_active,version FROM sys_metric_incidents WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, ActorID(ctx)).Scan(&status, &active, &current); err != nil {
			return out, err
		}
		if current != version || status == "closed" || status == "superseded" || action == "close" && active {
			return out, ErrAutomationConflict
		}
		if action == "acknowledge" {
			if status != "open" {
				return out, ErrAutomationConflict
			}
			status = "acknowledged"
		}
		if action == "close" {
			status = "closed"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sys_metric_incident_events(incident_id,event_type,comment) VALUES($1,$2,$3)`, id, action, comment); err != nil {
			return out, err
		}
		err := tx.QueryRow(ctx, `UPDATE sys_metric_incidents SET status=$2,version=version+1,closed_at=CASE WHEN $2='closed' THEN now() ELSE closed_at END WHERE id=$1 RETURNING version`, id, status).Scan(&out.Version)
		return out, err
	})
}
