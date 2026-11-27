package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/reportexport"
)

type ReportExport struct {
	PermissionBridge    string     `json:"permission_bridge"`
	PermissionSupported bool       `json:"permission_supported"`
	ID                  int64      `json:"id"`
	UserID              int64      `json:"user_id"`
	Requester           string     `json:"requester_name"`
	ReportID            int64      `json:"report_id"`
	ReportName          string     `json:"report_name"`
	PublicationNo       int64      `json:"publication_no"`
	RequestID           string     `json:"request_id"`
	Format              string     `json:"format"`
	Reason              string     `json:"reason"`
	Rows                int        `json:"row_count"`
	SHA256              string     `json:"snapshot_sha256"`
	Status              string     `json:"status"`
	GenerationStatus    string     `json:"generation_status,omitempty"`
	Reviewer            string     `json:"reviewer_name"`
	Comment             string     `json:"review_comment"`
	Version             int64      `json:"version"`
	CreatedAt           time.Time  `json:"created_at"`
	ExpiresAt           time.Time  `json:"expires_at"`
	CanReview           bool       `json:"can_review"`
	CanDownload         bool       `json:"can_download"`
	FileExpiresAt       *time.Time `json:"file_expires_at,omitempty"`
	businessPolicyStamp string
	businessDeliveryErr error
}

const reportExportColumns = `id,COALESCE(user_id,0),requester_name,report_id,report_name,publication_no,request_id::text,format,reason,row_count,snapshot_hash,status,reviewer_name,review_comment,version,created_at,expires_at,permission_bridge,business_policy_stamp,
	CASE WHEN generation_request='' THEN '' ELSE COALESCE(
		(SELECT j.status FROM sys_report_export_jobs j WHERE j.workspace_id=sys_report_exports.workspace_id AND j.export_id=sys_report_exports.id),
		CASE WHEN status='pending' THEN 'awaiting_approval' WHEN status='rejected' THEN 'rejected' ELSE 'interrupted' END) END`

func scanReportExport(row interface{ Scan(...any) error }) (*ReportExport, error) {
	out := &ReportExport{}
	err := row.Scan(&out.ID, &out.UserID, &out.Requester, &out.ReportID, &out.ReportName, &out.PublicationNo, &out.RequestID, &out.Format, &out.Reason, &out.Rows, &out.SHA256, &out.Status, &out.Reviewer, &out.Comment, &out.Version, &out.CreatedAt, &out.ExpiresAt, &out.PermissionBridge, &out.businessPolicyStamp, &out.GenerationStatus)
	out.PermissionSupported = supportedPermissionBridge(out.PermissionBridge)
	return out, managedError(err)
}

