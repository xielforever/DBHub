package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
)

type FormalExportCredential struct {
	Credential string    `json:"credential"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type FormalExportFile struct {
	Format, SHA256 string
	ReportID       int64
	Bytes          []byte
}

type formalPickup struct {
	execution                       *FormalExportExecution
	format, hash, session, audience string
	version                         int64
	expiresAt                       time.Time
}

func (r *ManagedReportRepository) readFormalPickup(ctx context.Context, id int64) (formalPickup, error) {
	var out formalPickup
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.SessionID == "" || MemberEpoch(ctx) < 1 {
		return out, ErrManagedDenied
	}
	out.session = r.cipher.Fingerprint([]byte("formal-export-session:" + claims.SessionID))
	var raw, policy, request string
	var report, publication int64
	err := r.pool.QueryRow(ctx, `SELECT e.report_id,e.publication_no,e.generation_request,e.business_policy_stamp,
		e.request_id::text,e.version,f.format,f.sha256,f.expires_at
		FROM sys_report_exports e JOIN sys_report_export_files f ON f.workspace_id=e.workspace_id AND f.export_id=e.id
		JOIN sys_report_export_jobs j ON j.workspace_id=e.workspace_id AND j.export_id=e.id
		WHERE e.workspace_id=dbhub_workspace() AND e.id=$1 AND e.user_id=dbhub_actor()
		AND e.generation_member_version=dbhub_member_epoch() AND e.status='approved' AND e.version=j.approval_version
		AND j.status='succeeded' AND f.expires_at>statement_timestamp()
		AND e.permission_bridge IN ('legacy-frozen-v1','phase1-bridge-2')`, id).
		Scan(&report, &publication, &raw, &policy, &request, &out.version, &out.format, &out.hash, &out.expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrManagedDenied
	}
	if err != nil {
		return out, err
	}
	decoded, err := r.cipher.Decrypt(raw)
	if err != nil {
		return out, err
	}
	var input formalExportInput
	decoder := json.NewDecoder(strings.NewReader(decoded))
	decoder.UseNumber()
	if err = decoder.Decode(&input); err != nil {
		return out, err
	}
	if input.Workspace != WorkspaceID(ctx) || input.User != ActorID(ctx) || input.Report != report || input.Publication != publication ||
		input.RequestID != request || input.Policy != policy {
		return out, ErrManagedConflict
	}
	x, err := r.ResolvePublished(ctx, report, publication)
	if err != nil {
		return out, err
	}
	out.execution = &FormalExportExecution{Report: x, input: input}
	if err = r.checkFormalExport(ctx, out.execution); err != nil {
		return out, err
	}
	var groups []int64
	if !containsID(x.Manifest.UserIDs, ActorID(ctx)) {
		err = r.pool.QueryRow(ctx, `SELECT COALESCE(array_agg(g.version ORDER BY g.id),'{}'::bigint[])
			FROM sys_user_groups g JOIN sys_user_group_members gm ON gm.group_id=g.id
			WHERE g.id=ANY($1::bigint[]) AND gm.user_id=dbhub_actor()`, nonNilIDs(x.Manifest.GroupIDs)).Scan(&groups)
		if err != nil {
			return out, err
		}
	}
	var config, epoch, revocations int64
	if claims.IAM != nil {
		config, epoch, revocations = claims.IAM.ConfigVersion, claims.IAM.AuthorizationEpoch, claims.IAM.RevocationSequence
	}
	out.audience = reportDigest([]any{config, epoch, revocations, groups})
	return out, nil
}

func (r *ManagedReportRepository) formalPickup(ctx context.Context, id int64) (formalPickup, error) {
	out, err := r.readFormalPickup(ctx, id)
	if err != nil {
		return out, err
	}
	auditctx.Update(ctx, func(d *auditctx.Data) {
		d.ExportID, d.ReportID, d.ReportPublicationNo = id, out.execution.input.Report, out.execution.input.Publication
	})
	return out, auditctx.Checkpoint(ctx)
}

// ExportDetail returns the same approval record plus the current applicant's
// formal file eligibility, without creating or redeeming any credential.
func (r *ManagedReportRepository) ExportDetail(ctx context.Context, id int64) (*ReportExport, error) {
	record, err := r.Export(ctx, id)
	if err != nil || record.GenerationStatus != "succeeded" || record.UserID != ActorID(ctx) {
		return record, err
	}
	pickup, err := r.readFormalPickup(ctx, id)
	if err == nil {
		record.FileExpiresAt = &pickup.expiresAt
	}
	if errors.Is(err, ErrManagedDenied) || errors.Is(err, ErrManagedConflict) || errors.Is(err, ErrNotFound) {
		return record, nil
	}
	return record, err
}

// IssueFormalExport returns a secret once and persists only its digest.
func (r *ManagedReportRepository) IssueFormalExport(ctx context.Context, id int64) (FormalExportCredential, error) {
	var out FormalExportCredential
	pickup, err := r.formalPickup(ctx, id)
	if err != nil {
		return out, err
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return out, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	digest := r.cipher.Fingerprint([]byte(token))
	err = r.pool.QueryRow(ctx, `INSERT INTO sys_report_export_credentials(digest,workspace_id,export_id,user_id,member_version,session_digest,
		file_sha256,audience_stamp,approval_version,expires_at)
		SELECT $1,f.workspace_id,f.export_id,dbhub_actor(),dbhub_member_epoch(),$2,f.sha256,$3,$4,
		LEAST(f.expires_at,statement_timestamp()+interval '10 minutes')
		FROM sys_report_export_files f WHERE f.workspace_id=dbhub_workspace() AND f.export_id=$5 AND f.sha256=$6
		AND f.expires_at>statement_timestamp() RETURNING expires_at`, digest, pickup.session, pickup.audience, pickup.version, id, pickup.hash).
		Scan(&out.ExpiresAt)
	if err != nil {
		return out, managedError(err)
	}
	out.Credential = token
	return out, nil
}

// RedeemFormalExport checks the original file and consumes a bound credential
// atomically, before the caller sends any binary response.
func (r *ManagedReportRepository) RedeemFormalExport(ctx context.Context, id int64, secret string) (FormalExportFile, error) {
	var out FormalExportFile
	token, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(token) != 32 {
		return out, ErrManagedInput
	}
	pickup, err := r.formalPickup(ctx, id)
	if err != nil {
		return out, err
	}
	digest := r.cipher.Fingerprint([]byte(secret))
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var encrypted string
	err = tx.QueryRow(ctx, `WITH redeemed AS(
		UPDATE sys_report_export_credentials SET redeemed_at=statement_timestamp() WHERE digest=$1
		AND workspace_id=dbhub_workspace() AND export_id=$2 AND user_id=dbhub_actor()
		AND session_digest=$3 AND audience_stamp=$4 AND approval_version=$5 AND file_sha256=$6
		AND redeemed_at IS NULL AND expires_at>statement_timestamp() RETURNING export_id)
		SELECT f.encrypted_file,f.format,f.sha256 FROM redeemed r JOIN sys_report_export_files f ON f.export_id=r.export_id
		WHERE f.workspace_id=dbhub_workspace() AND f.sha256=$6 AND f.expires_at>statement_timestamp()`,
		digest, id, pickup.session, pickup.audience, pickup.version, pickup.hash).
		Scan(&encrypted, &out.Format, &out.SHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrManagedDenied
	}
	if err != nil {
		return out, err
	}
	raw, err := r.cipher.Decrypt(encrypted)
	if err != nil {
		return out, err
	}
	out.Bytes = []byte(raw)
	hash := sha256.Sum256(out.Bytes)
	if len(out.Bytes) > formalExportMaxBytes || hex.EncodeToString(hash[:]) != out.SHA256 {
		return FormalExportFile{}, ErrManagedConflict
	}
	if err = r.checkFormalExport(ctx, pickup.execution); err != nil {
		return FormalExportFile{}, err
	}
	var current bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_report_export_credentials c
		JOIN sys_report_export_files f ON f.workspace_id=c.workspace_id AND f.export_id=c.export_id
		WHERE c.digest=$1 AND c.redeemed_at IS NOT NULL AND c.expires_at>statement_timestamp()
		AND f.sha256=c.file_sha256 AND f.expires_at>statement_timestamp())`, digest).Scan(&current)
	if err != nil {
		return FormalExportFile{}, err
	}
	if !current {
		return FormalExportFile{}, ErrManagedDenied
	}
	out.ReportID = pickup.execution.Report.ReportID
	return out, tx.Commit(ctx)
}
