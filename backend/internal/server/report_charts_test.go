package server

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAdditionalReportChartKindsPersist(t *testing.T) {
	a := setupAPI(t)
	connection := a.connectionFor(a.developer)
	cases := []struct {
		name, kind, dimension string
		extra                 map[string]any
	}{
		{name: "横向条形", kind: "horizontal_bar", dimension: "category"},
		{name: "面积", kind: "area", dimension: "category"},
		{name: "散点", kind: "scatter", dimension: "weight"},
		{name: "直方", kind: "histogram", dimension: "category"},
		{name: "漏斗", kind: "funnel", dimension: "category"},
		{name: "热力", kind: "heatmap", dimension: "category", extra: map[string]any{"heatmap_y": "period"}},
		{name: "实心饼", kind: "pie", dimension: "category", extra: map[string]any{"pie_style": "solid"}},
		{name: "显式环形", kind: "pie", dimension: "category", extra: map[string]any{"pie_style": "donut"}},
		{name: "历史饼图", kind: "pie", dimension: "category"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a.t = t
			config := map[string]any{"dimension": c.dimension, "metrics": []string{"amount"}}
			for key, value := range c.extra {
				config[key] = value
			}
			created := a.call("POST", "/reports", a.developer, map[string]any{
				"name": c.name, "connection_id": connection, "request_id": testRequestID(), "visibility": "private",
				"sql": "SELECT 'A'::text AS category, '早间'::text AS period, 5 AS weight, 7 AS amount", "chart_type": c.kind,
				"chart_config": config,
			}, 200)
			path := fmt.Sprintf("/reports/%d", int64(created["id"].(float64)))
			reopened := a.call("GET", path, a.developer, nil, 200)
			want, _ := json.Marshal(config)
			got, _ := json.Marshal(reopened["chart_config"])
			if reopened["chart_type"] != c.kind || string(got) != string(want) {
				t.Fatalf("reopened %v/%s, expected %s/%s", reopened["chart_type"], got, c.kind, want)
			}
		})
	}
}
