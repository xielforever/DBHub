package server

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/reportexport"
	"github.com/user/dbhub/pkg/crypto"
)

func verifyFormalReportExport(t *testing.T, a *apiTest, report int64, reviewer string, key []byte) {
	t.Helper()
	ctx := context.Background()
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	cipher, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(format string) (int64, map[string]any) {
		t.Helper()
		record := a.call("POST", fmt.Sprintf("/published-reports/%d/exports", report), a.readonly,
			map[string]any{"request_id": testRequestID(), "publication_no": 1, "format": format, "reason": "隔离导出验收",
				"params": map[string]any{"category": "blue"}}, 200)
		return int64(record["id"].(float64)), record
	}
	path := func(id int64, suffix string) string {
		return fmt.Sprintf("/report-exports/%d%s", id, suffix)
	}
	review := func(id int64, decision, token string, want int) map[string]any {
		t.Helper()
		return a.call("POST", path(id, "/review"), token,
			map[string]any{"version": 1, "decision": decision, "comment": "独立导出复核"}, want)
	}
	checkQueue := func(id int64, wantStatus string, wantFile bool) {
		t.Helper()
		var state string
		var file bool
		err := a.pool.QueryRow(ctx, `SELECT COALESCE((SELECT status FROM sys_report_export_jobs WHERE export_id=$1),''),
			EXISTS(SELECT 1 FROM sys_report_export_files WHERE export_id=$1)`, id).Scan(&state, &file)
		if err != nil || state != wantStatus || file != wantFile {
			t.Fatalf("export %d: job=%q file=%v err=%v", id, state, file, err)
		}
	}
	legacy, record := submit("csv")
	if record["generation_status"] != nil || review(legacy, "approved", reviewer, 200)["can_download"] != false {
		t.Fatal("unregistered legacy export changed mode or reviewer gained file access")
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_formal_exports VALUES(1,$1)`, report); err != nil {
		t.Fatal(err)
	}
	legacyRead := a.call("GET", path(legacy, ""), a.readonly, nil, 200)
	if legacyRead["can_download"] != true || legacyRead["expires_at"] != record["expires_at"] {
		t.Fatal("registration changed an existing legacy export")
	}
	checkQueue(legacy, "", false)

	for _, format := range []string{"csv", "json", "xlsx"} {
		id, record := submit(format)
		if record["generation_status"] != "awaiting_approval" || record["can_download"] != false {
			t.Fatal("new request advertised a file before approval")
		}
		checkQueue(id, "", false)
		preview := a.call("GET", path(id, "/preview"), reviewer, nil, 200)["result"].(map[string]any)["rows"].([]any)
		previewRow := preview[0].([]any)
		if len(preview) != 1 || previewRow[1] != "blue" || previewRow[2] != nil {
			t.Fatal("original review preview did not use frozen parameters and masking")
		}
		review(id, "approved", a.readonly, 403)
		if review(id, "approved", reviewer, 200)["generation_status"] != "queued" {
			t.Fatal("approved request did not expose a queued job")
		}
		review(id, "approved", reviewer, 409)
		checkQueue(id, "queued", false)
		if _, err := a.targetPool.Exec(ctx, `UPDATE `+pgx.Identifier{"public", a.table}.Sanitize()+` SET id=id+30 WHERE owner_id=1`); err != nil {
			t.Fatal(err)
		}
		job, err := service.RunFormalExport(ctx)
		if err != nil || job == nil || job.ExportID != id {
			t.Fatalf("formal export job did not finish: %v", err)
		}
		checkQueue(id, "succeeded", true)
		var encrypted, digest string
		var rows, size, receiptCount int
		var separate, window bool
		err = a.pool.QueryRow(ctx, `SELECT f.encrypted_file,f.sha256,f.row_count,f.size_bytes,
			e.encrypted_payload='' AND e.generation_preview<>'' AND e.generation_request<>'' AND e.snapshot_hash<>f.snapshot_sha256,
			f.expires_at=f.generated_at+interval '24 hours',
			(SELECT count(*) FROM sys_query_executions q WHERE q.request_id=$2::uuid AND q.user_id=e.user_id AND q.status='succeeded')
			FROM sys_report_export_files f JOIN sys_report_exports e ON e.id=f.export_id WHERE f.export_id=$1 AND f.format=$3`,
			id, job.ID, format).Scan(&encrypted, &digest, &rows, &size, &separate, &window, &receiptCount)
		if err != nil || rows != 1 || !separate || !window || receiptCount != 1 {
			t.Fatalf("file metadata was not sealed separately with a new receipt: %v", err)
		}
		raw, err := cipher.Decrypt(encrypted)
		hash := sha256.Sum256([]byte(raw))
		if err != nil || size != len(raw) || digest != hex.EncodeToString(hash[:]) {
			t.Fatalf("generated file failed integrity check: %v", err)
		}
		verifyGeneratedExportBytes(t, format, raw, int(previewRow[0].(float64))+30)
		old := a.call("GET", path(id, ""), a.readonly, nil, 200)
		if old["generation_status"] != "succeeded" || old["can_download"] != false {
			t.Fatal("generation changed the legacy download boundary")
		}
		a.call("GET", path(id, "/download"), a.readonly, nil, 403)
		a.call("GET", path(id, "/download"), reviewer, nil, 403)
	}

	id, _ := submit("csv")
	review(id, "rejected", reviewer, 200)
	checkQueue(id, "", false)
	id, _ = submit("csv")
	review(id, "approved", reviewer, 200)
	if _, err = a.pool.Exec(ctx, `INSERT INTO sys_query_settings(workspace_id,user_id,timeout_ms,max_rows,block_unsafe_write)
		VALUES(1,3,30000,1,true) ON CONFLICT(workspace_id,user_id) DO UPDATE SET max_rows=EXCLUDED.max_rows`); err != nil {
		t.Fatal(err)
	}
	if job, err := service.RunFormalExport(ctx); !errors.Is(err, db.ErrManagedConflict) || job == nil || job.ExportID != id {
		t.Fatalf("queued export accepted changed row ceiling: %v", err)
	}
	checkQueue(id, "failed", false)
	if _, err = a.pool.Exec(ctx, `UPDATE sys_query_settings SET max_rows=1000 WHERE workspace_id=1 AND user_id=3`); err != nil {
		t.Fatal(err)
	}
	id, _ = submit("csv")
	review(id, "approved", reviewer, 200)
	claimed, err := a.deps.Conns.ManagedReports().ClaimFormalExport(ctx)
	if err != nil || claimed == nil || claimed.ExportID != id {
		t.Fatalf("expected a leased formal export: %v", err)
	}
	if _, err = a.pool.Exec(ctx, `UPDATE sys_report_export_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if job, err := service.RunFormalExport(ctx); !errors.Is(err, db.ErrNotFound) || job != nil {
		t.Fatalf("expired lease retried the export: %v", err)
	}
	checkQueue(id, "interrupted", false)
	id, _ = submit("csv")
	review(id, "approved", reviewer, 200)
	setCustodyViewerScope(a, "dash-a", 1, "dept-a", "dept-b")
	if job, err := service.RunFormalExport(ctx); !errors.Is(err, db.ErrManagedConflict) || job == nil || job.ExportID != id {
		t.Fatalf("queued export accepted changed scope: %v", err)
	}
	checkQueue(id, "failed", false)
	setCustodyViewerScope(a, "dash-a", 2, "dept-a")
	// The reviewer must still be able to see the same frozen preview boundary.
	id, _ = submit("csv")
	review(id, "approved", reviewer, 409)
	checkQueue(id, "", false)
	if _, err = a.pool.Exec(ctx, `DELETE FROM sys_report_review_checks c USING sys_managed_reports m,sys_report_releases r
		WHERE m.id=$1 AND r.id=m.active_release_id AND c.review_id=r.review_id`, report); err != nil {
		t.Fatal(err)
	}
	a.call("POST", fmt.Sprintf("/published-reports/%d/exports", report), a.readonly,
		map[string]any{"request_id": testRequestID(), "publication_no": 1, "format": "csv", "reason": "缺少阶段 3 审批证据"}, 403)
	t.Log("fresh masked CSV/JSON/XLSX files used independent approvals and one receipt each; changed scope, changed ceiling and expired lease produced no file")
}

func verifyGeneratedExportBytes(t *testing.T, format, raw string, quantity int) {
	t.Helper()
	switch format {
	case "csv":
		match := fmt.Sprintf("\r\n%d,blue,,", quantity)
		if !strings.Contains(raw, match) || strings.Count(raw, "\r\n") != 2 {
			t.Fatal("CSV did not contain exactly the fresh masked result")
		}
	case "json":
		var file struct {
			Snapshot reportexport.Snapshot `json:"snapshot"`
		}
		err := json.Unmarshal([]byte(raw), &file)
		rows := file.Snapshot.Result.Rows
		if err != nil || len(rows) != 1 || rows[0][0] != float64(quantity) || rows[0][1] != "blue" || rows[0][2] != nil {
			t.Fatalf("JSON did not contain exactly the fresh masked result: %v", err)
		}
	case "xlsx":
		archive, err := zip.NewReader(strings.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		sheet, err := archive.Open("xl/worksheets/sheet1.xml")
		if err != nil {
			t.Fatal(err)
		}
		defer sheet.Close()
		data, err := io.ReadAll(sheet)
		if err != nil || !strings.Contains(string(data), fmt.Sprintf("<v>%d</v>", quantity)) ||
			!strings.Contains(string(data), ">blue<") || strings.Contains(string(data), "other-category") {
			t.Fatalf("XLSX did not contain the fresh masked result: %v", err)
		}
	}
}