func (r *ManagedReportRepository) CreateExport(ctx context.Context, x *ManagedExecution, requestID, format, reason string, params map[string]any, rowLimit int, result *connector.ExecResult) (*ReportExport, error) {
	p := x.Definition.ExportPolicy
	if p == nil || ValidateReportExportPolicy(p) != nil || !p.Enabled || result == nil || result.Truncated || len(result.Rows) > p.MaxRows {
		return nil, fmt.Errorf("%w: 此版本未批准导出或结果超出批准范围", ErrManagedInput)
	}
	if format != "csv" && format != "json" && format != "xlsx" || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 500 {
		return nil, fmt.Errorf("%w: 导出格式和用途无效", ErrManagedInput)
	}
	if err := r.CheckExecution(ctx, x); err != nil {
		return nil, err
	}
	queued, err := r.formalExportEnabled(ctx, r.pool, x)
	if err != nil {
		return nil, err
	}
	created := time.Now().UTC()
	preview := *result
	if queued && len(preview.Rows) > 20 {
		preview.Rows = preview.Rows[:20]
		preview.Truncated = true
	}
	snapshot := reportExportSnapshot(ctx, x, requestID, created, params, &preview)
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > formalExportMaxBytes {
		return nil, fmt.Errorf("%w: 导出结果超过16MiB，请缩小筛选范围", ErrManagedInput)
	}
	hash := reportSnapshotHash(snapshot)
	var encrypted, generationRequest, generationPreview string
	var memberVersion int64
	if queued {
		generationRequest, err = r.sealFormalExport(ctx, x, requestID, params, rowLimit)
		if err != nil {
			return nil, err
		}
		generationPreview, err = r.cipher.Encrypt(string(raw))
		memberVersion = MemberEpoch(ctx)
	} else {
		if _, _, err = reportexport.Encode(snapshot, format, hash); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrManagedInput, err)
		}
		encrypted, err = r.cipher.Encrypt(string(raw))
	}
	if err != nil {
		return nil, err
	}
	var id int64
	err = r.pool.QueryRow(ctx, `INSERT INTO sys_report_exports(user_id,requester_name,report_id,version_id,publication_no,report_name,request_id,format,reason,row_count,snapshot_hash,encrypted_payload,created_at,expires_at,business_policy_stamp,generation_request,generation_preview,generation_member_version)
 VALUES($1,(SELECT username FROM sys_users WHERE id=$1),$2,$3,$4,$5,$6::uuid,$7,$8,$9,$10,$11,$12::timestamptz,$12::timestamptz+interval '7 days',$13,$14,$15,$16) RETURNING id`,
		ActorID(ctx), x.ReportID, x.Manifest.VersionID, x.PublicationNo, x.Definition.Name, requestID, format, reason, len(preview.Rows), hash, encrypted, created, x.BusinessPolicyStamp(), generationRequest, generationPreview, memberVersion).Scan(&id)
	if err != nil {
		return nil, err
	}
	auditctx.Update(ctx, func(d *auditctx.Data) { d.ExportID = id })
	return r.Export(ctx, id)
}

func reportExportSnapshot(ctx context.Context, x *ManagedExecution, requestID string, created time.Time, params map[string]any, result *connector.ExecResult) *reportexport.Snapshot {
	snapshot := &reportexport.Snapshot{WorkspaceID: WorkspaceID(ctx), RequesterID: ActorID(ctx), RequestID: requestID, ManifestHash: x.Hash, ReportID: x.ReportID, Name: x.Definition.Name, Version: x.Version, PublicationNo: x.PublicationNo, CreatedAt: created, Params: ReportParameterDisplayValues(x.Definition.Parameters, params), Result: *result, Types: map[string]string{}}
	var chart struct {
		Display *ReportDisplay `json:"display"`
	}
	if json.Unmarshal(x.Definition.ChartConfig, &chart) == nil && chart.Display != nil {
		snapshot.Labels = chart.Display.Labels
	}
	if contract := x.Definition.DataContract; contract != nil {
		snapshot.Unit = contract.Unit
		snapshot.Currency = contract.Currency
		snapshot.Timezone = contract.Timezone
		snapshot.Scope = contract.ScopeDescription
		for _, column := range contract.Schema {
			snapshot.Types[column.Name] = column.Type
		}
	}
	return snapshot
}

func (r *ManagedReportRepository) Export(ctx context.Context, id int64) (*ReportExport, error) {
	out, err := scanReportExport(r.pool.QueryRow(ctx, `SELECT `+reportExportColumns+` FROM sys_report_exports WHERE id=$1 AND (user_id=$2 OR $3)`, id, ActorID(ctx), ActorExportReviewer(ctx)))
	if err != nil {
		return nil, err
	}
	current, accessErr := r.ResolvePublished(ctx, out.ReportID, out.PublicationNo)
	allowed := out.PermissionSupported && accessErr == nil && current.Definition.ExportPolicy != nil && current.Definition.ExportPolicy.Enabled && out.ExpiresAt.After(time.Now())
	out.CanReview = allowed && ActorExportReviewer(ctx) && out.UserID != ActorID(ctx) && out.Status == "pending"
	out.CanDownload = allowed && out.GenerationStatus == "" && out.Status == "approved" && (out.UserID == ActorID(ctx) || ActorExportReviewer(ctx))
	if out.CanDownload && (current.businessBinding != "" || out.businessPolicyStamp != "") {
		out.CanDownload = false
		if out.UserID == ActorID(ctx) {
			out.businessDeliveryErr = r.checkBusinessReportSnapshot(ctx, current, out.businessPolicyStamp)
			out.CanDownload = out.businessDeliveryErr == nil
		}
	}
	return out, nil
}

