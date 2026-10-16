package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/user/dbhub/internal/queryapi"
)

func TestQueryMetricsRealHTTP(t *testing.T) {
	a := setupAPI(t)
	client := &http.Client{Timeout: 5 * time.Second}
	read := func() queryapi.QueryMetricsSnapshot {
		t.Helper()
		res := executionRequest(client, a.server.URL, "GET", "/metrics/query", a.admin, nil)
		requireExecutionStatus(t, res, 200)
		var s queryapi.QueryMetricsSnapshot
		if err := json.Unmarshal(res.data, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	drain := func() queryapi.QueryMetricsSnapshot {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			s := read()
			if s.Requests.InFlight == 0 && s.Queue.Running == 0 && s.Queue.Waiting == 0 {
				return s
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("execution metrics did not drain")
		return queryapi.QueryMetricsSnapshot{}
	}
	for _, format := range []string{"", "?format=prometheus"} {
		for _, token := range []string{a.developer, a.readonly} {
			requireExecutionStatus(t, executionRequest(client, a.server.URL, "GET", "/metrics/query"+format, token, nil), 403)
		}
		requireExecutionStatus(t, executionRequest(client, a.server.URL, "GET", "/metrics/query"+format, "", nil), 401)
	}
	requireExecutionStatus(t, executionRequest(client, a.server.URL, "GET", "/metrics/query?format=unsupported", a.admin, nil), 400)
	before := drain()
	id := testRequestID()
	body := map[string]any{"request_id": id, "connection_id": a.connID, "sql": "SELECT 1 AS telemetry_value"}
	a.call("POST", "/query/execute", a.admin, body, 200)
	a.call("POST", "/query/execute", a.admin, body, 409)
	a.call("POST", "/query/execute", a.admin, map[string]any{"request_id": id, "connection_id": a.connID, "sql": "SELECT 2"}, 409)
	a.exec(a.admin, "SELECT FROM", 400)
	a.exec(a.admin, "SELECT nonexistent_metrics_column FROM "+a.table, 400)
	a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "sql": "SELECT pg_sleep(3)", "timeout_ms": 1000}, 400)
	after := drain()
	for outcome, delta := range map[string]uint64{"succeeded": 1, "duplicate": 1, "conflict": 1, "rejected": 1, "failed": 1, "unknown": 1} {
		if after.Requests.Completed[outcome].Count-before.Requests.Completed[outcome].Count != delta {
			t.Fatalf("wrong request outcome %s", outcome)
		}
	}
	if after.Requests.Total-before.Requests.Total != 6 || after.Executions.Total-before.Executions.Total != 3 {
		t.Fatal("duplicates and validation rejections were counted as SQL executions")
	}
	if after.Counters.Timeouts-before.Counters.Timeouts != 1 || after.Counters.Canceled != before.Counters.Canceled {
		t.Fatal("timeout was not distinguished from cancellation")
	}
	if after.Receipts == nil || !after.ReceiptStoreAvailable || after.Receipts.Unknown != 1 {
		t.Fatal("durable unknown receipt was not counted")
	}
	if after.Process.GOMAXPROCS < 1 || after.Process.RSSBytes == nil || after.Process.CPUSeconds == nil {
		t.Fatal("Linux process metrics unavailable")
	}
	raw, _ := json.Marshal(after)
	for _, secret := range []string{a.table, id, "nonexistent_metrics_column", "SELECT pg_sleep"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("execution identifiers or SQL leaked into metrics")
		}
	}

	// 真实 HTTP 取消应产生取消原因，不能混入超时计数。
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan executionResponse, 1)
	go func() {
		done <- executionRequestContext(cancelCtx, client, a.server.URL, "POST", "/query/execute", a.admin, map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": "SELECT pg_sleep(10)"})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for read().Executions.InFlight == 0 {
		if time.Now().After(deadline) {
			t.Fatal("cancel probe did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP cancel did not return")
	}
	canceled := drain()
	if canceled.Counters.Canceled-after.Counters.Canceled != 1 || canceled.Counters.Timeouts != after.Counters.Timeouts {
		t.Fatal("cancellation reason was lost")
	}

	// 只保留聚合状态；过期 running 展示为 unknown，24 小时之外不纳入该窗口。
	ctx := context.Background()
	for _, item := range []struct{ status, created, updated string }{
		{"queued", "NOW()", "NOW()"}, {"running", "NOW()", "NOW()"},
		{"running", "NOW()", "NOW()-INTERVAL '10 minutes'"}, {"unknown", "NOW()-INTERVAL '25 hours'", "NOW()-INTERVAL '25 hours'"},
	} {
		query := fmt.Sprintf("INSERT INTO sys_query_executions(user_id,request_id,request_hash,connection_id,status,created_at,updated_at) VALUES(1,$1,$2,$3,$4,%s,%s)", item.created, item.updated)
		if _, err := a.pool.Exec(ctx, query, testRequestID(), strings.Repeat("0", 64), a.connID, item.status); err != nil {
			t.Fatal(err)
		}
	}
	counts := read()
	if counts.Receipts.Queued != 1 || counts.Receipts.Running != 1 || counts.Receipts.Stale != 1 || counts.Receipts.Unknown != 3 {
		t.Fatalf("incorrect durable window: %+v", counts.Receipts)
	}
	getPrometheus := func() string {
		t.Helper()
		req, _ := http.NewRequest("GET", a.server.URL+"/api/v1/metrics/query?format=prometheus", nil)
		req.Header.Set("Authorization", "Bearer "+a.admin)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid exporter response")
		}
		return string(raw)
	}
	if text := getPrometheus(); !strings.Contains(text, `dbhub_query_receipts_last_24h{state="unknown"} 3`) || !strings.Contains(text, "dbhub_query_timeouts_total 1") {
		t.Fatal("Prometheus export lost counters")
	}

	// 回执存储故障仍能观察本进程，禁止把未知的持久化数据显示成零。
	if _, err := a.pool.Exec(ctx, "ALTER TABLE sys_query_executions RENAME TO metrics_unavailable_receipts"); err != nil {
		t.Fatal(err)
	}
	defer a.pool.Exec(ctx, "ALTER TABLE metrics_unavailable_receipts RENAME TO sys_query_executions")
	partial := read()
	if partial.ReceiptStoreAvailable || partial.Receipts != nil || partial.Requests.Total == 0 {
		t.Fatal("partial availability was hidden")
	}
	if text := getPrometheus(); !strings.Contains(text, "dbhub_query_receipt_store_available 0") || strings.Contains(text, `dbhub_query_receipts_last_24h{state=`) {
		t.Fatal("durable metrics outage was presented as zero")
	}
}
