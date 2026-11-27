package server

import (
	"fmt"
	"testing"
)

func TestMetricCatalogAuthorization(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	id, _ := f.draft("SELECT id,email FROM public." + f.view)
	review := f.submit(id, 1)
	validation := f.validate(review, []int64{1, 3}, nil)
	f.approve(review, validation)
	f.release(id, 0, review, "publish")
	if page := a.call("GET", "/metric-catalog", a.readonly, nil, 200); page["total"] != float64(0) {
		t.Fatal("legacy report without contract entered the metric catalog")
	}
	contract := map[string]any{"semantic_key": "test.metric_catalog", "meaning": "用户已批准的结果定义", "result_schema": []any{map[string]any{"name": "id", "logical_type": "integer", "nullable": false}, map[string]any{"name": "email", "logical_type": "string", "nullable": false}}, "dimensions": []string{"id"}, "metrics": []string{}, "chart_type": "table", "unit": "item_count", "timezone": "UTC", "scope_key": "catalog_test", "scope_description": "固定只读视图", "time_window": map[string]any{"kind": "current_snapshot"}, "comparison_key": "catalog_test", "freshness": map[string]any{"mode": "unverified"}}
	created := a.call("POST", "/managed-reports", a.developer, map[string]any{"request_id": testRequestID(), "name": "已声明口径", "connection_id": a.connectionFor(a.developer), "sql": "SELECT id,email FROM public." + f.view, "chart_type": "table", "chart_config": map[string]any{}, "data_contract": contract}, 200)
	id = int64(created["id"].(float64))
	review = f.submit(id, 1)
	validation = f.validate(review, []int64{1, 3}, nil)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(id, 0, review, "publish")
	page := a.call("GET", "/metric-catalog?q=test.metric_catalog&page_size=1", a.readonly, nil, 200)
	if page["total"] != float64(1) || len(page["items"].([]any)) != 1 {
		t.Fatal("catalog count or semantic key search incorrect", page)
	}
	if page := a.call("GET", "/metric-catalog", a.developer, nil, 200); page["total"] != float64(0) {
		t.Fatal("catalog treated authorship as a viewer grant")
	}
	account := a.call("GET", "/report-accounts", a.admin, nil, 200)["items"].([]any)[0].(map[string]any)
	a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": account["version"], "enabled": false, "expected_reports": 2}, 200)
	if page := a.call("GET", "/metric-catalog", a.readonly, nil, 200); page["total"] != float64(0) {
		t.Fatal("disabled execution account remained discoverable")
	}
}
