package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func slowQuery(t target, seconds int) string {
	if t.kind == "postgres" {
		return fmt.Sprintf("SELECT pg_sleep(%d) AS pause_complete", seconds)
	}
	return fmt.Sprintf("SELECT SLEEP(%d) AS pause_complete", seconds)
}
func probeCall(ctx context.Context, f *fixtures, a actor, t target, query string, timeout int) apiResult {
	return f.c.call(ctx, "POST", "/api/v1/query/execute", a.token, map[string]any{"request_id": newID(), "connection_id": t.id, "database": t.database, "sql": query, "timeout_ms": timeout})
}
func receiptState(r apiResult) string {
	var receipt struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(r.data, &receipt)
	return receipt.Status
}
func waitExecuting(ctx context.Context, f *fixtures, count uint64) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := f.c.metrics(ctx)
		if err != nil {
			return err
		}
		if s.Executions.InFlight == count {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("压测已取消")
		case <-time.After(10 * time.Millisecond):
		}
	}
	return fmt.Errorf("受控慢查询未启动")
}
func runTimeoutProbe(ctx context.Context, f *fixtures, t target) (phaseReport, error) {
	p, err := beginPhase(ctx, f)
	if err != nil {
		return phaseReport{}, err
	}
	defer p.stopSampling()
	response := probeCall(ctx, f, f.actors[0], t, slowQuery(t, 2), 1000)
	s := sample{elapsed: response.elapsed, status: response.status, bytes: response.bytes, valid: response.err == nil && response.status == 400 && receiptState(response) == "unknown"}
	r, err := p.finish(ctx, t, "execution_timeout_probe", 1, []sample{s})
	if err == nil && (r.Metrics.Timeouts != 1 || r.Metrics.Executions != 1 || r.Metrics.Canceled != 0) {
		r.Valid = false
		err = fmt.Errorf("执行超时未被正确观测")
	}
	return r, err
}
func runCancelProbe(ctx context.Context, f *fixtures, t target) (phaseReport, error) {
	p, err := beginPhase(ctx, f)
	if err != nil {
		return phaseReport{}, err
	}
	defer p.stopSampling()
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan apiResult, 1)
	go func() { done <- probeCall(callCtx, f, f.actors[0], t, slowQuery(t, 10), 30000) }()
	startErr := waitExecuting(ctx, f, 1)
	cancel()
	response := <-done
	s := sample{elapsed: response.elapsed, status: response.status, bytes: response.bytes, valid: startErr == nil && response.err != nil}
	r, err := p.finish(ctx, t, "client_cancel_probe", 1, []sample{s})
	if err == nil && (r.Metrics.Canceled != 1 || r.Metrics.Executions != 1 || r.Metrics.Timeouts != 0) {
		r.Valid = false
		err = fmt.Errorf("客户端取消未被正确观测")
	}
	return r, err
}
func runQueueTimeoutProbe(ctx context.Context, f *fixtures, t target) (phaseReport, error) {
	settings := f.c.call(ctx, "PUT", "/api/v1/query/settings", f.actors[0].token, map[string]any{"timeout_ms": 60000, "max_rows": 1000, "block_unsafe_write": true})
	if settings.err != nil || settings.status != 200 || settings.code != 0 {
		return phaseReport{}, fmt.Errorf("临时账号的队列探测设置失败")
	}
	p, err := beginPhase(ctx, f)
	if err != nil {
		return phaseReport{}, err
	}
	defer p.stopSampling()
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan apiResult, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- probeCall(callCtx, f, f.actors[0], t, slowQuery(t, 40), 60000) }()
	}
	startErr := waitExecuting(ctx, f, 2)
	var response apiResult
	if startErr == nil {
		response = probeCall(ctx, f, f.actors[0], t, "SELECT 1", 1000)
	}
	cancel()
	samples := []sample{{elapsed: response.elapsed, status: response.status, bytes: response.bytes, valid: startErr == nil && response.status == 429 && receiptState(response) == "rejected" && response.elapsed >= 25*time.Second}}
	for i := 0; i < 2; i++ {
		r := <-done
		samples = append(samples, sample{elapsed: r.elapsed, status: r.status, bytes: r.bytes, valid: r.err != nil})
	}
	r, err := p.finish(ctx, t, "queue_wait_timeout_probe", 3, samples)
	if err == nil && (r.Metrics.QueueTimeouts != 1 || r.Metrics.Timeouts != 1 || r.Metrics.Canceled != 2 || r.Metrics.Executions != 2) {
		r.Valid = false
		err = fmt.Errorf("排队超时与取消指标不一致")
	}
	return r, err
}