func (r *ManagedReportRepository) Exports(ctx context.Context, limit, offset int) ([]ReportExport, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_report_exports WHERE user_id=$1 OR $2`, ActorID(ctx), ActorExportReviewer(ctx)).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id FROM sys_report_exports WHERE user_id=$1 OR $2 ORDER BY id DESC LIMIT $3 OFFSET $4`, ActorID(ctx), ActorExportReviewer(ctx), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	out := []ReportExport{}
	for _, id := range ids {
		record, e := r.Export(ctx, id)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, *record)
	}
	return out, total, nil
}

func (r *ManagedReportRepository) ReviewExport(ctx context.Context, id, expected int64, decision, comment string) error {
	if !ActorExportReviewer(ctx) || decision != "approved" && decision != "rejected" || len([]rune(comment)) > 500 || decision == "rejected" && strings.TrimSpace(comment) == "" {
		return ErrManagedDenied
	}
	out, err := r.Export(ctx, id)
	if err != nil {
		return err
	}
	if out.Status != "pending" || out.Version != expected {
		return ErrManagedConflict
	}
	if !out.CanReview {
		return ErrManagedDenied
	}
	x, err := r.ResolvePublished(ctx, out.ReportID, out.PublicationNo)
	if err != nil {
		return err
	}
	if decision == "approved" && out.GenerationStatus != "" {
		enabled, err := r.formalExportEnabled(ctx, r.pool, x)
		if err != nil {
			return err
		}
		if !enabled {
			return ErrManagedConflict
		}
		if err = r.checkBusinessReportSnapshot(ctx, x, out.businessPolicyStamp); err != nil {
			return err
		}
	}
	release, err := r.AcquireExecutionLease(ctx, x)
	if err != nil {
		return err
	}
	defer release()
	if err = auditctx.Checkpoint(ctx); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, err := tx.Exec(ctx, `UPDATE sys_report_exports SET status=$3,reviewed_by=$4,reviewer_name=(SELECT username FROM sys_users WHERE id=$4),review_comment=$5,reviewed_at=now(),version=version+1 WHERE id=$1 AND version=$2 AND status='pending' AND expires_at>now() AND user_id<>$4`, id, expected, decision, ActorID(ctx), comment)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrManagedConflict
	}
	if decision == "approved" && out.GenerationStatus != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO sys_report_export_jobs(id,workspace_id,export_id,approval_version)
			VALUES($1::uuid,dbhub_workspace(),$2,$3)`, dashboardUUID(), id, expected+1); err != nil {
			return err
		}
	}
	auditctx.Update(ctx, func(d *auditctx.Data) {
		d.ExportID = id
		d.ReportID = out.ReportID
		d.ReportPublicationNo = out.PublicationNo
		d.ReportMode = "managed"
	})
	return tx.Commit(ctx)
}

func (r *ManagedReportRepository) DownloadExport(ctx context.Context, id int64) (*ReportExport, *reportexport.Snapshot, func(), error) {
	return r.exportPayload(ctx, id, false)
}

func (r *ManagedReportRepository) PreviewExport(ctx context.Context, id int64) (*ReportExport, *reportexport.Snapshot, func(), error) {
	return r.exportPayload(ctx, id, true)
}

// File encoding can take longer than the remaining approval/file lifetime.
// Re-read current metadata immediately before delivery, not an earlier Go
// object or transaction timestamp. The caller still holds its execution lease.
func (r *ManagedReportRepository) CheckExportDelivery(ctx context.Context, out *ReportExport, preview bool) error {
	if out == nil || !out.PermissionSupported {
		return ErrManagedDenied
	}
	var allowed bool
	e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_report_exports
 WHERE id=$1 AND version=$2 AND snapshot_hash=$3 AND permission_bridge=$4
 AND permission_bridge IN('legacy-frozen-v1','phase1-bridge-2')
 AND expires_at>statement_timestamp() AND (user_id=$5 OR $6)
 AND business_policy_stamp=$8
 AND ($7 OR generation_request='')
 AND (status='approved' OR ($7 AND status='pending' AND $6 AND user_id<>$5)))`, out.ID, out.Version, out.SHA256, out.PermissionBridge, ActorID(ctx), ActorExportReviewer(ctx), preview, out.businessPolicyStamp).Scan(&allowed)
	if e != nil {
		return e
	}
	if !allowed {
		return ErrManagedDenied
	}
	x, err := r.ResolvePublished(ctx, out.ReportID, out.PublicationNo)
	if err != nil {
		return err
	}
	if x.businessBinding != "" || out.businessPolicyStamp != "" {
		if !preview && out.UserID != ActorID(ctx) {
			return ErrManagedDenied
		}
		return r.checkBusinessReportSnapshot(ctx, x, out.businessPolicyStamp)
	}
	return nil
}

