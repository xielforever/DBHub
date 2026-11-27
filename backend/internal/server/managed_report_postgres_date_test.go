package server

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/user/dbhub/internal/db"
)

func TestManagedReportPostgresDateContract(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	connection := a.connectionFor(a.developer)
	schema := []db.ContractColumn{
		{Name: "business_day", Type: "date"},
		{Name: "iso_day", Type: "date"},
		{Name: "optional_day", Type: "date", Nullable: true},
		{Name: "midnight", Type: "timestamp_with_timezone"},
	}
	contract := db.DataContract{
		SemanticKey: "business-day", Meaning: "日期契约回归样本", Schema: schema,
		ChartType: "table", Unit: "day", Timezone: "UTC",
		ScopeKey: "date-fixture", ScopeDescription: "隔离日期样本",
		TimeWindow: []byte(`{"kind":"current_snapshot"}`),
		Freshness:  db.ContractFreshness{Mode: "unverified"},
	}
	query := `SELECT DATE '2026-11-22' AS business_day,
       '2026-11-22'::text AS iso_day, NULL::date AS optional_day,
       TIMESTAMPTZ '2026-11-22 00:00:00+00' AS midnight`
	assertRow := func(result map[string]any) {
		t.Helper()
		rows := result["rows"].([]any)
		if len(rows) != 1 {
			t.Fatalf("日期样本应返回一行，实际为 %v", rows)
		}
		row := rows[0].([]any)
		if len(row) != 4 || !reflect.DeepEqual(row[:3], []any{"2026-11-22", "2026-11-22", nil}) {
			t.Fatalf("原生日期、ISO 文本或 NULL 结果不符：%v", row)
		}
		stamp, err := time.Parse(time.RFC3339Nano, fmt.Sprint(row[3]))
		if err != nil || !stamp.Equal(time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("午夜时间戳失去时间或时区语义：%v", row[3])
		}
	}
	create := func(sql string, declaration db.DataContract) int64 {
		data := a.call("POST", "/managed-reports", a.developer, map[string]any{
			"request_id": testRequestID(), "name": "日期契约", "connection_id": connection,
			"sql": sql, "chart_type": "table", "chart_config": map[string]any{}, "data_contract": declaration,
		}, 200)
		return int64(data["id"].(float64))
	}
	verify := func(review int64, expectedStatus int) map[string]any {
		return a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), a.admin, map[string]any{
			"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3},
		}, expectedStatus)
	}

	preview := a.call("POST", "/reports/preview", a.developer, map[string]any{
		"request_id": testRequestID(), "connection_id": connection, "sql": query, "data_contract": contract,
	}, 200)
	if preview["contract_check"].(map[string]any)["passed"] != true {
		t.Fatal("预览日期契约校验失败", preview["contract_check"])
	}
	assertRow(preview)

	id := create(query, contract)
	review := f.submit(id, 1)
	validation := verify(review, 200)
	assertRow(validation["result"].(map[string]any))
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{
		"request_id": testRequestID(), "decision": "approved", "contract_confirmed": true,
		"validation_id": validation["validation_id"],
	}, 200)
	f.release(id, 0, review, "publish")
	requestID := testRequestID()
	published := a.call("POST", fmt.Sprintf("/published-reports/%d/execute", id), a.readonly, map[string]any{
		"request_id": requestID, "publication_no": 1,
	}, 200)
	assertRow(published)
	receipt := a.call("GET", "/query/executions/"+requestID, a.readonly, nil, 200)
	if receipt["status"] != "succeeded" || receipt["kind"] != "query" {
		t.Fatal("缺少真实读者执行成功回执", receipt)
	}

	ordinary := a.exec(a.developer, `SELECT DATE '2026-11-22' AS business_day`, 200)
	if value := ordinary["rows"].([]any)[0].([]any)[0]; value != "2026-11-22T00:00:00Z" {
		t.Fatalf("工作台普通查询的日期输出变化：%v", value)
	}

	for _, tc := range []struct {
		name   string
		column string
	}{
		{"invalid-date-text", `'2026-02-30'::text`},
		{"null-not-allowed", `NULL::date`},
		{"infinite-date", `DATE 'infinity'`},
		{"midnight-is-a-timestamp", `TIMESTAMPTZ '2026-11-22 00:00:00+00'`},
	} {
		t.Run(tc.name, func(subtest *testing.T) {
			a.t = subtest
			defer func() { a.t = t }()
			invalid := contract
			invalid.Schema = schema[:1]
			reportID := create("SELECT "+tc.column+" AS business_day", invalid)
			verify(f.submit(reportID, 1), 409)
		})
	}
}
