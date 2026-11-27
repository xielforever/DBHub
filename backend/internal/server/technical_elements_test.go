package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestTechnicalElementInventory(t *testing.T) {
	a := setupAPI(t)
	connection := a.connID
	other := a.connectionFor(a.readonly)
	columns := `[{"name":"order_id","data_type":"bigint","ordinal":1},{"name":"buyer_email","data_type":"text","ordinal":2}]`
	insertTechnicalSnapshot(t, a, connection, "retail", "sales", "orders", "table", columns,
		`[{"name":"orders_idx","columns":["order_id"],"is_unique":true}]`,
		`[{"name":"orders_pkey","kind":"primary_key","columns":["order_id"]}]`)
	insertTechnicalSnapshot(t, a, connection, "retail", "sales", "current_orders", "view", columns, "[]", "[]")
	insertTechnicalSnapshot(t, a, other, "private_db", "sales", "other_orders", "table", columns, "[]", "[]")
	a.call("PUT", "/assets/annotations", a.admin, map[string]any{
		"connection_id": connection, "database": "retail", "schema": "sales", "table": "orders",
		"sensitivity": "confidential", "tags": []string{"订单"},
	}, 200)
	a.call("PUT", "/assets/annotations", a.admin, map[string]any{
		"connection_id": connection, "database": "retail", "schema": "sales", "table": "orders",
		"column": "buyer_email", "sensitivity": "sensitive", "tags": []string{"个人信息"},
	}, 200)

	a.call("GET", "/assets/elements", "", nil, 401)
	all := a.call("GET", "/assets/elements?page_size=1&page=2", a.admin, nil, 200)
	if all["total"] != float64(10) || len(all["items"].([]any)) != 1 || all["page"] != float64(2) {
		t.Fatal("total should include each visible element exactly once before pagination", all)
	}
	database := a.call("GET", "/assets/elements?type=database", a.admin, nil, 200)
	if database["total"] != float64(1) || database["items"].([]any)[0].(map[string]any)["name"] != "retail" {
		t.Fatal("database grouping or visibility is incorrect", database)
	}
	schema := a.call("GET", fmt.Sprintf("/assets/elements?connection_id=%d&database=retail&type=schema", connection), a.admin, nil, 200)
	if schema["total"] != float64(1) || schema["items"].([]any)[0].(map[string]any)["name"] != "sales" {
		t.Fatal("database drill-down did not keep the correct namespace", schema)
	}
	prefix := fmt.Sprintf("/assets/elements?connection_id=%d&", connection)
	children := a.call("GET", prefix+"database=retail&schema=sales&table=orders&type=field&type=index&type=constraint", a.admin, nil, 200)
	if children["total"] != float64(4) {
		t.Fatal("table drill-down did not return the exact object's children", children)
	}
	field := a.call("GET", prefix+"type=field&level=sensitive&tag=个人信息&q=BUYER", a.admin, nil, 200)
	rows := field["items"].([]any)
	if field["total"] != float64(1) || len(rows) != 1 || rows[0].(map[string]any)["name"] != "buyer_email" {
		t.Fatal("field filters must use the field's own annotations", field)
	}
	for _, query := range []string{"type=field&level=confidential", "type=field&tag=订单", "q=%25"} {
		result := a.call("GET", prefix+query, a.admin, nil, 200)
		if result["total"] != float64(0) {
			t.Fatal("table annotations were inherited by a field or search treated a wildcard as a pattern", result)
		}
	}
	hidden := a.call("GET", fmt.Sprintf("/assets/elements?connection_id=%d", other), a.admin, nil, 200)
	if hidden["total"] != float64(0) || len(hidden["sources"].([]any)) != 1 || len(hidden["available_tags"].([]any)) != 0 {
		t.Fatal("another user's snapshot or facets were exposed", hidden)
	}
	a.call("GET", "/assets/elements?type=unknown", a.admin, nil, 400)
	a.call("GET", "/assets/elements?database=retail", a.admin, nil, 400)
}

func insertTechnicalSnapshot(t *testing.T, a *apiTest, connection int64, database, schema, name, kind, columns, indexes, keys string) {
	t.Helper()
	_, err := a.pool.Exec(context.Background(), `
		INSERT INTO sys_meta_snapshots (
			connection_id, database_name, schema_name, table_name, table_type, raw_columns, raw_indexes, raw_keys
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb)
	`, connection, database, schema, name, kind, json.RawMessage(columns), json.RawMessage(indexes), json.RawMessage(keys))
	if err != nil {
		t.Fatal(err)
	}
}