func (r *ManagedReportRepository) exportPayload(ctx context.Context, id int64, preview bool) (*ReportExport, *reportexport.Snapshot, func(), error) {
	out, err := r.Export(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	if !out.CanDownload && !(preview && out.CanReview) {
		if errors.Is(out.businessDeliveryErr, errCustodyReadBoundary) {
			return nil, nil, nil, out.businessDeliveryErr
		}
		return nil, nil, nil, ErrManagedDenied
	}
	x, err := r.ResolvePublished(ctx, out.ReportID, out.PublicationNo)
	if err != nil {
		return nil, nil, nil, err
	}
	if x.businessBinding != "" || out.businessPolicyStamp != "" {
		if !preview && out.UserID != ActorID(ctx) {
			return nil, nil, nil, ErrManagedDenied
		}
		if err = r.checkBusinessReportSnapshot(ctx, x, out.businessPolicyStamp); err != nil {
			return nil, nil, nil, err
		}
	}
	release, err := r.AcquireExecutionLease(ctx, x)
	if err != nil {
		return nil, nil, nil, err
	}
	failed := true
	defer func() {
		if failed {
			release()
		}
	}()
	var encrypted string
	if err = r.pool.QueryRow(ctx, `SELECT CASE WHEN generation_request='' THEN encrypted_payload ELSE generation_preview END
		FROM sys_report_exports WHERE permission_bridge IN('legacy-frozen-v1','phase1-bridge-2') AND id=$1
		AND (status='approved' OR ($4 AND status='pending')) AND ($4 OR generation_request='')
		AND expires_at>statement_timestamp() AND (user_id=$2 OR $3)`,
		id, ActorID(ctx), ActorExportReviewer(ctx), preview).Scan(&encrypted); err != nil {
		return nil, nil, nil, managedError(err)
	}
	raw, err := r.cipher.Decrypt(encrypted)
	if err != nil {
		return nil, nil, nil, err
	}
	var snapshot reportexport.Snapshot
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	if err = decoder.Decode(&snapshot); err != nil {
		return nil, nil, nil, err
	}
	if snapshot.WorkspaceID != WorkspaceID(ctx) || snapshot.RequestID != out.RequestID || out.UserID != 0 && snapshot.RequesterID != out.UserID || snapshot.Version != x.Version || snapshot.ReportID != out.ReportID || snapshot.PublicationNo != out.PublicationNo || len(snapshot.Result.Rows) != out.Rows || reportSnapshotHash(&snapshot) != out.SHA256 {
		return nil, nil, nil, ErrManagedConflict
	}
	auditctx.Update(ctx, func(d *auditctx.Data) {
		d.ExportID = id
		d.ReportID = out.ReportID
		d.ReportPublicationNo = out.PublicationNo
		d.ReportMode = "managed"
	})
	if err = auditctx.Checkpoint(ctx); err != nil {
		return nil, nil, nil, err
	}
	failed = false
	return out, &snapshot, release, nil
}

func reportSnapshotHash(snapshot *reportexport.Snapshot) string {
	digest := sha256.Sum256(canonicalReportJSON([]any{snapshot.WorkspaceID, snapshot.RequesterID, snapshot.RequestID, snapshot.ReportID, snapshot.Version, snapshot.PublicationNo, snapshot.CreatedAt, snapshot.ManifestHash, snapshot.Params, snapshot.Result.Columns, snapshot.Result.Rows}))
	return hex.EncodeToString(digest[:])
}
