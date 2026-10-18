package db

import (
	"encoding/json"
	"github.com/user/dbhub/internal/connector"
	"testing"
	"time"
)

func sampleContract() *DataContract {
	return &DataContract{SemanticKey: "stock", Meaning: "可售库存数量", Schema: []ContractColumn{{"amount", "lossless_numeric", false}, {"watermark", "timestamp_with_timezone", true}}, ChartType: "metric", Metrics: []string{"amount"}, Dimensions: []string{}, Unit: "item_count", Timezone: "UTC", ScopeKey: "warehouse_a", ScopeDescription: "仓库A可售库存，不含在途", TimeWindow: json.RawMessage(`{"kind":"current_snapshot"}`), ComparisonKey: "stock", Freshness: ContractFreshness{"watermark_column", "watermark", 900, 60}}
}
func TestContractCompatibilityBoundary(t *testing.T) {
	c := sampleContract()
	if e := ValidateDataContract(c, "metric"); e != nil {
		t.Fatal(e)
	}
	base := approvedContract(c, 1, 10, 20, "inventory")
	if approvedContract(c, 1, 10, 20, "inventory").Hash != base.Hash {
		t.Fatal("unstable contract")
	}
	for name, mutate := range map[string]func(*DataContract){"unit": func(c *DataContract) { c.Unit = "thousand_items" }, "meaning": func(c *DataContract) { c.Meaning = "含在途数量" }, "scope": func(c *DataContract) { c.ScopeDescription = "全部仓库" }, "timezone": func(c *DataContract) { c.Timezone = "Asia/Shanghai" }, "schema": func(c *DataContract) { c.Schema[0].Nullable = true }, "freshness": func(c *DataContract) { c.Freshness.MaxAge = 600 }, "window": func(c *DataContract) { c.TimeWindow = json.RawMessage(`{"kind":"rolling_days","days":7}`) }} {
		t.Run(name, func(t *testing.T) {
			next := sampleContract()
			mutate(next)
			if approvedContract(next, 1, 10, 20, "inventory").Hash == base.Hash {
				t.Fatal("breaking change was compatible")
			}
		})
	}
	for _, next := range []*ApprovedContract{approvedContract(c, 2, 10, 20, "inventory"), approvedContract(c, 1, 11, 20, "inventory"), approvedContract(c, 1, 10, 21, "inventory"), approvedContract(c, 1, 10, 20, "other")} {
		if next.Hash == base.Hash {
			t.Fatal("execution boundary missing from hash")
		}
	}
}
func TestContractResultsAndWatermarks(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	a := approvedContract(sampleContract(), 1, 10, 20, "inventory")
	result := func(rows [][]any) *connector.ExecResult {
		return &connector.ExecResult{Kind: "query", Columns: []string{"amount", "watermark"}, Rows: rows}
	}
	valid := result([][]any{{"9007199254740993.00001", now.Add(-time.Minute)}})
	out, e := ValidateContractResult(a, valid, now)
	if e != nil || out.Freshness != "fresh" || out.DataAsOf == nil {
		t.Fatal(out, e)
	}
	if valid.Rows[0][0] != "9007199254740993.00001" {
		t.Fatal("exact numeric changed")
	}
	for name, rows := range map[string][][]any{"zero": {}, "missing": {{"1", nil}}} {
		t.Run(name, func(t *testing.T) {
			out, e := ValidateContractResult(a, result(rows), now)
			if e != nil || out.Freshness != "unknown" || out.DataAsOf != nil {
				t.Fatal("invented a watermark", out, e)
			}
		})
	}
	stale, e := ValidateContractResult(a, result([][]any{{"1", now.Add(-time.Hour)}}), now)
	if e != nil || stale.Freshness != "stale" {
		t.Fatal(stale, e)
	}
	for name, r := range map[string]*connector.ExecResult{"future": result([][]any{{"1", now.Add(61 * time.Second)}}), "mixed": result([][]any{{"1", now}, {"2", now.Add(-time.Second)}}), "invalid_time": result([][]any{{"1", "2026-10-18 09:25"}}), "bad_numeric": result([][]any{{"not a number", now}}), "null_metric": result([][]any{{nil, now}}), "schema": {Columns: []string{"watermark", "amount"}, Rows: [][]any{{now, "1"}}}, "truncated": {Columns: []string{"amount", "watermark"}, Rows: [][]any{{"1", now}}, Truncated: true}} {
		t.Run(name, func(t *testing.T) {
			if _, e := ValidateContractResult(a, r, now); e == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func TestContractChartMappingCannotDriftWithSameHash(t *testing.T) {
	c := sampleContract()
	if e := ValidateDataContract(c, "metric"); e != nil {
		t.Fatal(e)
	}
	if e := ValidateContractMapping(c, json.RawMessage(`{"metrics":["amount"]}`)); e != nil {
		t.Fatal(e)
	}
	if e := ValidateContractMapping(c, json.RawMessage(`{"metrics":["unapproved_metric"]}`)); e == nil {
		t.Fatal("chart changed displayed metric without changing contract")
	}
}

func TestPresentationKeepsContractBoundary(t *testing.T) {
	c := sampleContract()
	before := approvedContract(c, 1, 10, 20, "inventory").Hash
	if e := ValidateContractMapping(c, json.RawMessage(`{"metrics":["amount"],"display":{"labels":{"amount":"库存金额"},"fraction_digits":2}}`)); e != nil {
		t.Fatal(e)
	}
	if approvedContract(c, 1, 10, 20, "inventory").Hash != before {
		t.Fatal("presentation changed the approved data contract")
	}
	if e := ValidateContractMapping(c, json.RawMessage(`{"metrics":["amount"],"display":{"labels":{"other":"虚构字段"}}}`)); e == nil {
		t.Fatal("presentation referenced a field outside the approved schema")
	}
	if e := ValidateContractMapping(c, json.RawMessage(`{"metrics":["amount"],"display":{"labels":{"amount":"watermark"}}}`)); e == nil {
		t.Fatal("display name collided with an original field")
	}
}
