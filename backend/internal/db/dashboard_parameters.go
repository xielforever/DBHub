package db

import (
	"fmt"
	"time"
)

// Mappings can only connect declared parameters of the same type. Report limits
// remain authoritative and are rechecked after mapping the shared filter value.
func dashboardTileParameters(global []ReportParameter, bindings map[string]string, report []ReportParameter, values map[string]any, now time.Time) (map[string]any, error) {
	if err := validateDashboardParameterBindings(global, bindings, report); err != nil {
		return nil, err
	}
	globals := map[string]ReportParameter{}
	for _, p := range global {
		globals[p.Name] = p
	}
	declared := map[string]ReportParameter{}
	for _, p := range report {
		declared[p.Name] = p
	}
	input := map[string]any{}
	for target, source := range bindings {
		p, ok := declared[target]
		g, exists := globals[source]
		if !ok || !exists || p.Type != g.Type {
			return nil, fmt.Errorf("筛选映射不存在或参数类型不一致：%s", target)
		}
		input[target] = values[source]
	}
	return ResolveReportParameters(report, input, now)
}

func validateDashboardParameterBindings(global []ReportParameter, bindings map[string]string, report []ReportParameter) error {
	globals := map[string]ReportParameter{}
	for _, p := range global {
		globals[p.Name] = p
	}
	declared := map[string]ReportParameter{}
	for _, p := range report {
		declared[p.Name] = p
	}
	for target, source := range bindings {
		p, ok := declared[target]
		g, exists := globals[source]
		if !ok || !exists || p.Type != g.Type {
			return fmt.Errorf("筛选映射不存在或参数类型不一致：%s", target)
		}
		if p.Required && !g.Required && g.Default == nil && g.RelativeDays == nil {
			return fmt.Errorf("必填报表参数 %s 不能映射到没有默认值的可选筛选", p.Label)
		}
	}
	for _, p := range report {
		if p.Required && p.Default == nil && p.RelativeDays == nil && bindings[p.Name] == "" {
			return fmt.Errorf("必填参数 %s 须绑定全局筛选或设置报表默认值", p.Label)
		}
	}
	return nil
}
