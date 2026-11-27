package db

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/reportexport"
)

type DashboardDeliveredCard struct {
	Key      string                 `json:"tile_key"`
	Status   string                 `json:"status"`
	Quality  *ContractResult        `json:"quality,omitempty"`
	Snapshot *reportexport.Snapshot `json:"snapshot,omitempty"`
}

type DashboardDeliveryResult struct {
	DashboardID int64                    `json:"dashboard_id"`
	Publication int64                    `json:"publication_no"`
	Cards       []DashboardDeliveredCard `json:"cards"`
}

type DashboardDeliveryOutput struct {
	Kind        string                   `json:"kind"`
	Format      string                   `json:"format,omitempty"`
	DashboardID int64                    `json:"dashboard_id"`
	Publication int64                    `json:"publication_no"`
	TileKey     string                   `json:"tile_key,omitempty"`
	Snapshot    *DashboardDeliveryResult `json:"snapshot,omitempty"`
	SHA256      string                   `json:"sha256"`
	ExpiresAt   time.Time                `json:"expires_at"`
	File        []byte                   `json:"-"`
}

type DashboardDeliveryState struct {
	ID        int64      `json:"id"`
	Recipient int64      `json:"user_id"`
	Slot      time.Time  `json:"slot"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// DashboardOwnDeliveryState links a recipient's receipt to their original plan.
type DashboardOwnDeliveryState struct {
	DashboardDeliveryState
	PlanID      int64  `json:"plan_id"`
	DashboardID int64  `json:"dashboard_id"`
	Kind        string `json:"kind"`
}

func (r *ManagedDashboardRepository) DashboardOwnDeliveryStates(ctx context.Context) ([]DashboardOwnDeliveryState, error) {
	rows, err := r.pool.Query(ctx, `SELECT d.id,d.user_id,d.slot,d.status,d.expires_at,p.id,p.dashboard_id,p.kind
		FROM sys_dashboard_deliveries d JOIN sys_dashboard_delivery_plans p ON p.workspace_id=d.workspace_id AND p.id=d.plan_id
		WHERE d.workspace_id=dbhub_workspace() AND d.user_id=dbhub_actor() ORDER BY d.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (DashboardOwnDeliveryState, error) {
		var state DashboardOwnDeliveryState
		err := row.Scan(&state.ID, &state.Recipient, &state.Slot, &state.Status, &state.ExpiresAt, &state.PlanID, &state.DashboardID, &state.Kind)
		return state, err
	})
}

