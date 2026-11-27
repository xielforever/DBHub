package db

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/user/dbhub/internal/sqlparse"
)

func TestReportParameterDisplayValuesPreserveIntegerPrecision(t *testing.T) {
	input := map[string]any{"id": int64(9007199254740993), "active": false, "date": "2026-10-20"}
	output := ReportParameterDisplayValues([]ReportParameter{{Name: "id", Type: "integer"}}, input)
	if output["id"] != "9007199254740993" || output["active"] != false || output["date"] != "2026-10-20" {
		t.Fatal("frozen values changed on the JSON boundary")
	}
	if input["id"] != int64(9007199254740993) {
		t.Fatal("driver bindings were mutated")
	}
}

func TestReportParameterBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC)
	days := -7
	defs := []ReportParameter{
		{Name: "from", Label: "开始日期", Type: "date", Required: true, RelativeDays: &days, Timezone: "Asia/Singapore"},
		{Name: "id", Label: "编号", Type: "integer"},
		{Name: "amount", Label: "金额", Type: "decimal"},
		{Name: "city", Label: "城市", Type: "enum", Options: []ReportParameterOption{{Label: "上海", Value: "sh"}}},
		{Name: "active", Label: "启用", Type: "boolean", Default: false},
		{Name: "q", Label: "搜索", Type: "text", MaxLength: 100},
	}
	values, err := ResolveReportParameters(defs, map[string]any{"id": "9007199254740993", "amount": "12345678901234567890.123456789", "q": "' OR TRUE; --"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if values["from"] != "2026-10-13" || values["id"] != int64(9007199254740993) || values["amount"] != "12345678901234567890.123456789" || values["active"] != false || values["city"] != nil {
		t.Fatal("default, timezone or precision changed", values)
	}
	query, args, err := sqlparse.BindNamed("SELECT :q AS text, :id::bigint AS id", "postgres", values)
	if err != nil || query != "SELECT $1 AS text, $2::bigint AS id" || args[0] != "' OR TRUE; --" {
		t.Fatal("parameter entered SQL syntax", query, args, err)
	}
	for name, input := range map[string]map[string]any{
		"unknown": {"unknown": "x"}, "object": {"q": map[string]any{"x": 1}}, "array": {"id": []any{1}},
		"invalid-date": {"from": "2026-02-30"}, "invalid-option": {"city": "bj"}, "fractional-id": {"id": 1.5},
		"unsafe-number": {"id": float64(9007199254740992)}, "nan": {"amount": math.NaN()}, "null-required": {"from": nil},
		"timestamp-in-date": {"from": "2026-10-20T00:00:00Z"}, "invalid-boolean": {"active": "no"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := ResolveReportParameters(defs, input, now); e == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	if _, e := ResolveReportParameters([]ReportParameter{{Name: "x", Label: "编号", Type: "integer"}}, map[string]any{"x": json.Number("9223372036854775807")}, now); e != nil {
		t.Fatal(e)
	}
}

func TestReportParameterDefinitionsAndSQL(t *testing.T) {
	definition := ReportParameter{Name: "q", Label: "搜索", Type: "text"}
	query := `SELECT :q::text AS label, ':ignored' AS constant /* :comment */`
	if e := ValidateReportSQL(query, "postgres", []ReportParameter{definition}); e != nil {
		t.Fatal(e)
	}
	for _, defs := range [][]ReportParameter{nil, {definition, definition}, {{Name: "__proto__", Label: "搜索", Type: "text"}}, {{Name: "q", Label: "搜索", Type: "enum"}}, {{Name: "unused", Label: "搜索", Type: "text"}}} {
		if e := ValidateReportSQL(query, "postgres", defs); e == nil {
			t.Fatal("invalid definition accepted", defs)
		}
	}
	if e := ValidateReportSQL("WITH d AS (DELETE FROM inventory RETURNING *) SELECT :q FROM d", "postgres", []ReportParameter{definition}); e == nil {
		t.Fatal("parameterized write accepted")
	}
}

func TestDashboardParameterMapping(t *testing.T) {
	global := []ReportParameter{{Name: "warehouse", Label: "仓库", Type: "integer", Required: true}}
	report := []ReportParameter{{Name: "wid", Label: "仓库编号", Type: "integer", Required: true}}
	bindings := map[string]string{"wid": "warehouse"}
	values, e := dashboardTileParameters(global, bindings, report, map[string]any{"warehouse": int64(6)}, time.Now())
	if e != nil || values["wid"] != int64(6) {
		t.Fatal(values, e)
	}
	if e := validateDashboardParameterBindings(global, nil, report); e == nil {
		t.Fatal("missing required mapping accepted")
	}
	global[0].Type = "text"
	if e := validateDashboardParameterBindings(global, bindings, report); e == nil {
		t.Fatal("incompatible mapping accepted")
	}
}
