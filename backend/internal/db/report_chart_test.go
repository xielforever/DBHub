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
	}
	for _, item := range cases {
		if err := ValidateReportChartExtras(item.kind, item.config); (err == nil) != item.valid {
			t.Fatalf("%s valid=%v: %v", item.kind, item.valid, err)
		}
	}
}
