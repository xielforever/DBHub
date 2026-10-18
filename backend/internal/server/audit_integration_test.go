package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func latestAudit(t *testing.T, a *apiTest, action string) db.AuditLog {
	t.Helper()
	rows, _, err := a.deps.Audit.List(a.actor(), db.AuditFilter{Action: action}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	platform, _, e := a.deps.Audit.List(db.WithPlatformAudit(a.actor()), db.AuditFilter{Action: action}, 100, 0)
	if e != nil {
		t.Fatal(e)
	}
	rows = append(rows, platform...)
	if len(rows) == 0 {
		t.Fatalf("missing %s audit", action)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].ID > rows[0].ID {
			rows[0] = rows[i]
		}
	}

	return rows[0]
}
func expectAudit(t *testing.T, a *apiTest, action, outcome string) db.AuditLog {
	t.Helper()
	row := latestAudit(t, a, action)
	if row.Context.Outcome != outcome || row.EventID == "" {
		t.Fatalf("%s: outcome %s, want %s", action, row.Context.Outcome, outcome)
	}
	return row
}

func TestAuditOperationCoverage(t *testing.T) {
	a := setupAPI(t)
	t.Run("transaction-commit-rollback-and-errors", func(t *testing.T) {
		a.t = t
		begin := func() string {
			data := a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": a.connID}, 200)
			return data["transaction_id"].(string)
		}
		tx := begin()
		row := expectAudit(t, a, "TRANSACTION_BEGIN", "begun")
		if row.Context.TransactionID != tx || row.Context.ConnectionID != a.connID || !row.Context.TargetOpened || row.Context.ActorUsername != "admin" || row.Context.DatabaseAccount == "" {
			t.Fatal("transaction identity missing")
		}
		a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": a.connID}, 409)
		expectAudit(t, a, "TRANSACTION_BEGIN", "rejected")
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": tx, "sql": "INSERT INTO " + a.table + " VALUES(3,1,'commit@example.com','test')"}, 200)
		a.call("POST", "/query/transaction/commit", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": tx}, 200)
		row = expectAudit(t, a, "TRANSACTION_COMMIT", "committed")
		if row.Context.TransactionID != tx || row.Context.DatabaseAccount == "" {
			t.Fatal("commit session identity missing")
		}
		a.call("POST", "/query/transaction/commit", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": tx}, 404)
		expectAudit(t, a, "TRANSACTION_COMMIT", "rejected")
		tx = begin()
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": tx, "sql": "INSERT INTO " + a.table + " VALUES(4,1,'rollback@example.com','test')"}, 200)
		a.call("POST", "/query/transaction/rollback", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": tx}, 200)
		expectAudit(t, a, "TRANSACTION_ROLLBACK", "rolled_back")
		res := a.exec(a.admin, "SELECT COUNT(*) FROM "+a.table, 200)
		if res["rows"].([]any)[0].([]any)[0] != float64(3) {
			t.Fatal("audit changed transaction semantics")
		}
		tx = begin()
		pidResult := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": tx, "sql": "SELECT pg_backend_pid()"}, 200)
		pid := int(pidResult["rows"].([]any)[0].([]any)[0].(float64))
		if _, err := a.pool.Exec(a.actor(), "SELECT pg_terminate_backend($1)", pid); err != nil {
			t.Fatal(err)
		}
		a.call("POST", "/query/transaction/commit", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": tx}, 400)
		row = expectAudit(t, a, "TRANSACTION_COMMIT", "unknown")
		if row.Context.TransactionID != tx || row.Status != 0 {
			t.Fatal("uncertain commit recorded as success")
		}
	})
	t.Run("cancel-ownership-and-finished-task", func(t *testing.T) {
		a.t = t
		payload, _ := json.Marshal(map[string]any{"connection_id": a.connID, "request_id": testRequestID(), "sql": "SELECT pg_sleep(10) /* audit-cancel */"})
		req, _ := http.NewRequest("POST", a.server.URL+"/api/v1/query/execute", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+a.admin)
		req.Header.Set("Content-Type", "application/json")
		finished := make(chan error, 1)
		go func() {
			res, err := a.server.Client().Do(req)
			if err == nil {
				_, err = io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}
			finished <- err
		}()
		id := ""
		deadline := time.Now().Add(5 * time.Second)
		for id == "" && time.Now().Before(deadline) {
			items := a.call("GET", "/query/jobs", a.admin, nil, 200)["items"].([]any)
			for _, raw := range items {
				item := raw.(map[string]any)
				if strings.Contains(item["sql"].(string), "audit-cancel") && item["status"] == "running" {
					id = item["id"].(string)
				}
			}
			if id == "" {
				time.Sleep(20 * time.Millisecond)
			}
		}
		if id == "" {
			t.Fatal("query did not enter running state")
		}
		path := "/query/jobs/" + id + "/cancel"
		a.call("POST", path, a.developer, nil, 404)
		expectAudit(t, a, "QUERY_CANCEL", "rejected")
		a.call("POST", path, a.admin, nil, 200)
		row := expectAudit(t, a, "QUERY_CANCEL", "cancel_requested")
		if row.Context.JobID != id || row.Context.ConnectionID != a.connID || row.Context.DatabaseAccount == "" {
			t.Fatal("cancel target identity missing")
		}
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("query was not cancelled")
		}
		a.call("POST", path, a.admin, nil, 200)
		expectAudit(t, a, "QUERY_CANCEL", "already_finished")
	})
	t.Run("export-approval-download-and-denials", func(t *testing.T) {
		a.t = t
		reviewer := securityReviewer(a)
		export := a.call("POST", "/query/exports", a.admin, map[string]any{"connection_id": a.connID, "sql": "SELECT id FROM " + a.table, "format": "json", "reason": "audit verification"}, 200)
		id := int64(export["id"].(float64))
		path := fmt.Sprintf("/query/exports/%d", id)
		expectAudit(t, a, "EXPORT_CREATE", "export_pending")
		a.call("GET", path+"/download", a.admin, nil, 404)
		expectAudit(t, a, "EXPORT_DOWNLOAD", "rejected")
		a.call("POST", path+"/review", a.developer, map[string]any{"status": "approved"}, 403)
		row := expectAudit(t, a, "EXPORT_REVIEW", "rejected")
		if row.Context.AuthFailure != "insufficient_role" {
			t.Fatal("role rejection escaped audit")
		}
		a.call("POST", path+"/review", a.admin, map[string]any{"status": "approved"}, 409)
		expectAudit(t, a, "EXPORT_REVIEW", "rejected")
		a.call("POST", path+"/review", reviewer, map[string]any{"status": "approved"}, 200)
		expectAudit(t, a, "EXPORT_REVIEW", "export_approved")
		a.call("GET", path+"/download", a.admin, nil, 200)
		row = expectAudit(t, a, "EXPORT_DOWNLOAD", "download_response")
		if row.Context.ExportID != id || row.Context.ConnectionID != a.connID || row.Context.Route != "GET /api/v1/query/exports/{id}/download" {
			t.Fatal("download target missing")
		}
		blockedCtx, blockedSnapshot := auditctx.Begin(a.actor())
		blockedCtx = auditctx.WithCheckpoint(blockedCtx, func() error { return errors.New("audit storage unavailable") })
		blockedRequest := httptest.NewRequest("GET", "/api/v1"+path+"/download", nil).WithContext(blockedCtx)
		blockedRequest.SetPathValue("id", fmt.Sprint(id))
		blockedResponse := httptest.NewRecorder()
		a.deps.Queries.DownloadExport(blockedResponse, blockedRequest)
		if blockedResponse.Code != 503 || strings.Contains(blockedResponse.Body.String(), "\"result\"") || blockedSnapshot.Read().Outcome != "rejected" {
			t.Fatal("failed audit checkpoint served or claimed download")
		}
		a.call("GET", path+"/download", a.developer, nil, 404)
		expectAudit(t, a, "EXPORT_DOWNLOAD", "rejected")
	})
	t.Run("authentication-and-read-route-denials", func(t *testing.T) {
		a.t = t
		for _, tc := range []struct {
			token, reason string
			status        int
		}{{"", "missing_token", 401}, {"malformed-secret-token", "invalid_token", 401}, {a.readonly, "insufficient_role", 403}} {
			a.call("GET", "/users", tc.token, nil, tc.status)
			row := expectAudit(t, a, "AUTH_DENIED", "rejected")
			if row.Context.AuthFailure != tc.reason || row.Context.HTTPStatus != tc.status {
				t.Fatalf("missing denial %s", tc.reason)
			}
			if tc.token == a.readonly && row.Context.ActorUsername != "readonly" {
				t.Fatal("authenticated denial actor lost")
			}
		}
		user, err := a.deps.UserRepo.FindByUsername(a.actor(), "readonly")
		if err != nil {
			t.Fatal(err)
		}
		expired, err := auth.IssuePair([]byte("test-main-key-32-bytes-long-123456789"), user, -time.Hour, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		a.call("GET", "/connections", expired.AccessToken, nil, 401)
		if latestAudit(t, a, "AUTH_DENIED").Context.AuthFailure != "invalid_token" {
			t.Fatal("expired token not audited")
		}
		a.call("GET", "/audit-logs/delivery", a.readonly, nil, 403)
		if latestAudit(t, a, "AUTH_DENIED").Context.AuthFailure != "insufficient_role" {
			t.Fatal("delivery diagnostics exposed")
		}
		a.call("POST", "/auth/logout", a.readonly, nil, 200)
		expectAudit(t, a, "LOGOUT", "completed")
		a.call("GET", "/connections", a.readonly, nil, 401)
		row := latestAudit(t, a, "AUTH_DENIED")
		if row.Context.AuthFailure != "invalid_session" || row.Context.ActorUsername != "readonly" {
			t.Fatal("revoked session not correlated")
		}
		rows, _, err := a.deps.Audit.List(a.actor(), db.AuditFilter{}, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(rows)
		for _, secret := range []string{"TestPassword123!", "malformed-secret-token", "SELECT id FROM", "commit@example.com"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("audit leaked body or token")
			}
		}
	})
}

func TestAuditAuthenticationProviderUnavailable(t *testing.T) {
	a := setupAPI(t)
	brokenRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer brokenRedis.Close()
	otherDeps := *a.deps
	otherDeps.Sessions = auth.NewSessionStore(brokenRedis)
	cfg := &config.Config{Env: "development", SecretKey: []byte("test-main-key-32-bytes-long-123456789"), AccessTTL: time.Hour, RefreshTTL: time.Hour}
	other := httptest.NewServer(NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps.UserRepo, &otherDeps))
	defer other.Close()
	defer otherDeps.Queries.Close()
	req, _ := http.NewRequest("GET", other.URL+"/api/v1/connections", nil)
	req.Header.Set("Authorization", "Bearer "+a.admin)
	res, err := other.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("session outage returned %d", res.StatusCode)
	}
	row := expectAudit(t, a, "AUTH_DENIED", "rejected")
	if row.Context.AuthFailure != "session_unavailable" || row.Context.ActorUsername != "admin" || row.Context.HTTPStatus != 503 {
		t.Fatal("provider failure escaped audit")
	}
}

func restartAudit(t *testing.T, a *apiTest, sink auditlog.Sink) {
	t.Helper()
	a.server.Close()
	a.deps.Queries.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = a.deps.AuditJournal.Close(ctx)
	var err error
	a.deps.AuditJournal, err = auditlog.Open(a.auditDir, 10000, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Env: "development", SecretKey: []byte("test-main-key-32-bytes-long-123456789"), AccessTTL: time.Hour, RefreshTTL: time.Hour}
	a.server = httptest.NewServer(NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps.UserRepo, a.deps))
}

func TestAuditDatabaseFailureAndRestart(t *testing.T) {
	a := setupAPI(t)
	ctx := a.actor()
	if _, err := a.pool.Exec(ctx, "ALTER TABLE sys_audit_logs RENAME TO audit_temporarily_unavailable"); err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			_, _ = a.pool.Exec(ctx, "ALTER TABLE audit_temporarily_unavailable RENAME TO sys_audit_logs")
		}
	})
	id := testRequestID()
	a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "request_id": id, "sql": "INSERT INTO " + a.table + " VALUES(9,1,'queued@example.com','test')"}, 200)
	if a.deps.AuditJournal.Status().Pending != 1 {
		t.Fatal("failed audit insert was not retained")
	}
	status := a.call("GET", "/audit-logs/delivery", a.admin, nil, 200)
	if status["pending"] != float64(1) || status["last_error"] != "database_write_failed" {
		t.Fatal("queue failure not visible")
	}
	// A queued snapshot must also survive deletion of its connection.
	if _, err := a.pool.Exec(ctx, "DELETE FROM sys_connections WHERE id=$1", a.connID); err != nil {
		t.Fatal(err)
	}
	malformed := &db.AuditLog{Action: "TEST", ResourceType: "CONNECTION", Context: auditctx.Data{ActorUsername: "admin", DatabaseAccount: "invalid\x00account", Outcome: "rejected"}}
	ticket, err := a.deps.AuditJournal.Begin(malformed)
	if err != nil {
		t.Fatal(err)
	}
	if err = ticket.Finish(malformed); err != nil {
		t.Fatal(err)
	}
	restartAudit(t, a, a.deps.Audit)
	if a.deps.AuditJournal.Status().Pending != 2 {
		t.Fatal("restart lost pending event")
	}
	if _, err := a.pool.Exec(ctx, "ALTER TABLE audit_temporarily_unavailable RENAME TO sys_audit_logs"); err != nil {
		t.Fatal(err)
	}
	restored = true
	if err := a.deps.AuditJournal.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	row := expectAudit(t, a, "QUERY", "succeeded")
	if row.Context.ExecutionRequestID != id || row.Context.ActorUsername != "admin" || row.Context.ConnectionID != a.connID || row.Context.DatabaseAccount == "" {
		t.Fatal("replayed result lost its original snapshot")
	}
	var count int
	if err := a.pool.QueryRow(ctx, "SELECT COUNT(*) FROM public."+a.table+" WHERE id=9").Scan(&count); err != nil || count != 1 {
		t.Fatal("audit replay re-executed business SQL")
	}
	if a.deps.AuditJournal.Status().Pending != 0 {
		t.Fatal("recovered event not acknowledged")
	}
	bad := expectAudit(t, a, "TEST", "rejected")
	if strings.ContainsRune(bad.Context.DatabaseAccount, 0) || !strings.Contains(bad.Context.DatabaseAccount, "\\x00") {
		t.Fatal("invalid metadata poisoned delivery or lost its escaped identity")
	}
}

