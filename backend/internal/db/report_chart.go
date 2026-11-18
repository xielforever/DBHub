package db

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Chart mappings are frozen with the report. They rearrange approved rows;
// neither the browser nor a drill link may introduce a query or credential.
type ReportChartConfig struct {
	Dimension    string              `json:"dimension,omitempty"`
	Metrics      []string            `json:"metrics,omitempty"`
	Display      *ReportDisplay      `json:"display,omitempty"`
	Pivot        *ReportPivot        `json:"pivot,omitempty"`
	DualAxis     *ReportDualAxis     `json:"dual_axis,omitempty"`
	DrillThrough *ReportDrillThrough `json:"drillthrough,omitempty"`
}
type ReportPivot struct {
	Column string `json:"column"`
}
type ReportDualAxis struct {
	RightMetrics []string `json:"right_metrics"`
	LeftUnit     string   `json:"left_unit"`
	RightUnit    string   `json:"right_unit"`
}
type ReportDrillThrough struct {
	ReportID int64             `json:"report_id"`
	Bindings map[string]string `json:"bindings"`
}

func ValidateReportChartExtras(kind string, c ReportChartConfig) error {
	bad := func(message string) error { return fmt.Errorf("%s", message) }
	if kind == "pivot" {
		if c.Pivot == nil || strings.TrimSpace(c.Pivot.Column) == "" || utf8.RuneCountInString(c.Pivot.Column) > 128 || strings.ContainsRune(c.Pivot.Column, 0) || c.Pivot.Column == c.Dimension || len(c.Metrics) != 1 || c.Metrics[0] == c.Pivot.Column || c.Metrics[0] == c.Dimension {
			return bad("透视表须选择不同的行维度、列维度和一个指标")
		}
	} else if c.Pivot != nil {
		return bad("列维度设置仅适用于透视表")
	}
	if kind == "dual_axis" {
		if c.DualAxis == nil || len(c.DualAxis.RightMetrics) == 0 || len(c.DualAxis.RightMetrics) >= len(c.Metrics) || strings.TrimSpace(c.DualAxis.LeftUnit) == "" || strings.TrimSpace(c.DualAxis.RightUnit) == "" || utf8.RuneCountInString(c.DualAxis.LeftUnit) > 32 || utf8.RuneCountInString(c.DualAxis.RightUnit) > 32 {
			return bad("双轴图须指定左右轴单位，并分别分配指标")
		}
		seen := map[string]bool{}
		for _, metric := range c.DualAxis.RightMetrics {
			found := false
			for _, name := range c.Metrics {
				if metric == name {
					found = true
				}
			}
			if !found || seen[metric] {
				return bad("右轴指标须来自已选择指标且不能重复")
			}
			seen[metric] = true
		}
	} else if c.DualAxis != nil {
		return bad("左右轴设置仅适用于双轴图")
	}
	if kind == "stacked_bar" && len(c.Metrics) < 2 {
		return bad("堆叠图须选择至少两个同单位指标")
	}
	if d := c.DrillThrough; d != nil {
		if d.ReportID < 1 || len(d.Bindings) < 1 || len(d.Bindings) > 10 {
			return bad("下钻须指定正式报表和1至10个参数映射")
		}
		for parameter, column := range d.Bindings {
			if err := ValidateReportParameters([]ReportParameter{{Name: parameter, Label: parameter, Type: "text"}}); err != nil {
				return bad("下钻参数名称无效")
			}
			if strings.TrimSpace(column) == "" || utf8.RuneCountInString(column) > 128 || strings.ContainsRune(column, 0) {
				return bad("下钻源字段无效")
			}
		}
	}
	return nil
}
