package db

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/user/dbhub/internal/connector"
)

func TestContractDecimalResourceBounds(t *testing.T) {
	for _, text := range []string{"1e999999999", "1e-999999999", "NaN", "Inf", "1/3", "0x10", strings.Repeat("1", 4097)} {
		if _, ok := contractDecimal(text); ok {
			t.Fatalf("unbounded or non-decimal value accepted: %.40s", text)
		}
	}
	for _, text := range []string{"9007199254740993.00001", "1e1000", "1e-1000", "-0.000001", ".5", "1."} {
		if _, ok := contractDecimal(text); !ok {
			t.Fatalf("bounded decimal rejected: %s", text)
		}
	}
}
func TestMetricChecksExactAndFailClosed(t *testing.T) {
	c := sampleContract()
	c.Freshness = ContractFreshness{Mode: "unverified"}
	base := approvedContract(c, 1, 10, 20, "inventory").Hash
	c.Checks = []MetricCheck{{ID: "total", Label: "精确合计", Kind: "sum_equals", Column: "amount", Expected: "9007199254740993.00003"}, {ID: "rows", Label: "两行", Kind: "row_count", Expected: "2"}, {ID: "nonnegative", Label: "金额非负", Kind: "range", Column: "amount", Min: "0"}}
	if err := ValidateDataContract(c, "metric"); err != nil {
		t.Fatal(err)
	}
	if approvedContract(c, 1, 10, 20, "inventory").Hash == base {
		t.Fatal("checks absent from immutable contract hash")
	}
	valid := &connector.ExecResult{Columns: []string{"amount", "watermark"}, Rows: [][]any{{"9007199254740993.00001", nil}, {"0.00002", nil}}}
	approved := approvedContract(c, 1, 10, 20, "inventory")
	if _, err := ValidateContractResult(approved, valid, time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]*connector.ExecResult{"empty": {Columns: valid.Columns}, "null": {Columns: valid.Columns, Rows: [][]any{{nil, nil}, {"0", nil}}}, "wrong-total": {Columns: valid.Columns, Rows: [][]any{{"9007199254740993.00002", nil}, {"0.00002", nil}}}, "truncated": {Columns: valid.Columns, Rows: valid.Rows, Truncated: true}} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateContractResult(approved, result, time.Now()); err == nil {
				t.Fatal("failed check delivered")
			}
		})
	}
	c.Checks = []MetricCheck{{ID: "zero", Label: "账实差异为零", Kind: "all_equal", Column: "amount", Expected: "0", Tolerance: "0.01"}}
	if !RunMetricChecks(c, &connector.ExecResult{Columns: []string{"amount"}, Rows: [][]any{{"-0.01"}, {"0.01"}}})[0].Passed {
		t.Fatal("inclusive tolerance rejected")
	}
	if RunMetricChecks(c, &connector.ExecResult{Columns: []string{"amount"}, Rows: [][]any{{"0.01000001"}}})[0].Passed {
		t.Fatal("rounded away regression")
	}
	c.Checks = nil
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "metric_checks") {
		t.Fatal("legacy contracts gained a field and changed their hashes")
	}
}
func TestContractSchemaImpactDiagnostics(t *testing.T) {
	c := sampleContract()
	c.Freshness = ContractFreshness{Mode: "unverified"}
	result := &connector.ExecResult{Columns: []string{"renamed", "watermark", "new_field"}, Rows: [][]any{{"1", nil, "added"}}}
	out := InspectContractResult(c, result)
	if out.Passed || len(out.Issues) < 2 || len(out.Expected) != 2 || len(out.Actual) != 3 {
		t.Fatal(out)
	}
	result = &connector.ExecResult{Columns: []string{"amount", "watermark"}, Rows: [][]any{{"1e999999999", nil}}}
	if InspectContractResult(c, result).Passed {
		t.Fatal("hostile numeric text accepted by preview")
	}
}

func TestMetricCheckExplicitEmptyPolicy(t *testing.T) {
	c := sampleContract()
	c.Checks = []MetricCheck{{ID: "owner", Label: "个人范围", Kind: "all_equal", Column: "amount", Expected: "40"}}
	empty := &connector.ExecResult{Columns: []string{"amount"}}
	if RunMetricChecks(c, empty)[0].Passed {
		t.Fatal("default empty result accepted")
	}
	oldHash := approvedContract(c, 1, 10, 20, "inventory").Hash
	c.Checks[0].AllowEmpty = true
	if !RunMetricChecks(c, empty)[0].Passed {
		t.Fatal("explicit approved empty policy ignored")
	}
	if oldHash == approvedContract(c, 1, 10, 20, "inventory").Hash {
		t.Fatal("empty policy not bound to approval")
	}
	for _, bad := range []*connector.ExecResult{{Columns: []string{"other"}}, {Columns: empty.Columns, Truncated: true}, {Columns: empty.Columns, Rows: [][]any{{nil}}}, {Columns: empty.Columns, Rows: [][]any{{"41"}}}} {
		if RunMetricChecks(c, bad)[0].Passed {
			t.Fatal("empty exception bypassed a different check boundary")
		}
	}
	c.Checks[0].Kind = "sum_equals"
	if validateMetricChecks(c) == nil {
		t.Fatal("empty exception accepted for a total baseline")
	}
}
