package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func TestReportWebhookSignatureRetryAndOptOut(t *testing.T) {
	const secret = "isolated-webhook-test-signing-key-over-32-bytes"
	var mu sync.Mutex
	calls := 0
	processed := map[string]bool{}
	invalid := false
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(r.Header.Get("X-DBHub-Timestamp") + "."))
		mac.Write(body)
		var payload map[string]any
		json.Unmarshal(body, &payload)
		mu.Lock()
		defer mu.Unlock()
		calls++
		if r.Header.Get("X-DBHub-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) || payload["event_id"] != r.Header.Get("X-DBHub-Event-Id") || len(payload) != 5 || !strings.HasPrefix(fmt.Sprint(payload["resource_path"]), "/reports?view=subscriptions&") {
			invalid = true
			w.WriteHeader(400)
			return
		}
		for _, forbidden := range []string{"rows", "params", "password", "access_token", "metric_value", "connection_id"} {
			if strings.Contains(string(body), forbidden) {
				invalid = true
				w.WriteHeader(400)
				return
			}
		}
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		processed[fmt.Sprint(payload["event_id"])] = true
		w.WriteHeader(202)
	}))
	defer receiver.Close()
	a := setupConfiguredAPI(t, "", func(cfg *config.Config, _ string) {
		cfg.NotificationWebhook = config.NotificationWebhookConfig{URL: receiver.URL, Secret: secret}
	})
	f := setupManagedAPI(t, a)
	report := subscriptionMetricReport(t, f)
	ctx := context.Background()
	service := NewAutomation(&config.Config{NotificationWebhook: config.NotificationWebhookConfig{URL: receiver.URL, Secret: secret}}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	definition := map[string]any{"name": "本地外部接口验收", "report_id": report, "publication_no": 1, "interval_minutes": 5, "params": map[string]any{}, "enabled": true, "webhook": true, "alert_rule": map[string]any{"column": "metric_value", "operator": "gt", "threshold": "0"}}
	created := a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(), "definition": definition}, 200)
	id := int64(created["id"].(float64))
	if _, err := service.Subscriptions.GenerateDue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunSubscription(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := a.pool.Exec(ctx, `UPDATE sys_webhook_deliveries SET next_attempt_at=now() WHERE status='pending'`); err != nil {
			t.Fatal(err)
		}
		if err := service.RunWebhook(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if invalid || calls != 3 || len(processed) != 2 {
		t.Fatal("signed local receiver did not process two metadata events after one retry")
	}
	mu.Unlock()
	var eventID string
	if err := a.pool.QueryRow(ctx, `SELECT id::text FROM sys_webhook_deliveries ORDER BY id LIMIT 1`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	// Inject the worker-crash boundary after remote acceptance. Recovery must
	// retain the same event ID; the receiver handles its duplicate only once.
	if _, err := a.pool.Exec(ctx, `UPDATE sys_webhook_deliveries SET status='sending',attempts=1,lease_until=now()-interval '1 second' WHERE id=$1::uuid`, eventID); err != nil {
		t.Fatal(err)
	}
	if err := service.RunWebhook(ctx); !errors.Is(err, db.ErrNotFound) {
		t.Fatal("expired delivery was not deferred with a bounded retry")
	}
	var status string
	if err := a.pool.QueryRow(ctx, `SELECT status FROM sys_webhook_deliveries WHERE id=$1::uuid`, eventID).Scan(&status); err != nil || status != "pending" {
		t.Fatal("lease recovery was rolled back when no immediate work existed")
	}
	if _, err := a.pool.Exec(ctx, `UPDATE sys_webhook_deliveries SET next_attempt_at=now() WHERE id=$1::uuid`, eventID); err != nil {
		t.Fatal(err)
	}
	if err := service.RunWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if invalid || calls != 4 || len(processed) != 2 {
		t.Fatal("recovered delivery changed event identity or duplicate processing")
	}
	mu.Unlock()
	a.call("POST", fmt.Sprintf("/report-subscriptions/%d/runs", id), a.readonly, map[string]any{"request_id": testRequestID(), "version": 1}, 200)
	if _, err := service.RunSubscription(ctx); err != nil {
		t.Fatal(err)
	}
	definition["webhook"] = false
	a.call("PUT", fmt.Sprintf("/report-subscriptions/%d", id), a.readonly, map[string]any{"request_id": testRequestID(), "version": 1, "definition": definition}, 200)
	if err := service.RunWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if calls != 4 {
		t.Fatal("opting out did not stop a queued external delivery")
	}
	mu.Unlock()
	var failed int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_webhook_deliveries WHERE status='failed'`).Scan(&failed); err != nil || failed != 1 {
		t.Fatal("opted-out event was not durably rejected")
	}
}