func (r *ManagedDashboardRepository) DashboardDeliveryStates(ctx context.Context, plan int64) ([]DashboardDeliveryState, error) {
	var owner bool
	if err := r.pool.QueryRow(ctx, `SELECT dbhub_dashboard_delivery_open($1,dbhub_actor(),'owner')`, plan).Scan(&owner); err != nil {
		return nil, err
	}
	if !owner {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT * FROM dbhub_dashboard_delivery_statuses($1)`, plan)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[DashboardDeliveryState])
}

func (w DashboardDeliveryWork) DeliveryTiles() []DashboardViewTile {
	out := make([]DashboardViewTile, 0, len(w.proof.Cards))
	for i, c := range w.proof.Cards {
		v := DashboardViewTile{DashboardTile: DashboardTile{Key: c.Key, Required: w.basis.Cards[i].Required}, Status: "restricted"}
		if c.Policy != "" {
			v.Status = "ready"
		}
		out = append(out, v)
	}
	return out
}

func (w DashboardDeliveryWork) CollectDashboardCard(ctx context.Context, x *DashboardExecution, result connector.ExecResult, qt ContractResult) (DashboardDeliveredCard, error) {
	if result.Truncated || qt.Freshness == "stale" {
		return DashboardDeliveredCard{}, ErrManagedConflict
	}
	if w.plan.Kind == "export" {
		policy := x.Report.Definition.ExportPolicy
		if policy == nil || !policy.Enabled || len(result.Rows) > min(policy.MaxRows, x.Report.Manifest.MaxRows) {
			return DashboardDeliveredCard{}, ErrManagedDenied
		}
	}
	return DashboardDeliveredCard{Key: x.TileKey, Status: "ready", Quality: &qt,
		Snapshot: reportExportSnapshot(ctx, x.Report, x.ExecutionID, qt.QueryCompletedAt, x.Params, &result)}, nil
}

func dashboardDeliveryFile(result DashboardDeliveryResult, format string, single bool) ([]byte, error) {
	if single {
		if len(result.Cards) != 1 || result.Cards[0].Snapshot == nil {
			return nil, ErrManagedConflict
		}
		s := result.Cards[0].Snapshot
		out, _, err := reportexport.Encode(s, format, reportSnapshotHash(s))
		return out, err
	}
	type cardFile struct {
		Key     string          `json:"tile_key"`
		Status  string          `json:"status"`
		File    string          `json:"file,omitempty"`
		Quality *ContractResult `json:"quality,omitempty"`
	}
	manifest := struct {
		Dashboard, Publication int64
		Cards                  []cardFile
	}{Dashboard: result.DashboardID, Publication: result.Publication}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	total := 0
	for i, c := range result.Cards {
		entry := cardFile{Key: c.Key, Status: c.Status, Quality: c.Quality}
		if c.Snapshot != nil {
			encoded, _, err := reportexport.Encode(c.Snapshot, format, reportSnapshotHash(c.Snapshot))
			if err != nil {
				return nil, err
			}
			total += len(encoded)
			if total > formalExportMaxBytes {
				return nil, ErrManagedInput
			}
			entry.File = fmt.Sprintf("%02d.%s", i+1, format)
			file, err := archive.Create(entry.File)
			if err != nil {
				return nil, err
			}
			if _, err = file.Write(encoded); err != nil {
				return nil, err
			}
		}
		manifest.Cards = append(manifest.Cards, entry)
	}
	index, err := archive.Create("manifest.json")
	if err != nil {
		return nil, err
	}
	if _, err = index.Write(canonicalReportJSON(manifest)); err != nil {
		return nil, err
	}
	if err = archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (r *ManagedDashboardRepository) FinishDashboardDelivery(ctx context.Context, j *DashboardDeliveryJob, w DashboardDeliveryWork, cards []DashboardDeliveredCard) error {
	if len(cards) != len(w.proof.Cards) {
		return ErrManagedConflict
	}
	for i, c := range cards {
		if c.Key != w.proof.Cards[i].Key || w.basis.Cards[i].Required && c.Snapshot == nil {
			return ErrManagedConflict
		}
		if c.Snapshot != nil && (c.Snapshot.WorkspaceID != WorkspaceID(ctx) || c.Snapshot.RequesterID != ActorID(ctx) ||
			c.Snapshot.ReportID != w.basis.Cards[i].Approval.ReportID || c.Snapshot.ManifestHash != w.proof.Cards[i].Manifest) {
			return ErrManagedConflict
		}
	}
	if err := validateDeliveryWatermarks(w.Refresh, cards); err != nil {
		return err
	}
	snapshot := DashboardDeliveryResult{DashboardID: w.plan.Dashboard, Publication: w.plan.Publication, Cards: cards}
	var raw []byte
	var err error
	if w.plan.Kind == "export" {
		raw, err = dashboardDeliveryFile(snapshot, w.basis.Input.Format, w.basis.Input.TileKey != "")
	} else {
		raw = canonicalReportJSON(snapshot)
	}
	if err != nil {
		return err
	}
	limit := 5 << 20
	if w.plan.Kind == "export" {
		limit = formalExportMaxBytes
	}
	if len(raw) == 0 || len(raw) > limit {
		return ErrManagedInput
	}
	sealed, err := r.reports.cipher.Encrypt(string(raw))
	if err != nil {
		return err
	}
	signed, err := r.reports.cipher.Encrypt(string(canonicalReportJSON(w.proof)))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = lockDashboardDelivery(ctx, tx, j.PlanID); err != nil {
		return err
	}
	current, err := r.dashboardDeliveryAccess(ctx, tx, j.ID, j.token)
	if err != nil {
		return err
	}
	if err = r.dashboardDeliveryProof(ctx, tx, &current, true); err != nil {
		return err
	}
	if reportDigest(current.proof) != reportDigest(w.proof) {
		return ErrManagedConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE sys_dashboard_deliveries SET status='succeeded',worker_token=NULL,lease_until=NULL,
		payload=$3,evidence=$4,sha256=$5,expires_at=statement_timestamp()+CASE WHEN $6='export' THEN interval '24 hours' ELSE interval '7 days' END
		WHERE workspace_id=dbhub_workspace() AND id=$1 AND user_id=dbhub_actor() AND status='running' AND worker_token=$2::uuid
		AND lease_until>statement_timestamp()`, j.ID, j.token, sealed, signed, hex.EncodeToString(digest[:]), w.plan.Kind)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrManagedConflict
	}
	if err = r.observeDashboardMetric(ctx, tx, j, w, cards); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT dbhub_notify_delivery(dbhub_workspace(),dbhub_actor(),$1,
		'仪表盘交付已完成','请登录后查看本人交付。','dashboard_delivery',$2)`, fmt.Sprintf("dashboard-delivery:%d", j.ID), j.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validateDeliveryWatermarks(refresh *DashboardRefresh, cards []DashboardDeliveredCard) error {
	if refresh.Quality.MaxWatermarkSkew <= 0 {
		return nil
	}
	first := map[string]time.Time{}
	last := map[string]time.Time{}
	for _, card := range cards {
		if card.Quality == nil {
			continue
		}
		for _, tile := range refresh.Tiles {
			group := tile.ComparisonGroup
			if tile.Key != card.Key || group == "" {
				continue
			}
			if card.Quality.DataAsOf == nil {
				return ErrDashboardUnknownFreshness
			}
			at := *card.Quality.DataAsOf
			if first[group].IsZero() || at.Before(first[group]) {
				first[group] = at
			}
			if at.After(last[group]) {
				last[group] = at
			}
			if last[group].Sub(first[group]) > time.Duration(refresh.Quality.MaxWatermarkSkew)*time.Second {
				return ErrManagedConflict
			}
		}
	}
	return nil
}

func (r *ManagedDashboardRepository) dashboardStoredProof(ctx context.Context, tx pgx.Tx, id int64) (DashboardDeliveryWork, error) {
	w, err := r.dashboardDeliveryAccess(ctx, tx, id, "")
	if err != nil {
		return w, err
	}
	if err = lockDashboardDelivery(ctx, tx, w.plan.ID); err != nil {
		return w, err
	}
	w, err = r.dashboardDeliveryAccess(ctx, tx, id, "")
	if err != nil {
		return w, err
	}
	if err = r.dashboardDeliveryProof(ctx, tx, &w, true); err != nil {
		return w, err
	}
	var sealed string
	if err = tx.QueryRow(ctx, `SELECT evidence FROM sys_dashboard_deliveries WHERE id=$1 AND user_id=dbhub_actor()`, id).Scan(&sealed); err != nil {
		return w, managedError(err)
	}
	var proof dashboardDeliveryProof
	if err = r.reports.decodeDashboardDelivery(sealed, &proof); err != nil {
		return w, err
	}
	if reportDigest(proof) != reportDigest(w.proof) {
		return w, ErrManagedConflict
	}
	return w, nil
}

func (r *ManagedDashboardRepository) dashboardStoredOutput(ctx context.Context, tx pgx.Tx, id int64) (DashboardDeliveryOutput, error) {
	var out DashboardDeliveryOutput
	w, err := r.dashboardStoredProof(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.Kind = w.plan.Kind
	out.Format = w.basis.Input.Format
	out.DashboardID = w.plan.Dashboard
	out.Publication = w.plan.Publication
	out.TileKey = w.basis.Input.TileKey
	if out.Kind == "export" && w.basis.Input.TileKey == "" {
		out.Format = "zip"
	}
	err = tx.QueryRow(ctx, `SELECT sha256,expires_at FROM sys_dashboard_deliveries
		WHERE id=$1 AND user_id=dbhub_actor() AND expires_at>statement_timestamp() AND payload<>''`, id).Scan(&out.SHA256, &out.ExpiresAt)
	return out, managedError(err)
}

func (r *ManagedDashboardRepository) DashboardDelivery(ctx context.Context, id int64) (DashboardDeliveryOutput, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DashboardDeliveryOutput{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	out, err := r.dashboardStoredOutput(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if out.Kind != "export" {
		raw, err := r.openDashboardPayload(ctx, tx, id, out.SHA256)
		if err != nil {
			return out, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		out.Snapshot = &DashboardDeliveryResult{}
		if err = decoder.Decode(out.Snapshot); err != nil {
			return DashboardDeliveryOutput{}, err
		}
	}
	return out, tx.Commit(ctx)
}

func (r *ManagedDashboardRepository) openDashboardPayload(ctx context.Context, tx pgx.Tx, id int64, hash string) ([]byte, error) {
	var sealed string
	err := tx.QueryRow(ctx, `SELECT payload FROM sys_dashboard_deliveries WHERE id=$1 AND sha256=$2 AND user_id=dbhub_actor()
		AND expires_at>statement_timestamp() AND payload<>''`, id, hash).Scan(&sealed)
	if err != nil {
		return nil, managedError(err)
	}
	plain, err := r.reports.cipher.Decrypt(sealed)
	if err != nil {
		return nil, err
	}
	raw := []byte(plain)
	digest := sha256.Sum256(raw)
	if len(raw) > formalExportMaxBytes || hex.EncodeToString(digest[:]) != hash {
		return nil, ErrManagedConflict
	}
	return raw, nil
}

func (r *ManagedDashboardRepository) dashboardCredentialSession(ctx context.Context) (string, error) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c == nil || c.SessionID == "" || MemberEpoch(ctx) < 1 {
		return "", ErrManagedDenied
	}
	return r.reports.cipher.Fingerprint([]byte("dashboard-delivery-session:" + c.SessionID)), nil
}

func (r *ManagedDashboardRepository) IssueDashboardFile(ctx context.Context, id int64) (FormalExportCredential, error) {
	var result FormalExportCredential
	session, err := r.dashboardCredentialSession(ctx)
	if err != nil {
		return result, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	out, err := r.dashboardStoredOutput(ctx, tx, id)
	if err != nil {
		return result, err
	}
	if out.Kind != "export" {
		return result, ErrManagedDenied
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return result, err
	}
	result.Credential = base64.RawURLEncoding.EncodeToString(secret[:])
	digest := r.reports.cipher.Fingerprint([]byte(result.Credential))
	err = tx.QueryRow(ctx, `INSERT INTO sys_dashboard_delivery_credentials(digest,workspace_id,delivery_id,user_id,member_version,session_digest,file_sha256,expires_at)
		SELECT $1,workspace_id,id,user_id,member_version,$3,sha256,LEAST(expires_at,statement_timestamp()+interval '10 minutes')
		FROM sys_dashboard_deliveries WHERE id=$2 AND user_id=dbhub_actor() AND expires_at>statement_timestamp()
		RETURNING expires_at`, digest, id, session).Scan(&result.ExpiresAt)
	if err != nil {
		return FormalExportCredential{}, managedError(err)
	}
	return result, tx.Commit(ctx)
}

func (r *ManagedDashboardRepository) RedeemDashboardFile(ctx context.Context, id int64, token string) (DashboardDeliveryOutput, error) {
	var empty DashboardDeliveryOutput
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return empty, ErrManagedInput
	}
	session, err := r.dashboardCredentialSession(ctx)
	if err != nil {
		return empty, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	out, err := r.dashboardStoredOutput(ctx, tx, id)
	if err != nil {
		return empty, err
	}
	if out.Kind != "export" {
		return empty, ErrManagedDenied
	}
	digest := r.reports.cipher.Fingerprint([]byte(token))
	var validUntil time.Time
	err = tx.QueryRow(ctx, `UPDATE sys_dashboard_delivery_credentials SET redeemed_at=statement_timestamp()
		WHERE digest=$1 AND delivery_id=$2 AND user_id=dbhub_actor() AND session_digest=$3 AND file_sha256=$4
		AND expires_at>statement_timestamp() AND redeemed_at IS NULL RETURNING expires_at`, digest, id, session, out.SHA256).Scan(&validUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, ErrManagedDenied
	}
	if err != nil {
		return empty, err
	}
	out.File, err = r.openDashboardPayload(ctx, tx, id, out.SHA256)
	if err != nil {
		return empty, err
	}
	if !validUntil.After(time.Now()) {
		return empty, ErrManagedDenied
	}
	return out, tx.Commit(ctx)
}

func (r *ManagedDashboardRepository) observeDashboardMetric(ctx context.Context, tx pgx.Tx, j *DashboardDeliveryJob, w DashboardDeliveryWork, cards []DashboardDeliveredCard) error {
	if w.plan.Kind != "personal" || w.basis.Input.Alert == nil {
		return nil
	}
	for _, card := range cards {
		if card.Key != w.basis.Input.AlertTile || card.Snapshot == nil {
			continue
		}
		active, value, err := EvaluateMetricAlert(w.basis.Input.Alert, &card.Snapshot.Result)
		if err != nil {
			return err
		}
		var previous bool
		err = tx.QueryRow(ctx, `SELECT active FROM sys_dashboard_delivery_alerts WHERE plan_id=$1 ORDER BY id DESC LIMIT 1`, j.PlanID).Scan(&previous)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if previous == active {
			return nil
		}
		sealed, err := r.reports.cipher.Encrypt(value)
		if err != nil {
			return err
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO sys_dashboard_delivery_alerts(workspace_id,plan_id,delivery_id,user_id,active,encrypted_value)
			VALUES(dbhub_workspace(),$1,$2,dbhub_actor(),$3,$4) RETURNING id`, j.PlanID, j.ID, active, sealed).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT dbhub_notify(dbhub_workspace(),dbhub_actor(),$1,'metric_alert',
			CASE WHEN $2 THEN '仪表盘指标达到告警阈值' ELSE '仪表盘指标已恢复' END,
			'请登录核对原期次告警与处理记录。','dashboard_delivery_alert',$3)`, fmt.Sprintf("dashboard-metric:%d", j.ID), active, j.PlanID)
		return err
	}
	return nil
}

type DashboardMetricEvent struct {
	ID          int64      `json:"id"`
	Delivery    int64      `json:"delivery_id"`
	Active      bool       `json:"active"`
	Value       *string    `json:"observed_value,omitempty"`
	ObservedAt  time.Time  `json:"observed_at"`
	Disposition *string    `json:"disposition,omitempty"`
	DisposedAt  *time.Time `json:"disposed_at,omitempty"`
}

func (r *ManagedDashboardRepository) DashboardDeliveryAlerts(ctx context.Context, id int64) ([]DashboardMetricEvent, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,delivery_id,active,encrypted_value,observed_at,disposition,disposed_at
		FROM sys_dashboard_delivery_alerts WHERE plan_id=$1 AND user_id=dbhub_actor() ORDER BY id DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	type storedMetric struct {
		DashboardMetricEvent
		sealed string
	}
	items := []storedMetric{}
	for rows.Next() {
		var e storedMetric
		if err = rows.Scan(&e.ID, &e.Delivery, &e.Active, &e.sealed, &e.ObservedAt, &e.Disposition, &e.DisposedAt); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]DashboardMetricEvent, 0, len(items))
	for _, e := range items {
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		_, proofErr := r.dashboardStoredProof(ctx, tx, e.Delivery)
		if proofErr == nil {
			v, err := r.reports.cipher.Decrypt(e.sealed)
			if err != nil {
				_ = tx.Rollback(context.WithoutCancel(ctx))
				return nil, err
			}
			e.Value = &v
		}
		_ = tx.Rollback(context.WithoutCancel(ctx))
		if proofErr != nil && !errors.Is(proofErr, ErrNotFound) && !errors.Is(proofErr, ErrManagedConflict) && !errors.Is(proofErr, ErrManagedDenied) {
			return nil, proofErr
		}
		if proofErr == nil {
			out = append(out, e.DashboardMetricEvent)
		}
	}
	return out, nil
}

func (r *ManagedDashboardRepository) DisposeDashboardAlert(ctx context.Context, key string, plan, event int64, note string) (ReportCommandResult, error) {
	if strings.TrimSpace(note) == "" || len([]rune(note)) > 500 {
		return ReportCommandResult{}, ErrManagedInput
	}
	return r.reports.command(ctx, key, "dashboard-metric-disposition", []any{plan, event, note}, false, false, func(tx pgx.Tx) (ReportCommandResult, error) {
		out := ReportCommandResult{ID: event}
		var delivery int64
		err := tx.QueryRow(ctx, `SELECT delivery_id FROM sys_dashboard_delivery_alerts WHERE id=$1 AND plan_id=$2 AND user_id=dbhub_actor()`, event, plan).Scan(&delivery)
		if err != nil {
			return out, managedError(err)
		}
		if _, err = r.dashboardStoredProof(ctx, tx, delivery); err != nil {
			return out, err
		}
		tag, err := tx.Exec(ctx, `UPDATE sys_dashboard_delivery_alerts SET disposition=$3,disposed_at=statement_timestamp()
			WHERE id=$1 AND plan_id=$2 AND user_id=dbhub_actor() AND disposition IS NULL`, event, plan, note)
		if err != nil {
			return out, err
		}
		if tag.RowsAffected() != 1 {
			return out, ErrManagedConflict
		}
		return out, nil
	})
}
