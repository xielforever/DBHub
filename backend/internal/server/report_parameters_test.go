package server

import (
	"context"
	"fmt"
	"testing"
)

func TestReportParametersRealDatabase(t *testing.T) {
	a := setupAPI(t)
	defs := []any{map[string]any{"name": "owner", "label": "归属编号", "type": "integer", "required": true, "default": 1, "min": 1, "max": 2}, map[string]any{"name": "q", "label": "邮箱搜索", "type": "text", "max_length": 100}}
	sql := "SELECT id,email FROM public." + a.table + " WHERE owner_id=:owner::integer AND (:q::text IS NULL OR email=:q::text) ORDER BY id"
	body := map[string]any{"name": "带参数库存", "connection_id": a.connID, "sql": sql, "parameters": defs, "chart_type": "table", "chart_config": map[string]any{}, "visibility": "shared", "request_id": testRequestID()}
	report := a.call("POST", "/reports", a.admin, body, 200)
	path := fmt.Sprintf("/reports/%d/execute", int64(report["id"].(float64)))
	run := func(values map[string]any, status int) map[string]any {
		return a.call("POST", path, a.readonly, map[string]any{"version": 1, "request_id": testRequestID(), "params": values}, status)
	}
	if got := run(nil, 200); len(got["rows"].([]any)) != 1 || got["rows"].([]any)[0].([]any)[0] != float64(1) {
		t.Fatal("saved defaults were not executed", got)
	}
	if got := run(map[string]any{"owner": 2}, 200); got["rows"].([]any)[0].([]any)[0] != float64(2) {
		t.Fatal("viewer filter was ignored", got)
	}
	if got := run(map[string]any{"q": "' OR TRUE; --"}, 200); len(got["rows"].([]any)) != 0 {
		t.Fatal("text filter changed SQL semantics", got)
	}
	for _, values := range []map[string]any{{"owner": 3}, {"owner": nil}, {"owner": 1.5}, {"sql": "SELECT 1"}, {"q": []any{"x"}}} {
		run(values, 400)
	}
	a.call("POST", path, a.readonly, map[string]any{"version": 1, "request_id": testRequestID(), "params": map[string]any{}, "sql": "SELECT 99"}, 400)
	preview := a.call("POST", "/reports/preview", a.admin, map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": sql, "parameters": defs, "params": map[string]any{"owner": 2}}, 200)
	if len(preview["rows"].([]any)) != 1 || preview["rows"].([]any)[0].([]any)[0] != float64(2) {
		t.Fatal("preview used different binding rules")
	}
	key := testRequestID()
	a.call("POST", path, a.readonly, map[string]any{"version": 1, "request_id": key, "params": map[string]any{"owner": 1}}, 200)
	a.call("POST", path, a.readonly, map[string]any{"version": 1, "request_id": key, "params": map[string]any{"owner": 2}}, 409)
	body["parameters"] = []any{}
	body["request_id"] = testRequestID()
	a.call("POST", "/reports", a.admin, body, 400)
}

func TestManagedAndDashboardParametersRealDatabase(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	if _, e := a.targetPool.Exec(context.Background(), "INSERT INTO public."+a.table+" VALUES(3,1,'c@example.com','three')"); e != nil {
		t.Fatal(e)
	}
	id, body := dashboardReport(f)
	body["parameters"] = []any{map[string]any{"name": "min_id", "label": "最小编号", "type": "integer", "required": true, "default": 1, "min": 1, "max": 3}}
	body["sql"] = "SELECT id,email,date_trunc('minute',now()) AS watermark FROM public." + f.view + " WHERE id>=:min_id::integer ORDER BY id"
	body["version"] = 1
	body["request_id"] = testRequestID()
	path := fmt.Sprintf("/managed-reports/%d", id)
	a.call("PUT", path+"/draft", a.developer, body, 200)
	review := f.submit(id, 2)
	validation := f.validate(review, []int64{1, 2, 3}, nil)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(id, 1, review, "publish")
	published := a.call("GET", fmt.Sprintf("/published-reports/%d", id), a.readonly, nil, 200)
	list := a.call("GET", "/published-reports?page_size=100", a.readonly, nil, 200)
	listed := list["items"].([]any)[0].(map[string]any)
	if len(listed["parameters"].([]any)) != 1 || listed["data_contract"] == nil {
		t.Fatal("business directory omitted approved parameter or metric metadata")
	}
	if len(published["parameters"].([]any)) != 1 {
		t.Fatal("approved parameter metadata unavailable")
	}
	result := a.call("POST", fmt.Sprintf("/published-reports/%d/execute", id), a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 2, "params": map[string]any{"min_id": 3}}, 200)
	if len(result["rows"].([]any)) != 1 || result["rows"].([]any)[0].([]any)[0] != float64(3) {
		t.Fatal("published execution ignored parameters", result)
	}
	a.call("POST", fmt.Sprintf("/published-reports/%d/execute", id), a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 2, "params": map[string]any{"account_id": f.account}}, 400)
	definition := map[string]any{"name": "统一筛选看板", "parameters": []any{map[string]any{"name": "minimum", "label": "编号下限", "type": "integer", "required": true, "default": 1}}, "tiles": []any{map[string]any{"key": "stock", "report_id": id, "required": true, "width": "full", "binding_policy": "follow_published", "parameter_bindings": map[string]string{"min_id": "minimum"}}}, "user_ids": []int64{3}, "group_ids": []int64{}, "quality": map[string]any{"allow_unknown_freshness": false, "max_watermark_skew_seconds": 300}}
	d := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": definition}, 200)
	dpath := fmt.Sprintf("/managed-dashboards/%d", int64(d["id"].(float64)))
	refresh := a.call("POST", dpath+"/refreshes", a.developer, map[string]any{"mode": "draft", "params": map[string]any{"minimum": 3}}, 200)
	tilepath := "/dashboard-refreshes/" + refresh["refresh_id"].(string) + "/tiles/stock/execute"
	tile := a.call("POST", tilepath, a.developer, map[string]any{}, 200)
	rows := tile["result"].(map[string]any)["rows"].([]any)
	if len(rows) != 1 || rows[0].([]any)[0] != float64(3) {
		t.Fatal("global filter did not reach frozen tile execution", tile)
	}
	a.call("POST", tilepath, a.developer, map[string]any{"params": map[string]any{"min_id": 1}}, 400)
	a.call("POST", dpath+"/refreshes", a.readonly, map[string]any{"mode": "draft", "params": map[string]any{"minimum": 1}}, 403)
	a.call("POST", dpath+"/refreshes", a.developer, map[string]any{"mode": "draft", "params": map[string]any{"minimum": 99}}, 400)
	a.call("POST", dpath+"/refreshes", a.developer, map[string]any{"mode": "draft", "params": map[string]any{"unknown": 1}}, 400)
}
