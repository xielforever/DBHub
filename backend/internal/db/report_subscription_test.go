package db

import (
	"encoding/json"
	"github.com/user/dbhub/internal/connector"
	"testing"
)

func TestMetricAlertPrecisionAndMissingData(t *testing.T) {
	c := sampleContract()
	c.Schema = []ContractColumn{{Name: "amount", Type: "lossless_numeric"}}
	rule := &MetricAlertRule{Column: "amount", Operator: "gt", Threshold: "9007199254740993.00000"}
	if err := ValidateMetricAlertRule(rule, c); err != nil {
		t.Fatal(err)
	}
	result := &connector.ExecResult{Columns: []string{"amount"}, Rows: [][]any{{json.Number("9007199254740993.00001")}}}
	active, text, err := EvaluateMetricAlert(rule, result)
	if err != nil || !active || text != "9007199254740993.00001" {
		t.Fatal("alert rounded an exact boundary")
	}
	for _, bad := range []*connector.ExecResult{nil, {Columns: result.Columns}, {Columns: result.Columns, Rows: [][]any{{nil}}}, {Columns: result.Columns, Rows: [][]any{{"1"}, {"2"}}}, {Columns: result.Columns, Rows: [][]any{{"NaN"}}}, {Columns: result.Columns, Rows: result.Rows, Truncated: true}} {
		if _, _, err = EvaluateMetricAlert(rule, bad); err == nil {
			t.Fatal("missing or incomplete data reported as normal")
		}
	}
	for _, operator := range []string{"lt", "lte", "gt", "gte", "eq", "ne"} {
		rule.Operator = operator
		if ValidateMetricAlertRule(rule, c) != nil {
			t.Fatal("supported comparison rejected")
		}
	}
	for _, threshold := range []string{"NaN", "1e999999999", "1/3", "Inf"} {
		rule.Threshold = threshold
		if ValidateMetricAlertRule(rule, c) == nil {
			t.Fatal("unbounded threshold accepted")
		}
	}
}
