package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/connector"
)

type TeamDeliveryJob struct {
	ID, token           string
	WorkspaceID, UserID int64
	MemberVersion       int64
	PlanID              int64
}

type TeamDeliveryExecution struct {
	Report   *ManagedExecution
	Params   map[string]any
	audience string
	policy   string
}

var ErrTeamRecipientReview = fmt.Errorf("%w: 收件范围超过原批准边界，请重新送审", ErrManagedConflict)

func (r *ReportSubscriptionRepository) ClaimTeamDelivery(ctx context.Context) (*TeamDeliveryJob, error) {
	if r.reports.phase2ReportIsolation == "" {
		return nil, ErrNotFound
	}
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = tx.Exec(ctx, subscriptionQueueLock); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_report_team_deliveries SET status='interrupted',worker_token=NULL,lease_until=NULL
		WHERE status='running' AND lease_until<=statement_timestamp()`); err != nil {
		return nil, err
	}
	job := &TeamDeliveryJob{token: dashboardUUID()}
	err = tx.QueryRow(ctx, `UPDATE sys_report_team_deliveries SET status='running',worker_token=$1::uuid,
		lease_until=statement_timestamp()+interval '2 minutes'
		WHERE id=(SELECT id FROM sys_report_team_deliveries WHERE status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
		AND (SELECT count(*) FROM sys_report_team_deliveries WHERE status='running')<2
		RETURNING id::text,workspace_id,user_id,member_version,plan_id`, job.token).
		Scan(&job.ID, &job.WorkspaceID, &job.UserID, &job.MemberVersion, &job.PlanID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return nil, commitErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return job, nil
}

type teamDeliveryRecord struct {
	shape                     teamSubscriptionApproval
	plan, report, publication int64
	audience                  string
}

func (r *ReportSubscriptionRepository) readTeamDeliveryRecord(ctx context.Context, tx pgx.Tx, id, worker string) (teamDeliveryRecord, error) {
	var rec teamDeliveryRecord
	if err := r.setTeamSubscriptionContext(ctx, tx, 0); err != nil {
		return rec, err
	}
	var encrypted, basis string
	var valid bool
	var reviewerGrant, owner, version int64
	var recipients, direct, groups []int64
	err := tx.QueryRow(ctx, `SELECT p.id,p.report_id,p.publication_no,p.version,p.owner_id,p.recipient_ids,p.user_ids,p.group_ids,
		p.encrypted_approval,p.basis_sha256,p.review_grant_id,
		dbhub_team_delivery_live(p.id) AND dbhub_team_delivery_selected(p.id,dbhub_actor())
		AND a.enabled AND a.version=d.grant_version AND a.member_version=d.member_version
		AND d.member_version=dbhub_member_epoch()
		FROM sys_report_team_deliveries d JOIN sys_report_team_subscriptions p ON p.workspace_id=d.workspace_id AND p.id=d.plan_id
		JOIN sys_report_team_recipient_access a ON a.workspace_id=d.workspace_id AND a.plan_id=p.id AND a.user_id=d.user_id
		WHERE d.workspace_id=dbhub_workspace() AND d.id=$1::uuid AND d.user_id=dbhub_actor()
		AND (($2='' AND d.status='succeeded' AND d.expires_at>statement_timestamp())
			OR ($2<>'' AND d.status='running' AND d.worker_token=NULLIF($2,'')::uuid AND d.lease_until>statement_timestamp()))`,
		id, worker).Scan(&rec.plan, &rec.report, &rec.publication, &version, &owner, &recipients, &direct, &groups,
		&encrypted, &basis, &reviewerGrant, &valid)
	if err != nil {
		return rec, managedError(err)
	}
	if !valid {
		return rec, ErrManagedDenied
	}
	if err = r.decode(encrypted, &rec.shape); err != nil {
		return rec, err
	}
	item := &TeamSubscription{ID: rec.plan, Version: version - 1, OwnerID: owner, Recipients: recipients}
	if basis != teamSubscriptionBasisHash(item, reviewerGrant, rec.shape) {
		return rec, ErrManagedConflict
	}
	var ownerCurrent bool
	err = tx.QueryRow(ctx, `SELECT dbhub_team_delivery_owner_scope($1,$2,$3,$4)`, rec.plan,
		rec.shape.Owner.Version, rec.shape.Owner.IdentityVersion, rec.shape.Shape.DatasetVersion).Scan(&ownerCurrent)
	if err != nil {
		return rec, err
	}
	if !ownerCurrent {
		return rec, ErrManagedConflict
	}
	// Keep a generation proof for any group on which this recipient actually relies.
	if containsID(direct, ActorID(ctx)) {
		groups = nil
	}
	if !containsID(rec.shape.Shape.Users, ActorID(ctx)) {
		groups = append(groups, rec.shape.Shape.Groups...)
	}
	var membership []string
	err = tx.QueryRow(ctx, `SELECT COALESCE(array_agg(g.id::text||':'||g.version::text ORDER BY g.id),'{}'::text[])
		FROM sys_user_groups g JOIN sys_user_group_members gm ON gm.group_id=g.id
		WHERE g.id=ANY($1::bigint[]) AND gm.user_id=dbhub_actor()`, nonNilIDs(groups)).Scan(&membership)
	rec.audience = reportDigest(membership)
	return rec, err
}

func (r *ReportSubscriptionRepository) teamDeliveryExecution(ctx context.Context, tx pgx.Tx, id, worker string, durable bool) (*TeamDeliveryExecution, error) {
	rec, err := r.readTeamDeliveryRecord(ctx, tx, id, worker)
	if err != nil {
		return nil, err
	}
	x, err := r.reports.ResolvePublished(ctx, rec.report, rec.publication)
	if err != nil {
		return nil, err
	}
	if err = r.reports.useBusinessReportBinding(ctx, tx, x); err != nil {
		return nil, err
	}
	if durable {
		if err = lockBusinessReportPolicy(ctx, tx, x.businessBinding); err != nil {
			return nil, err
		}
	}
	row, output, _, err := r.reports.businessReportPolicy(ctx, tx, x)
	if err != nil {
		return nil, err
	}
	member, err := readOrganizationScope(ctx, tx, ActorID(ctx), row.DatasetKey)
	if err != nil {
		return nil, err
	}
	approved := rec.shape.Shape
	approved.Audience = rec.shape.Recipients
	index := slices.IndexFunc(approved.Audience, func(entry reportReviewAudience) bool { return entry.UserID == ActorID(ctx) })
	if index >= 0 && !subset(effectiveBusinessScopes(row.Definition, member), approved.Audience[index].Scopes) {
		return nil, ErrTeamRecipientReview
	}
	if !custodyReadCovered(approved, x, row, output, member) {
		return nil, ErrManagedConflict
	}
	if err = r.reports.prepareBusinessReport(ctx, tx, x); err != nil {
		return nil, err
	}
	params, err := ResolveReportParameters(x.Definition.Parameters, rec.shape.Params, time.Now())
	if err != nil || reportDigest(params) != reportDigest(rec.shape.Params) {
		return nil, ErrManagedConflict
	}
	if _, err = x.BusinessPlan.BindReportParameters(params); err != nil {
		return nil, ErrManagedConflict
	}
	return &TeamDeliveryExecution{Report: x, Params: params, audience: rec.audience, policy: x.BusinessPolicyStamp()}, nil
}

// LoadTeamDelivery binds an approval to the recipient's own current execution
// and gives the shared query path a guard to repeat before sending any SQL.
func (r *ReportSubscriptionRepository) LoadTeamDelivery(ctx context.Context, job *TeamDeliveryJob) (*TeamDeliveryExecution, error) {
	tx, err := r.pool.BeginSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	exec, err := r.teamDeliveryExecution(ctx, tx, job.ID, job.token, false)
	if err != nil {
		return nil, err
	}
	exec.Report.AdditionalCheck = func(runCtx context.Context) error {
		current, err := r.pool.BeginSnapshot(runCtx)
		if err != nil {
			return err
		}
		defer current.Rollback(context.WithoutCancel(runCtx))
		check, err := r.teamDeliveryExecution(runCtx, current, job.ID, job.token, false)
		if err != nil {
			return err
		}
		if check.policy != exec.policy || check.audience != exec.audience {
			return ErrManagedConflict
		}
		return nil
	}
	return exec, nil
}

func (r *ReportSubscriptionRepository) FinishTeamDelivery(ctx context.Context, job *TeamDeliveryJob, exec *TeamDeliveryExecution, result *connector.ExecResult) error {
	if result == nil || result.Truncated {
		return ErrManagedInput
	}
	x := exec.Report
	snapshot := ReportSubscriptionSnapshot{WorkspaceID: WorkspaceID(ctx), RunID: job.ID, ReportID: x.ReportID,
		PublicationNo: x.PublicationNo, RequesterID: ActorID(ctx), Name: x.Definition.Name,
		CreatedAt: time.Now().UTC(), Params: ReportParameterDisplayValues(x.Definition.Parameters, exec.Params),
		ManifestHash: x.Hash, ChartType: x.Definition.ChartType, ChartConfig: x.Definition.ChartConfig,
		Contract: x.Definition.DataContract, Result: *result, BusinessPolicyStamp: exec.policy}
	raw := canonicalReportJSON(snapshot)
	if len(raw) > 5<<20 {
		return ErrManagedInput
	}
	encrypted, err := r.cipher.Encrypt(string(raw))
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = lockTeamRecipient(ctx, tx, job.PlanID, job.UserID); err != nil {
		return err
	}
	current, err := r.teamDeliveryExecution(ctx, tx, job.ID, job.token, true)
	if err != nil {
		return err
	}
	if current.policy != exec.policy || current.audience != exec.audience {
		return ErrManagedConflict
	}
	var period int64
	err = tx.QueryRow(ctx, `UPDATE sys_report_team_deliveries SET status='succeeded',worker_token=NULL,lease_until=NULL,
		encrypted_snapshot=$3,snapshot_sha256=$4,audience_stamp=$5,expires_at=statement_timestamp()+interval '7 days'
		WHERE workspace_id=dbhub_workspace() AND id=$1::uuid AND worker_token=$2::uuid AND status='running'
		AND user_id=dbhub_actor() AND lease_until>statement_timestamp() RETURNING period_id`,
		job.ID, job.token, encrypted, reportDigest(snapshot), exec.audience).Scan(&period)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrManagedConflict
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT dbhub_notify_delivery(dbhub_workspace(),dbhub_actor(),$1,
		'团队报表期次已送达','请登录后查看本人期次。','team_report_delivery',$2)`, "team-delivery-ready:"+job.ID, period); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ReportSubscriptionRepository) FailTeamDelivery(ctx context.Context, job *TeamDeliveryJob, skip, review bool) error {
	status := "failed"
	if skip {
		status = "skipped"
	}
	_, err := r.pool.maintenance.Exec(ctx, `WITH stopped AS(
		UPDATE sys_report_team_deliveries SET status=$3,worker_token=NULL,lease_until=NULL
		WHERE id=$1::uuid AND worker_token=$2::uuid AND status='running' RETURNING workspace_id,plan_id,user_id,grant_version)
		UPDATE sys_report_team_recipient_access a SET enabled=false,needs_review=true,version=a.version+1,
		changed_at=statement_timestamp(),reason='收件人数据范围扩大，需重新送审'
		FROM stopped s WHERE $4 AND a.workspace_id=s.workspace_id AND a.plan_id=s.plan_id
		AND a.user_id=s.user_id AND a.version=s.grant_version`, job.ID, job.token, status, review)
	return err
}

