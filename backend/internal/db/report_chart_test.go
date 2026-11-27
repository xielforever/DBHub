package db

import "testing"

func TestChartAnalysisMappings(t *testing.T) {
	cases := []struct {
		kind   string
		config ReportChartConfig
		valid  bool
	}{
		{"pivot", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, Pivot: &ReportPivot{Column: "channel"}}, true},
		{"pivot", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, Pivot: &ReportPivot{Column: "city"}}, false},
		{"dual_axis", ReportChartConfig{Metrics: []string{"amount", "orders"}, DualAxis: &ReportDualAxis{RightMetrics: []string{"orders"}, LeftUnit: "人民币元", RightUnit: "订单数"}}, true},
		{"dual_axis", ReportChartConfig{Metrics: []string{"amount", "orders"}, DualAxis: &ReportDualAxis{RightMetrics: []string{"missing"}, LeftUnit: "元", RightUnit: "单"}}, false},
		{"bar", ReportChartConfig{DrillThrough: &ReportDrillThrough{ReportID: 1, Bindings: map[string]string{"q": "order_no"}}}, true},
		{"bar", ReportChartConfig{DrillThrough: &ReportDrillThrough{ReportID: 1, Bindings: map[string]string{"__proto__": "order_no"}}}, false},
		{"stacked_bar", ReportChartConfig{Metrics: []string{"amount"}}, false},
		{"pie", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}}, true},
		{"pie", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, PieStyle: "solid"}, true},
		{"pie", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, PieStyle: "donut"}, true},
		{"pie", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, PieStyle: "other"}, false},
		{"area", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, PieStyle: "solid"}, false},
		{"scatter", ReportChartConfig{Dimension: "amount", Metrics: []string{"weight"}}, true},
		{"scatter", ReportChartConfig{Dimension: "amount", Metrics: []string{"weight", "height"}}, false},
		{"scatter", ReportChartConfig{Dimension: "amount", Metrics: []string{"amount"}}, false},
		{"histogram", ReportChartConfig{Dimension: "bucket", Metrics: []string{"count"}}, true},
		{"histogram", ReportChartConfig{Dimension: "bucket"}, false},
		{"funnel", ReportChartConfig{Dimension: "stage", Metrics: []string{"count"}}, true},
		{"heatmap", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, HeatmapY: "period"}, true},
		{"heatmap", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}}, false},
		{"heatmap", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, HeatmapY: "city"}, false},
		{"heatmap", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, HeatmapY: "amount"}, false},
		{"bar", ReportChartConfig{Dimension: "city", Metrics: []string{"amount"}, HeatmapY: "period"}, false},
	}
	for _, item := range cases {
		if err := ValidateReportChartExtras(item.kind, item.config); (err == nil) != item.valid {
			t.Fatalf("%s valid=%v: %v", item.kind, item.valid, err)
		}
	}
}
