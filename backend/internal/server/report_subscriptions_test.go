package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/user/dbhub/internal/config"
)

func subscriptionMetricReport(t *testing.T, f *managedFixture) int64 {
	t.Helper()
	a := f.a
	contract := map[string]any{"semantic_key": "subscription_metric", "meaning": "受限视图的精确指标", "result_schema": []any{map[string]any{"name": "metric_value", "logical_type": "integer", "nullable": false}, map[string]any{"name": "watermark", "logical_type": "timestamp_with_timezone", "nullable": false}}, "dimensions": []string{}, "metrics": []string{"metric_value"}, "chart_type": "metric", "unit": "item_count", "currency": "", "timezone": "UTC", "scope_key": "owner1", "scope_description": "服务账号仅能读取owner1视图", "time_window": map[string]any{"kind": "current_snapshot"}, "comparison_key": "subscription_metric", "freshness": map[string]any{"mode": "watermark_column", "column": "watermark", "max_data_age_seconds": 900, "max_future_skew_seconds": 60}}
	created := a.call("POST", "/managed-reports", a.developer, map[string]any{"request_id": testRequestID(), "name": "受控订阅指标", "connection_id": a.connectionFor(a.developer), "sql": "SELECT COALESCE(sum(id),0)::bigint AS metric_value,date_trunc('minute',now()) AS watermark FROM public." + f.view, "chart_type": "metric", "chart_config": map[string]any{"metrics": []string{"metric_value"}}, "data_contract": contract}, 200)
	id := int64(created["id"].(float64))
	review := f.submit(id, 1)
	validation := f.validate(review, []int64{3}, nil)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(id, 0, review, "publish")
	return id
}
func TestReportSubscriptionsLocalDeliveryAndAlerts(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := context.Background()
	report := subscriptionMetricReport(t, f)
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	definition := map[string]any{"name": "我的指标订阅", "report_id": report, "publication_no": 1, "interval_minutes": 5, "params": map[string]any{}, "enabled": true, "webhook": false, "alert_rule": map[string]any{"column": "metric_value", "operator": "gt", "threshold": "0"}}
	request := map[string]any{"request_id": testRequestID(), "definition": definition}
	created := a.call("POST", "/report-subscriptions", a.readonly, request, 200)
	same := a.call("POST", "/report-subscriptions", a.readonly, request, 200)
	if created["id"] != same["id"] {
		t.Fatal("retry created duplicate subscription")
	}
	id := int64(created["id"].(float64))
	path := fmt.Sprintf("/report-subscriptions/%d", id)
	if generated, err := service.Subscriptions.GenerateDue(ctx); err != nil || generated != 1 {
		t.Fatalf("due subscription not queued: %d %v", generated, err)
	}
	if generated, err := service.Subscriptions.GenerateDue(ctx); err != nil || generated != 0 {
		t.Fatal("same due window queued twice")
	}
	if _, err := service.RunSubscription(ctx); err != nil {
		t.Fatal(err)
	}
	runs := a.call("GET", "/report-deliveries", a.readonly, nil, 200)["items"].([]any)
	if len(runs) != 1 {
		t.Fatal("delivery missing")
	}
	run := runs[0].(map[string]any)
	if run["status"] != "succeeded" || run["row_count"] != float64(1) {
		t.Fatal("delivery not durably successful", run["status"])
	}
	delivery := int64(run["delivery_id"].(float64))
	snapshot := a.call("GET", fmt.Sprintf("/report-deliveries/%d", delivery), a.readonly, nil, 200)["snapshot"].(map[string]any)
	if snapshot["requester_id"] != float64(3) || snapshot["result"].(map[string]any)["rows"].([]any)[0].([]any)[0] != float64(1) {
		t.Fatal("snapshot not bound to subscriber or formal result")
	}
	a.call("GET", fmt.Sprintf("/report-deliveries/%d", delivery), a.developer, nil, 404)
	var encrypted string
	if err := a.pool.QueryRow(ctx, `SELECT encrypted_snapshot FROM sys_report_subscription_runs WHERE delivery_id=$1`, delivery).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encrypted, "v1:") || strings.Contains(encrypted, "metric_value") {
		t.Fatal("result not encrypted at rest")
	}
	incidents := a.call("GET", "/metric-incidents", a.readonly, nil, 200)["items"].([]any)
	if len(incidents) != 1 {
		t.Fatal("threshold did not open an incident")
	}
	incident := incidents[0].(map[string]any)
	incidentID := int64(incident["id"].(float64))
	incidentPath := fmt.Sprintf("/metric-incidents/%d", incidentID)
	if incident["condition_active"] != true || incident["value"] != "1" {
		t.Fatal("incident numeric observation incorrect")
	}
	a.call("GET", incidentPath, a.developer, nil, 404)
	runAgain := func() {
		a.call("POST", path+"/runs", a.readonly, map[string]any{"request_id": testRequestID(), "version": 1}, 200)
		if _, err := service.RunSubscription(ctx); err != nil {
			t.Fatal(err)
		}
	}
	runAgain()
	incidents = a.call("GET", "/metric-incidents", a.readonly, nil, 200)["items"].([]any)
	if len(incidents) != 1 {
		t.Fatal("persistent threshold duplicated a case")
	}
	incident = incidents[0].(map[string]any)
	ack := a.call("POST", incidentPath+"/actions", a.readonly, map[string]any{"request_id": testRequestID(), "version": incident["version"], "action": "acknowledge", "comment": "已确认，正在处理"}, 200)
	a.call("POST", incidentPath+"/actions", a.readonly, map[string]any{"request_id": testRequestID(), "version": ack["version"], "action": "close", "comment": "仍超阈值，不能假称恢复"}, 409)
	if _, err := a.targetPool.Exec(ctx, "UPDATE public."+a.table+" SET id=0 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	runAgain()
	current := a.call("GET", incidentPath, a.readonly, nil, 200)["incident"].(map[string]any)
	if current["condition_active"] != false || current["recovered_at"] == nil || current["status"] != "acknowledged" {
		t.Fatal("metric recovery lost human followup")
	}
	a.call("POST", incidentPath+"/actions", a.readonly, map[string]any{"request_id": testRequestID(), "version": current["version"], "action": "close", "comment": "已核对实际指标恢复，关闭跟踪"}, 200)
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_subscription_runs SET expires_at=statement_timestamp()-interval '1 second'
		WHERE delivery_id=$1`, delivery); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Subscriptions.GenerateDue(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.pool.QueryRow(ctx, `SELECT encrypted_snapshot FROM sys_report_subscription_runs WHERE delivery_id=$1`, delivery).Scan(&encrypted); err != nil || encrypted != "" {
		t.Fatalf("expired personal snapshot was retained: %v", err)
	}
	a.call("GET", fmt.Sprintf("/report-deliveries/%d", delivery), a.readonly, nil, 404)
	events := a.call("GET", incidentPath, a.readonly, nil, 200)["events"].([]any)
	observations := map[string]any{}
	for _, raw := range events {
		event := raw.(map[string]any)
		observations[event["event_type"].(string)] = event
	}
	for name, key := range map[string]string{"metric_opened": "1", "metric_recovered": "0"} {
		if event, ok := observations[name].(map[string]any); !ok || event["observed_value"] != key {
			t.Fatalf("original %s observation was not retained after snapshot expiry", name)
		}
	}
	if event, ok := observations["close"].(map[string]any); !ok || event["comment"] != "已核对实际指标恢复，关闭跟踪" {
		t.Fatal("metric disposal record was not retained")
	}
	t.Log("metric opened/recovered values and human disposition survived the snapshot purge")
	if _, err := a.targetPool.Exec(ctx, "UPDATE public."+a.table+" SET id=1 WHERE id=0"); err != nil {
		t.Fatal(err)
	}
	runAgain()
	if a.call("GET", "/metric-incidents", a.readonly, nil, 200)["total"] != float64(2) {
		t.Fatal("a later breach did not create a new incident")
	}
	bad := map[string]any{}
	raw, _ := json.Marshal(definition)
	json.Unmarshal(raw, &bad)
	bad["params"] = map[string]any{"sql": "SELECT 999"}
	a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(), "definition": bad}, 400)
	bad["params"] = map[string]any{}
	bad["webhook"] = true
	a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(), "definition": bad}, 400)
	// Revoke after queue admission. The worker must not open the target or
	// publish a snapshot by falling back to another workspace identity.
	a.call("POST", path+"/runs", a.readonly, map[string]any{"request_id": testRequestID(), "version": 1}, 200)
	members := a.call("GET", "/workspaces/1/members", a.admin, nil, 200)["items"].([]any)
	var version any
	for _, member := range members {
		item := member.(map[string]any)
		if item["id"] == float64(3) {
			version = item["membership_version"]
		}
	}
	a.call("PATCH", "/workspaces/1/members/3", a.admin, map[string]any{"expected_version": version, "active": false}, 200)
	if _, err := service.RunSubscription(ctx); err == nil {
		t.Fatal("revoked subscriber executed")
	}
	var enabled bool
	if err := a.pool.QueryRow(ctx, `SELECT enabled FROM sys_report_subscriptions WHERE id=$1`, id).Scan(&enabled); err != nil || enabled {
		t.Fatal("revoked schedule was not paused")
	}
	a.call("GET", fmt.Sprintf("/workspaces/1/report-deliveries/%d", delivery), a.readonly, nil, 404)
	var failures int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_report_subscription_runs WHERE subscription_id=$1 AND status='failed' AND encrypted_snapshot=''`, id).Scan(&failures); err != nil || failures != 1 {
		t.Fatal("revoked run left a result payload")
	}
}