// TeamDelivery reads the immutable result of one period as its recipient.
func (r *ReportSubscriptionRepository) TeamDelivery(ctx context.Context, id string) (*ReportSubscriptionSnapshot, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var plan int64
	err = tx.QueryRow(ctx, `SELECT plan_id FROM sys_report_team_deliveries
		WHERE workspace_id=dbhub_workspace() AND id=$1::uuid AND user_id=dbhub_actor()`, id).Scan(&plan)
	if err != nil {
		return nil, managedError(err)
	}
	if err = lockTeamRecipient(ctx, tx, plan, ActorID(ctx)); err != nil {
		return nil, err
	}
	exec, err := r.teamDeliveryExecution(ctx, tx, id, "", true)
	if err != nil {
		return nil, err
	}
	var encrypted, hash, audience string
	err = tx.QueryRow(ctx, `SELECT encrypted_snapshot,snapshot_sha256,audience_stamp
		FROM sys_report_team_deliveries WHERE workspace_id=dbhub_workspace() AND id=$1::uuid AND user_id=dbhub_actor()
		AND status='succeeded' AND expires_at>statement_timestamp()`, id).Scan(&encrypted, &hash, &audience)
	if err != nil {
		return nil, managedError(err)
	}
	var out ReportSubscriptionSnapshot
	if err = r.decode(encrypted, &out); err != nil {
		return nil, err
	}
	if reportDigest(out) != hash || out.WorkspaceID != WorkspaceID(ctx) || out.RequesterID != ActorID(ctx) ||
		out.RunID != id || out.ReportID != exec.Report.ReportID || out.PublicationNo != exec.Report.PublicationNo ||
		out.BusinessPolicyStamp != exec.policy || audience != exec.audience {
		return nil, ErrManagedConflict
	}
	out.BusinessPolicyStamp = ""
	return &out, tx.Commit(ctx)
}
