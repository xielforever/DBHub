package reportapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/dbhub/internal/db"
)

func TestReportFieldsRejectAmbiguousOrUnboundedConfiguration(t *testing.T) {
	base := func() *db.Report {
		return &db.Report{Name: "库存", SQLText: "SELECT category,amount FROM inventory", Visibility: "private", ChartType: "bar", ChartConfig: json.RawMessage(`{"dimension":"category","metrics":["amount"]}`)}
	}
	cases := map[string]func(*db.Report){
		"blank-name":           func(r *db.Report) { r.Name = "  " },
		"overlong-description": func(r *db.Report) { r.Description = strings.Repeat("字", 513) },
		"blank-sql":            func(r *db.Report) { r.SQLText = "\n" },
		"nul":                  func(r *db.Report) { r.Name = "bad\x00name" },
		"null-config":          func(r *db.Report) { r.ChartConfig = json.RawMessage(`null`) },
		"non-object-config":    func(r *db.Report) { r.ChartConfig = json.RawMessage(`[]`) },
		"legacy-aggregation": func(r *db.Report) {
			r.ChartConfig = json.RawMessage(`{"dimension":"category","metrics":["amount"],"aggregation":"sum"}`)
		},
		"duplicate-metrics": func(r *db.Report) {
			r.ChartConfig = json.RawMessage(`{"dimension":"category","metrics":["amount","amount"]}`)
		},
		"no-dimension": func(r *db.Report) { r.ChartConfig = json.RawMessage(`{"metrics":["amount"]}`) },
		"display-unknown-field": func(r *db.Report) {
			r.ChartConfig = json.RawMessage(`{"dimension":"category","metrics":["amount"],"display":{"sql":"SELECT 2"}}`)
		},
		"display-bad-places": func(r *db.Report) {
			r.ChartConfig = json.RawMessage(`{"dimension":"category","metrics":["amount"],"display":{"fraction_digits":99}}`)
		},
		"display-empty-label": func(r *db.Report) {
			r.ChartConfig = json.RawMessage(`{"dimension":"category","metrics":["amount"],"display":{"labels":{"amount":" "}}}`)
		},
		"display-duplicate-label": func(r *db.Report) {
			r.ChartConfig = json.RawMessage(`{"dimension":"category","metrics":["amount"],"display":{"labels":{"amount":"库存","category":"库存"}}}`)
		},
		"metric-multiple-columns": func(r *db.Report) {
			r.ChartType = "metric"
			r.ChartConfig = json.RawMessage(`{"metrics":["amount","total"]}`)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			rep := base()
			change(rep)
			if validateFields(rep) == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
	if err := validateFields(base()); err != nil {
		t.Fatal(err)
	}
	valid := base()
	valid.ChartConfig = json.RawMessage(`{"dimension":"category","metrics":["amount"],"display":{"labels":{"amount":"库存金额"},"grouping":true,"fraction_digits":2}}`)
	if err := validateFields(valid); err != nil {
		t.Fatal(err)
	}
}
