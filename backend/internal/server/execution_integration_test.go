package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

type executionResponse struct {
	status  int
	message string
	data    json.RawMessage
	err     error
}

// 不在并发 goroutine 中调用 Fatal，所有网络结果交回测试主 goroutine 检查。
func executionRequest(client *http.Client, base, method, path, token string, body any) executionResponse {
	return executionRequestContext(context.Background(), client, base, method, path, token, body)
}
func executionRequestContext(ctx context.Context, client *http.Client, base, method, path, token string, body any) executionResponse {
	raw, err := json.Marshal(body)
	if err != nil {
		return executionResponse{err: err}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+"/api/v1"+path, reader)
	if err != nil {
		return executionResponse{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return executionResponse{err: err}
	}
	defer res.Body.Close()
	var out struct {
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	err = json.NewDecoder(res.Body).Decode(&out)
	return executionResponse{res.StatusCode, out.Message, out.Data, err}
}
func requireExecutionStatus(t *testing.T, res executionResponse, status int) {
	t.Helper()
	if res.err != nil || res.status != status {
		t.Fatalf("HTTP status=%d, want=%d, error=%v, message=%s", res.status, status, res.err, res.message)
	}
}
func decodeReceipt(t *testing.T, res executionResponse) db.QueryExecution {
	t.Helper()
	var receipt db.QueryExecution
	if err := json.Unmarshal(res.data, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestExecutionReliability(t *testing.T) {
	a := setupAPI(t)
	ctx := context.Background()
	otherDeps := *a.deps
	cfg := &config.Config{Env: "development", SecretKey: []byte("test-main-key-32-bytes-long-123456789"), AccessTTL: time.Hour, RefreshTTL: time.Hour}
	otherServer := httptest.NewServer(NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps.UserRepo, &otherDeps))
	defer otherServer.Close()
	defer otherDeps.Queries.Close()
	client := &http.Client{Timeout: 8 * time.Second}
	call := func(method, path string, body any) executionResponse {
		return executionRequest(client, a.server.URL, method, path, a.admin, body)
	}
	owner := func() int {
		t.Helper()
		var n int
		if err := a.pool.QueryRow(ctx, "SELECT owner_id FROM public."+a.table+" WHERE id=1").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	write := func(id string) map[string]any {
		return map[string]any{"request_id": id, "connection_id": a.connID, "sql": "UPDATE " + a.table + " SET owner_id=owner_id+1 WHERE id=1"}
	}

	t.Run("concurrent-duplicate-across-independent-handlers-executes-once", func(t *testing.T) {
		before, id := owner(), testRequestID()
		body := write(id)
		body["sql"] = "WITH pause AS MATERIALIZED (SELECT pg_sleep(0.15)) UPDATE " + a.table + " SET owner_id=owner_id+1 FROM pause WHERE id=1"
		const count = 24
		results := make(chan executionResponse, count)
		var wg sync.WaitGroup
		for i := 0; i < count; i++ {
			base := a.server.URL
			if i%2 == 0 {
				base = otherServer.URL
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- executionRequest(client, base, "POST", "/query/execute", a.admin, body)
			}()
		}
		wg.Wait()
		close(results)
		winners := 0
		for res := range results {
			if res.status == 200 {
				winners++
				requireExecutionStatus(t, res, 200)
			} else {
				requireExecutionStatus(t, res, 409)
			}
		}
		if winners != 1 || owner() != before+1 {
			t.Fatalf("duplicate write: winners=%d, count delta=%d", winners, owner()-before)
		}
		res := call("GET", "/query/executions/"+id, nil)
		requireExecutionStatus(t, res, 200)
		if r := decodeReceipt(t, res); r.Status != "succeeded" || r.AffectedRows != 1 {
			t.Fatalf("receipt=%+v", r)
		}
		var fields map[string]any
		_ = json.Unmarshal(res.data, &fields)
		for _, field := range []string{"sql", "params", "rows", "columns", "request_hash"} {
			if _, ok := fields[field]; ok {
				t.Fatalf("receipt exposed %s", field)
			}
		}
		requireExecutionStatus(t, executionRequest(client, a.server.URL, "GET", "/query/executions/"+id, a.developer, nil), 404)
		for _, change := range []map[string]any{{"sql": "SELECT 1"}, {"params": map[string]any{"unused": 1}}, {"max_rows": 1}, {"timeout_ms": 1500}} {
			altered := map[string]any{}
			for k, v := range body {
				altered[k] = v
			}
			for k, v := range change {
				altered[k] = v
			}
			requireExecutionStatus(t, call("POST", "/query/execute", altered), 409)
		}
		if owner() != before+1 {
			t.Fatal("a conflicting request executed")
		}
		requireExecutionStatus(t, executionRequest(client, a.server.URL, "POST", "/query/execute", a.developer, body), 200)
		if owner() != before+2 {
			t.Fatal("request ID namespaces were not isolated by user")
		}
	})

	t.Run("missing-id-and-preflight-rejection-never-send-write", func(t *testing.T) {
		before, body := owner(), write("")
		requireExecutionStatus(t, call("POST", "/query/execute", body), 400)
		id := testRequestID()
		body = map[string]any{"request_id": id, "connection_id": a.connID, "sql": "DELETE FROM " + a.table}
		res := call("POST", "/query/execute", body)
		requireExecutionStatus(t, res, 400)
		if decodeReceipt(t, res).Status != "rejected" {
			t.Fatal("preflight failure was not distinguished from uncertain execution")
		}
		requireExecutionStatus(t, call("POST", "/query/execute", body), 409)
		if owner() != before {
			t.Fatal("rejected request modified data")
		}
		for _, condition := range []string{"TRUE", "1=1", "id=1 OR 1=1"} {
			requireExecutionStatus(t, call("POST", "/query/execute", map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": "DELETE FROM " + a.table + " WHERE " + condition}), 400)
		}
		if owner() != before {
			t.Fatal("always-true condition bypassed write protection")
		}
	})

	t.Run("failure-to-mark-started-prevents-sending-sql", func(t *testing.T) {
		before := owner()
		_, err := a.pool.Exec(ctx, `CREATE FUNCTION reject_execution_start() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.status='running' THEN RAISE EXCEPTION 'simulated start persistence failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_execution_start BEFORE UPDATE ON sys_query_executions FOR EACH ROW EXECUTE FUNCTION reject_execution_start()`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = a.pool.Exec(ctx, "DROP TRIGGER IF EXISTS reject_execution_start ON sys_query_executions; DROP FUNCTION IF EXISTS reject_execution_start()")
		}()
		res := call("POST", "/query/execute", write(testRequestID()))
		requireExecutionStatus(t, res, 400)
		if decodeReceipt(t, res).Status != "rejected" || owner() != before {
			t.Fatal("SQL was sent without a durable execution state")
		}
	})

	t.Run("response-loss-can-be-reconciled-without-repeating-write", func(t *testing.T) {
		before, id := owner(), testRequestID()
		body := write(id)
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res := call("POST", "/query/execute", body)
			if res.status != 200 {
				t.Errorf("upstream did not complete: %d", res.status)
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}))
		defer proxy.Close()
		lost := executionRequest(client, proxy.URL, "POST", "/query/execute", a.admin, body)
		if lost.err == nil {
			t.Fatal("response loss was not simulated")
		}
		res := call("GET", "/query/executions/"+id, nil)
		requireExecutionStatus(t, res, 200)
		if decodeReceipt(t, res).Status != "succeeded" {
			t.Fatal("completed write was not recoverable")
		}
		requireExecutionStatus(t, executionRequest(client, otherServer.URL, "POST", "/query/execute", a.admin, body), 409)
		if owner() != before+1 {
			t.Fatal("response loss resulted in repeated write")
		}
	})

	t.Run("database-lock-timeout-keeps-an-unknown-fence-and-releases-slot", func(t *testing.T) {
		before, id := owner(), testRequestID()
		blocker, err := a.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback(ctx)
		if _, err = blocker.Exec(ctx, "UPDATE public."+a.table+" SET owner_id=owner_id WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		body := write(id)
		body["timeout_ms"] = 1000
		res := call("POST", "/query/execute", body)
		requireExecutionStatus(t, res, 400)
		if decodeReceipt(t, res).Status != "unknown" {
			t.Fatal("timeout was incorrectly declared safe to retry")
		}
		_ = blocker.Rollback(ctx)
		requireExecutionStatus(t, call("POST", "/query/execute", body), 409)
		requireExecutionStatus(t, call("POST", "/query/execute", map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": "SELECT 1"}), 200)
		if owner() != before {
			t.Fatal("timed-out lock waiter modified data")
		}
	})

	t.Run("receipt-save-failure-and-stale-running-state-never-reexecute", func(t *testing.T) {
		before, id := owner(), testRequestID()
		_, err := a.pool.Exec(ctx, `CREATE FUNCTION reject_execution_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.status='succeeded' THEN RAISE EXCEPTION 'simulated receipt persistence failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_execution_receipt BEFORE UPDATE ON sys_query_executions FOR EACH ROW EXECUTE FUNCTION reject_execution_receipt()`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = a.pool.Exec(ctx, "DROP TRIGGER IF EXISTS reject_execution_receipt ON sys_query_executions; DROP FUNCTION IF EXISTS reject_execution_receipt()")
		}()
		body := write(id)
		res := call("POST", "/query/execute", body)
		requireExecutionStatus(t, res, 503)
		if decodeReceipt(t, res).Status != "unknown" || owner() != before+1 {
			t.Fatal("lost receipt did not preserve the committed write and uncertainty")
		}
		if _, err = a.pool.Exec(ctx, "UPDATE sys_query_executions SET updated_at=NOW()-INTERVAL '10 minutes' WHERE request_id=$1", id); err != nil {
			t.Fatal(err)
		}
		res = executionRequest(client, otherServer.URL, "GET", "/query/executions/"+id, a.admin, nil)
		requireExecutionStatus(t, res, 200)
		if decodeReceipt(t, res).Status != "unknown" {
			t.Fatal("stale execution was not reported as uncertain")
		}
		requireExecutionStatus(t, executionRequest(client, otherServer.URL, "POST", "/query/execute", a.admin, body), 409)
		if owner() != before+1 {
			t.Fatal("stale execution was replayed")
		}
	})

	t.Run("new-request-id-allows-an-intentional-write", func(t *testing.T) {
		before := owner()
		res := call("POST", "/query/execute", write(testRequestID()))
		requireExecutionStatus(t, res, 200)
		if owner() != before+1 {
			t.Fatal(fmt.Sprintf("new intentional execution missing: delta=%d", owner()-before))
		}
	})

	t.Run("real-http-user-queue-overload-rejects-and-recovers", func(t *testing.T) {
		loadCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		marker := fmt.Sprintf("reliability-overload-%d", time.Now().UnixNano())
		results := make(chan executionResponse, 10)
		ids := make([]string, 10)
		for i := range ids {
			ids[i] = testRequestID()
			body := map[string]any{"request_id": ids[i], "connection_id": a.connID, "sql": "SELECT pg_sleep(10) /* " + marker + " */"}
			go func() {
				results <- executionRequestContext(loadCtx, client, a.server.URL, "POST", "/query/execute", a.admin, body)
			}()
		}
		deadline := time.Now().Add(3 * time.Second)
		running, pending := 0, 0
		for time.Now().Before(deadline) {
			res := call("GET", "/query/jobs", nil)
			requireExecutionStatus(t, res, 200)
			var jobs struct {
				Items []struct {
					SQL    string `json:"sql"`
					Status string `json:"status"`
				} `json:"items"`
			}
			if err := json.Unmarshal(res.data, &jobs); err != nil {
				t.Fatal(err)
			}
			running, pending = 0, 0
			for _, job := range jobs.Items {
				if job.SQL != "SELECT pg_sleep(10) /* "+marker+" */" {
					continue
				}
				if job.Status == "running" {
					running++
				}
				if job.Status == "pending" {
					pending++
				}
			}
			if running == 2 && pending == 8 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if running != 2 || pending != 8 {
			t.Fatalf("user queue limits incorrect: running=%d, pending=%d", running, pending)
		}
		res := call("POST", "/query/execute", map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": "SELECT 1"})
		requireExecutionStatus(t, res, 429)
		if decodeReceipt(t, res).Status != "rejected" {
			t.Fatal("overload rejection was not persisted before execution")
		}
		cancel()
		for range ids {
			select {
			case <-results:
			case <-time.After(3 * time.Second):
				t.Fatal("canceled HTTP requests did not finish")
			}
		}
		deadline = time.Now().Add(3 * time.Second)
		settled := false
		for time.Now().Before(deadline) {
			settled = true
			for _, id := range ids {
				res := call("GET", "/query/executions/"+id, nil)
				requireExecutionStatus(t, res, 200)
				status := decodeReceipt(t, res).Status
				if status == "running" || status == "queued" {
					settled = false
				}
			}
			if settled {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !settled {
			t.Fatal("cancellation left queued or active executions")
		}
		requireExecutionStatus(t, call("POST", "/query/execute", map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": "SELECT 1"}), 200)
	})
}
