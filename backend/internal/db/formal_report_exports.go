package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/reportexport"
)

const formalExportMaxBytes = 16 << 20

// FormalExportJob carries the leased queue receipt and the original actor.
type FormalExportJob struct {
	ID, workerToken                    string
	WorkspaceID, UserID, MemberVersion int64
	ExportID, approvalVersion          int64
}

type formalExportInput struct {
	Workspace, User, Report, Publication, AccountVersion, Generation int64
	RequestID, Manifest, Policy                                      string
	Params                                                           map[string]any
	MaxRows                                                          int
}

type FormalExportExecution struct {
	Report  *ManagedExecution
	Params  map[string]any
	Format  string
	Reason  string
	MaxRows int
	input   formalExportInput
}

func (r *ManagedReportRepository) formalExportEnabled(ctx context.Context, q reportQueryer, x *ManagedExecution) (bool, error) {
	var enabled bool
	err := q.QueryRow(ctx, `SELECT b.isolation_id=$2 AND EXISTS (
		SELECT 1 FROM sys_iam_isolation_guard g WHERE g.isolation_id=b.isolation_id AND g.database_name=current_database())
		AND EXISTS (SELECT 1 FROM sys_report_review_checks c WHERE c.workspace_id=f.workspace_id
			AND c.review_id=$3 AND (NOT c.requires_data OR c.data_reviewer_id IS NOT NULL))
		FROM sys_iam_isolated_formal_exports f LEFT JOIN sys_iam_isolated_business_reports b USING(workspace_id,report_id)
		WHERE f.workspace_id=dbhub_workspace() AND f.report_id=$1`, x.ReportID, r.phase2ReportIsolation, x.ReviewID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err == nil && (!enabled || x.businessBinding == "") {
		return false, ErrManagedDenied
	}
	return enabled, err
}

func (r *ManagedReportRepository) sealFormalExport(ctx context.Context, x *ManagedExecution, requestID string, params map[string]any, maxRows int) (string, error) {
	stamp := x.BusinessPolicyStamp()
	if MemberEpoch(ctx) < 1 || stamp == "" || maxRows < 1 || len(requestID) != 36 {
		return "", ErrManagedConflict
	}
	input := formalExportInput{
		Workspace: WorkspaceID(ctx), User: ActorID(ctx), Report: x.ReportID, Publication: x.PublicationNo,
		AccountVersion: x.AccountVersion, Generation: x.Generation, RequestID: requestID,
		Manifest: x.Hash, Policy: stamp, Params: params, MaxRows: maxRows,
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	return r.cipher.Encrypt(string(raw))
}

// ClaimFormalExport uses the report delivery queue lock and a bounded worker
// lease. An interrupted job keeps its receipt and is never automatically run again.
func (r *ManagedReportRepository) ClaimFormalExport(ctx context.Context) (*FormalExportJob, error) {
	if r.phase2ReportIsolation == "" {
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
	if _, err = tx.Exec(ctx, `UPDATE sys_report_export_jobs SET status='interrupted',worker_token=NULL,lease_until=NULL,finished_at=statement_timestamp()
		WHERE status='running' AND lease_until<=statement_timestamp()`); err != nil {
		return nil, err
	}
	job := &FormalExportJob{workerToken: dashboardUUID()}
	err = tx.QueryRow(ctx, `WITH claimed AS (
		UPDATE sys_report_export_jobs SET status='running',worker_token=$1::uuid,lease_until=statement_timestamp()+interval '2 minutes'
		WHERE id=(SELECT id FROM sys_report_export_jobs WHERE status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
		AND (SELECT count(*) FROM sys_report_export_jobs WHERE status='running')<2
		RETURNING id,workspace_id,export_id,approval_version
	) SELECT j.id::text,j.workspace_id,j.export_id,j.approval_version,COALESCE(e.user_id,0),e.generation_member_version
	FROM claimed j JOIN sys_report_exports e ON e.workspace_id=j.workspace_id AND e.id=j.export_id`, job.workerToken).
		Scan(&job.ID, &job.WorkspaceID, &job.ExportID, &job.approvalVersion, &job.UserID, &job.MemberVersion)
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

func (r *ManagedReportRepository) LoadFormalExport(ctx context.Context, job *FormalExportJob) (*FormalExportExecution, error) {
	var encrypted, requestID, stamp string
	var report, publication int64
	out := &FormalExportExecution{}
	err := r.pool.QueryRow(ctx, `SELECT e.report_id,e.publication_no,e.request_id::text,e.format,e.reason,e.generation_request,e.business_policy_stamp
		FROM sys_report_exports e JOIN sys_report_export_jobs j ON j.workspace_id=e.workspace_id AND j.export_id=e.id
		WHERE e.id=$1 AND j.id=$2::uuid AND j.worker_token=$3::uuid AND j.status='running'
		AND j.lease_until>statement_timestamp() AND e.status='approved' AND e.version=j.approval_version
		AND e.user_id=dbhub_actor() AND e.generation_member_version=dbhub_member_epoch()
		AND e.expires_at>statement_timestamp() AND e.permission_bridge IN ('legacy-frozen-v1','phase1-bridge-2')`,
		job.ExportID, job.ID, job.workerToken).Scan(&report, &publication, &requestID, &out.Format, &out.Reason, &encrypted, &stamp)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrManagedConflict
	}
	if err != nil {
		return nil, err
	}
	raw, err := r.cipher.Decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&out.input); err != nil {
		return nil, err
	}
	in := out.input
	out.MaxRows = in.MaxRows
	if in.Workspace != WorkspaceID(ctx) || in.User != ActorID(ctx) || in.Report != report ||
		in.Publication != publication || in.RequestID != requestID || in.Policy != stamp {
		return nil, ErrManagedConflict
	}
	x, err := r.ResolvePublished(ctx, report, publication)
	if err != nil {
		return nil, err
	}
	out.Report = x
	if err = r.checkFormalExport(ctx, out); err != nil {
		return nil, err
	}
	out.Params, err = ResolveReportParameters(x.Definition.Parameters, in.Params, time.Now())
	if err != nil || reportDigest(out.Params) != reportDigest(in.Params) {
		return nil, ErrManagedConflict
	}
	return out, nil
}

func (r *ManagedReportRepository) checkFormalExport(ctx context.Context, execution *FormalExportExecution) error {
	x, input := execution.Report, execution.input
	enabled, err := r.formalExportEnabled(ctx, r.pool, x)
	if err != nil {
		return err
	}
	if !enabled || input.Manifest != x.Hash || input.AccountVersion != x.AccountVersion || input.Generation != x.Generation ||
		x.Definition.ExportPolicy == nil || ValidateReportExportPolicy(x.Definition.ExportPolicy) != nil {
		return ErrManagedConflict
	}
	if err = r.checkBusinessReportSnapshot(ctx, x, input.Policy); err != nil {
		return err
	}
	settings, err := (&QueryControls{pool: r.pool}).Settings(ctx, ActorID(ctx))
	if err != nil {
		return err
	}
	if input.MaxRows < 1 || input.MaxRows != min(settings.MaxRows, x.Manifest.MaxRows, x.Definition.ExportPolicy.MaxRows, BusinessReportMaxRows) {
		return ErrManagedConflict
	}
	return r.CheckExecution(ctx, x)
}

// FinishFormalExport encodes the fresh result and seals it with the durable
// job in one transaction. The old download route has no access to this file.
func (r *ManagedReportRepository) FinishFormalExport(ctx context.Context, job *FormalExportJob, execution *FormalExportExecution, result *connector.ExecResult) error {
	if result == nil || result.Truncated || len(result.Rows) > execution.input.MaxRows {
		return ErrManagedInput
	}
	x := execution.Report
	snapshot := reportExportSnapshot(ctx, x, job.ID, time.Now().UTC(), execution.Params, result)
	payload, err := json.Marshal(snapshot)
	if err != nil || len(payload) > formalExportMaxBytes {
		return ErrManagedInput
	}
	snapshotHash := reportSnapshotHash(snapshot)
	file, _, err := reportexport.Encode(snapshot, execution.Format, snapshotHash)
	if err != nil || len(file) == 0 || len(file) > formalExportMaxBytes {
		return ErrManagedInput
	}
	digest := sha256.Sum256(file)
	encrypted, err := r.cipher.Encrypt(string(file))
	if err != nil {
		return err
	}
	if err = r.checkFormalExport(ctx, execution); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var reportID int64
	err = tx.QueryRow(ctx, `SELECT m.id FROM sys_managed_reports m JOIN sys_report_releases r ON r.id=m.active_release_id
		WHERE m.workspace_id=dbhub_workspace() AND m.id=$1 AND m.publication_no=$2 AND r.review_id=$3 FOR SHARE OF m`,
		x.ReportID, x.PublicationNo, x.ReviewID).Scan(&reportID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrManagedConflict
	}
	if err != nil {
		return err
	}
	_, _, account, err := r.loadAccount(ctx, tx, x.AccountID)
	if err != nil {
		return err
	}
	if account.Version != x.AccountVersion || account.Generation != x.Generation {
		return ErrManagedConflict
	}
	enabled, err := r.formalExportEnabled(ctx, tx, x)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrManagedConflict
	}
	if err = lockBusinessReportPolicy(ctx, tx, x.businessBinding); err != nil {
		return err
	}
	current := *x
	current.businessBinding = ""
	if err = r.useBusinessReportBinding(ctx, tx, &current); err != nil {
		return err
	}
	if current.businessBinding != x.businessBinding {
		return ErrManagedConflict
	}
	if err = r.prepareBusinessReport(ctx, tx, &current); err != nil {
		return err
	}
	if current.BusinessPolicyStamp() != execution.input.Policy {
		return ErrManagedConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE sys_report_export_jobs j SET status='succeeded',worker_token=NULL,lease_until=NULL,finished_at=statement_timestamp()
		FROM sys_report_exports e WHERE e.workspace_id=j.workspace_id AND e.id=j.export_id
		AND j.id=$1::uuid AND j.worker_token=$2::uuid AND j.status='running' AND j.lease_until>statement_timestamp()
		AND e.status='approved' AND e.version=$3 AND e.version=j.approval_version AND e.expires_at>statement_timestamp()`,
		job.ID, job.workerToken, job.approvalVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrManagedConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sys_report_export_files(workspace_id,export_id,format,encrypted_file,sha256,snapshot_sha256,row_count,size_bytes)
		VALUES(dbhub_workspace(),$1,$2,$3,$4,$5,$6,$7)`, job.ExportID, execution.Format, encrypted,
		hex.EncodeToString(digest[:]), snapshotHash, len(result.Rows), len(file)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT dbhub_notify_delivery(dbhub_workspace(),dbhub_actor(),$1,
		'正式导出已生成','请登录后领取正式文件。','formal_export_delivery',$2)`, "formal-export-ready:"+job.ID, job.ExportID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ManagedReportRepository) FailFormalExport(ctx context.Context, job *FormalExportJob) error {
	_, err := r.pool.maintenance.Exec(ctx, `UPDATE sys_report_export_jobs
		SET status='failed',worker_token=NULL,lease_until=NULL,finished_at=statement_timestamp()
		WHERE id=$1::uuid AND worker_token=$2::uuid AND status='running'`, job.ID, job.workerToken)
	return err
}
