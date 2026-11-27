package server

import (
	"context"
	"fmt"
	"testing"
)

func TestReportQualityAndPromotionRealDatabase(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	var localAdminID int64
	if err := a.pool.QueryRow(context.Background(), `INSERT INTO sys_users(username,password_hash,role_id) SELECT 'workspace_admin',password_hash,(SELECT id FROM sys_roles WHERE code='readonly') FROM sys_users WHERE username='admin' RETURNING id`).Scan(&localAdminID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(context.Background(), `UPDATE sys_workspace_members SET role_code='admin' WHERE user_id=$1`, localAdminID); err != nil {
		t.Fatal(err)
	}
	workspaceAdmin := a.call("POST", "/auth/login", "", map[string]any{"username": "workspace_admin", "password": "TestPassword123!"}, 200)["access_token"].(string)
	a.call("GET", fmt.Sprintf("/datasource-definitions/%d/access", f.definition), workspaceAdmin, nil, 200)
	_, body := dashboardReport(f)
	contract := body["data_contract"].(map[string]any)
	contract["metric_checks"] = []any{map[string]any{"id": "ids_are_one", "label": "固定样本ID应为1", "kind": "all_equal", "column": "id", "expected": "1"}, map[string]any{"id": "one_row", "label": "恰好一行", "kind": "row_count", "expected": "1"}}
	body["name"] = "校验与迁移源"
	body["export_policy"] = map[string]any{"enabled": true, "max_rows": 1000}
	body["request_id"] = testRequestID()
	created := a.call("POST", "/managed-reports", a.developer, body, 200)
	id := int64(created["id"].(float64))
	path := fmt.Sprintf("/managed-reports/%d", id)
	review := f.submit(id, 1)
	validation := f.validate(review, []int64{3}, nil)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(id, 0, review, "publish")
	f.run(id, 1, 200)
	if _, err := a.targetPool.Exec(context.Background(), `UPDATE public.`+a.table+` SET id=4 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	f.run(id, 1, 409)
	a.call("POST", fmt.Sprintf("/published-reports/%d/exports", id), a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 1, "format": "csv", "reason": "指标失败时不交付导出"}, 409)
	preview := map[string]any{"request_id": testRequestID(), "connection_id": a.connectionFor(a.developer), "sql": body["sql"], "data_contract": contract}
	checked := a.call("POST", "/reports/preview", a.developer, preview, 200)["contract_check"].(map[string]any)
	if checked["passed"] != false || checked["metric_checks"].([]any)[0].(map[string]any)["passed"] != false {
		t.Fatal("regression invisible in author preview", checked)
	}
	preview["request_id"] = testRequestID()
	preview["sql"] = "SELECT id AS changed,email,date_trunc('minute',now()) AS watermark FROM public." + f.view
	checked = a.call("POST", "/reports/preview", a.developer, preview, 200)["contract_check"].(map[string]any)
	if checked["passed"] != false || checked["actual_columns"].([]any)[0] != "changed" || len(checked["issues"].([]any)) < 1 {
		t.Fatal("renamed field not explained", checked)
	}
	if _, err := a.targetPool.Exec(context.Background(), `UPDATE public.`+a.table+` SET id=1 WHERE id=4`); err != nil {
		t.Fatal(err)
	}
	f.run(id, 1, 200)
	// The editable draft deliberately diverges. Promotion must copy the approved version.
	body["request_id"] = testRequestID()
	body["version"] = 1
	body["sql"] = "SELECT 999 AS changed"
	a.call("PUT", path+"/draft", a.developer, body, 200)
	conn, password, err := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if err != nil {
		t.Fatal(err)
	}
	destination := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "隔离的正式环境模拟源", "visibility": "global", "config": map[string]any{"type": "postgres", "host": conn.Host, "port": conn.Port, "database": conn.Database, "ssl_mode": "disable", "connection_timeout": 3, "environment": "prod"}}, 201)
	definition := int64(destination["id"].(float64))
	target := a.call("POST", fmt.Sprintf("/datasource-definitions/%d/connections", definition), a.developer, map[string]any{"name": "目标环境个人身份", "username": conn.Username, "password": password, "confirmed": true}, 201)
	request := map[string]any{"request_id": testRequestID(), "review_id": review, "target_connection_id": target["id"], "name": "迁移后的待审报表", "clear_drillthrough": false}
	a.call("POST", path+"/promote", a.readonly, request, 403)
	promoted := a.call("POST", path+"/promote", a.developer, request, 200)
	again := a.call("POST", path+"/promote", a.developer, request, 200)
	if again["id"] != promoted["id"] {
		t.Fatal("retry created a second destination draft")
	}
	targetID := int64(promoted["id"].(float64))
	targetPath := fmt.Sprintf("/managed-reports/%d", targetID)
	copy := a.call("GET", targetPath, a.developer, nil, 200)
	draft := copy["draft"].(map[string]any)
	if copy["active_release_id"] != nil || copy["pending_count"] != float64(0) || copy["latest_version"] != float64(0) {
		t.Fatal("promotion carried source approval", copy)
	}
	if draft["sql_text"] == body["sql"] || draft["definition_revision_id"] != float64(definition) || draft["promotion"].(map[string]any)["source_review_id"] != float64(review) {
		t.Fatal("wrong source or target provenance", draft)
	}
	if draft["data_contract"].(map[string]any)["metric_checks"].([]any)[0].(map[string]any)["id"] != "ids_are_one" {
		t.Fatal("approved checks omitted")
	}
	// Editing may not remove or forge server-owned provenance.
	edit := map[string]any{"request_id": testRequestID(), "version": 1, "name": "目标草稿修订", "connection_id": target["id"], "sql": draft["sql_text"], "chart_type": "table", "chart_config": map[string]any{}, "data_contract": draft["data_contract"]}
	a.call("PUT", targetPath+"/draft", a.developer, edit, 200)
	if a.call("GET", targetPath, a.developer, nil, 200)["draft"].(map[string]any)["promotion"] == nil {
		t.Fatal("editing erased provenance")
	}
	edit["request_id"] = testRequestID()
	edit["version"] = 2
	edit["promotion"] = map[string]any{"source_report_id": 999}
	a.call("PUT", targetPath+"/draft", a.developer, edit, 400)
	targetReview := f.submit(targetID, 2)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", targetReview), a.admin, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 400)
	account := a.call("POST", "/report-accounts", a.admin, map[string]any{"request_id": testRequestID(), "name": "目标独立执行身份", "definition_revision_id": definition, "username": f.username, "password": f.password}, 200)
	validation = testRequestID()
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", targetReview), a.admin, map[string]any{"request_id": validation, "account_id": account["id"], "user_ids": []int64{3}}, 200)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", targetReview), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(targetID, 0, targetReview, "publish")
	f.run(targetID, 1, 200)
	f.run(id, 1, 200)
}