type lostAuditACK struct{ sink *db.AuditRepository }

func (s lostAuditACK) Create(ctx context.Context, a *db.AuditLog) error {
	if err := s.sink.Create(ctx, a); err != nil {
		return err
	}
	return errors.New("simulated lost acknowledgement")
}
func TestAuditLostAcknowledgementIsIdempotent(t *testing.T) {
	a := setupAPI(t)
	restartAudit(t, a, lostAuditACK{sink: a.deps.Audit})
	id := testRequestID()
	a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "request_id": id, "sql": "SELECT 1"}, 200)
	if a.deps.AuditJournal.Status().Pending != 1 {
		t.Fatal("lost acknowledgement not retained")
	}
	first := expectAudit(t, a, "QUERY", "succeeded")
	restartAudit(t, a, a.deps.Audit)
	if err := a.deps.AuditJournal.Flush(a.actor()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.pool.QueryRow(a.actor(), "SELECT COUNT(*) FROM sys_audit_logs WHERE event_id=$1::uuid", first.EventID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate audit after uncertain ACK")
	}
	row := expectAudit(t, a, "QUERY", "succeeded")
	if row.ID != first.ID || row.EventID != first.EventID || !row.CreatedAt.Equal(first.CreatedAt) || row.Context.ExecutionRequestID != id {
		t.Fatal("event identity changed after replay")
	}
}
