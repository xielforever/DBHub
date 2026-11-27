package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const subscriptionQueueLock = `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':report-subscription-queue',0))`

func (r *ReportSubscriptionRepository) GenerateDue(ctx context.Context) (int, error) {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = tx.Exec(ctx, subscriptionQueueLock); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_report_subscriptions s SET enabled=false,last_status='authorization_changed',version=version+1,updated_at=now() WHERE enabled AND NOT EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id JOIN sys_workspaces w ON w.id=m.workspace_id WHERE m.workspace_id=s.workspace_id AND m.user_id=s.user_id AND m.version=s.member_version AND m.active AND u.is_active AND w.enabled AND dbhub_iam_task_allowed(m.workspace_id,m.user_id,'report.subscribe'))`); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT s.id FROM sys_report_subscriptions s WHERE s.enabled AND NOT s.archived AND s.next_run_at<=now() AND `+subscriptionReviewReady+` ORDER BY next_run_at,id FOR UPDATE SKIP LOCKED LIMIT 20`)
	if err != nil {
		return 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	created := 0
	for _, id := range ids {
		var queued int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM sys_report_subscription_runs WHERE status IN('queued','running')`).Scan(&queued); err != nil {
			return 0, err
		}
		if queued >= 200 {
			break
		}
		tag, e := tx.Exec(ctx, `INSERT INTO sys_report_subscription_runs(id,workspace_id,user_id,subscription_id,subscription_version,member_version,report_id,publication_no,name,encrypted_definition,request_id) SELECT $2::uuid,s.workspace_id,s.user_id,s.id,s.version,s.member_version,s.report_id,s.publication_no,s.name,s.encrypted_definition,$3::uuid FROM sys_report_subscriptions s WHERE s.id=$1 AND s.enabled AND NOT s.archived AND `+subscriptionReviewReady+` AND NOT EXISTS(SELECT 1 FROM sys_report_subscription_runs r WHERE r.subscription_id=s.id AND r.status IN('queued','running')) AND (SELECT count(*) FROM sys_report_subscription_runs r WHERE r.workspace_id=s.workspace_id AND r.user_id=s.user_id AND r.status IN('queued','running'))<5`, id, dashboardUUID(), dashboardUUID())
		if e != nil {
			return 0, e
		}
		created += int(tag.RowsAffected())
		if _, err = tx.Exec(ctx, `UPDATE sys_report_subscriptions SET next_run_at=now()+make_interval(mins=>interval_minutes) WHERE id=$1`, id); err != nil {
			return 0, err
		}
	}
	// Keep receipt metadata while expiring the encrypted row payload itself.
	if _, err = tx.Exec(ctx, `UPDATE sys_report_subscription_runs SET encrypted_snapshot='' WHERE permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND expires_at<=now() AND encrypted_snapshot<>''`); err != nil {
		return 0, err
	}
	return created, tx.Commit(ctx)
}
func (r *ReportSubscriptionRepository) EnqueueNow(ctx context.Context, key string, id, version int64) (ReportCommandResult, error) {
	return r.reports.command(ctx, key, "subscription-run", []int64{id, version}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{}
		if _, err := tx.Exec(ctx, subscriptionQueueLock); err != nil {
			return out, err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sys_report_subscription_runs WHERE user_id=$1 AND status IN('queued','running')`, ActorID(ctx)).Scan(&count); err != nil {
			return out, err
		}
		if count >= 5 {
			return out, ErrAutomationLimit
		}
		err := tx.QueryRow(ctx, `INSERT INTO sys_report_subscription_runs(id,workspace_id,user_id,subscription_id,subscription_version,member_version,report_id,publication_no,name,encrypted_definition,request_id) SELECT $4::uuid,s.workspace_id,s.user_id,s.id,s.version,s.member_version,s.report_id,s.publication_no,s.name,s.encrypted_definition,$5::uuid FROM sys_report_subscriptions s WHERE s.id=$1 AND s.user_id=$2 AND s.version=$3 AND s.enabled AND NOT s.archived AND `+subscriptionReviewReady+` AND s.member_version=dbhub_member_epoch() AND NOT EXISTS(SELECT 1 FROM sys_report_subscription_runs r WHERE r.subscription_id=s.id AND r.status IN('queued','running')) RETURNING delivery_id`, id, ActorID(ctx), version, dashboardUUID(), key).Scan(&out.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrAutomationConflict
		}
		return out, err
	})
}
func (r *ReportSubscriptionRepository) Claim(ctx context.Context) (*ReportSubscriptionRun, error) {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = tx.Exec(ctx, subscriptionQueueLock); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_report_subscription_runs SET status='interrupted',message='运行租约已过期，结果未交付。',finished_at=now() WHERE permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND status='running' AND lease_until<now()`); err != nil {
		return nil, err
	}
	var running int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sys_report_subscription_runs WHERE permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND status='running'`).Scan(&running); err != nil {
		return nil, err
	}
	if running >= 2 {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}
	run, err := r.scanRun(tx.QueryRow(ctx, `UPDATE sys_report_subscription_runs r SET status='running',worker_token=$1::uuid,lease_until=now()+interval '2 minutes',started_at=now() WHERE r.id=(SELECT id FROM sys_report_subscription_runs WHERE permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+subscriptionRunColumns, dashboardUUID()))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			if err = tx.Commit(ctx); err == nil {
				return nil, ErrNotFound
			}
		}
		return nil, err
	}
	return run, tx.Commit(ctx)
}
func (r *ReportSubscriptionRepository) CheckRun(ctx context.Context, run *ReportSubscriptionRun) error {
	var valid bool
	var stamp string
	err := r.pool.QueryRow(ctx, `SELECT s.review_stamp,COALESCE((r.permission_bridge IN('legacy-frozen-v1','phase1-bridge-2')
		AND r.status='running' AND r.worker_token=$3::uuid AND r.lease_until>statement_timestamp()
		AND s.version=r.subscription_version AND s.enabled AND NOT s.archived
		AND s.member_version=dbhub_member_epoch() AND s.member_version=r.member_version AND `+subscriptionReviewReady+`),false)
		FROM sys_report_subscription_runs r JOIN sys_report_subscriptions s ON s.workspace_id=r.workspace_id AND s.id=r.subscription_id
		WHERE r.id=$1::uuid AND r.user_id=$2`, run.ID, ActorID(ctx), run.WorkerToken).Scan(&stamp, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAutomationConflict
	}
	if err != nil {
		return err
	}
	if !valid {
		return ErrAutomationConflict
	}
	if stamp != "" {
		err = r.checkSubscriptionConfirmation(ctx, run.Definition, stamp)
		if errors.Is(err, ErrNotFound) {
			return ErrManagedConflict
		}
		return err
	}
	return nil
}
func (r *ReportSubscriptionRepository) FreezeParameters(ctx context.Context, run *ReportSubscriptionRun, params map[string]any) error {
	if err := r.CheckRun(ctx, run); err != nil {
		return err
	}
	definition := run.Definition
	definition.Params = params
	encrypted, err := r.cipher.Encrypt(string(canonicalReportJSON(definition)))
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `UPDATE sys_report_subscription_runs SET encrypted_definition=$4 WHERE id=$1::uuid AND user_id=$2 AND worker_token=$3::uuid AND status='running'`, run.ID, ActorID(ctx), run.WorkerToken, encrypted)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAutomationConflict
	}
	run.Definition = definition
	return nil
}

// Failure completion never stores result rows and does not borrow a stale actor
// context after membership revocation. Notice text is fixed and credential-free.
func (r *ReportSubscriptionRepository) FailRun(ctx context.Context, run *ReportSubscriptionRun, pause bool) error {
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `UPDATE sys_report_subscription_runs SET status='failed',message='订阅未通过当前授权、版本、数据校验或运行条件；结果未交付。',finished_at=now() WHERE id=$1::uuid AND worker_token=$2::uuid AND status='running'`, run.ID, run.WorkerToken)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAutomationConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_report_subscriptions SET last_status='failed',last_run_at=now(),enabled=CASE WHEN $3 THEN false ELSE enabled END WHERE id=$1 AND version=$2`, run.SubscriptionID, run.SubscriptionVersion, pause); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT dbhub_notify($1,$2,$3,'report_subscription','订阅执行未完成','请检查订阅的当前权限、发布版本及数据规则；没有交付未确认结果。','report_subscription',$4)`, run.WorkspaceID, run.UserID, "subscription-failed:"+run.ID, run.SubscriptionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
