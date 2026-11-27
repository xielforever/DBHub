package db

import "fmt"

// Export permission and its result bound are approved with the report version.
// Legacy versions have no policy and cannot gain a larger read through an export.
type ReportExportPolicy struct {
	Enabled           bool   `json:"enabled"`
	MaxRows           int    `json:"max_rows"`
	PageSizeParameter string `json:"page_size_parameter,omitempty"`
	OffsetParameter   string `json:"offset_parameter,omitempty"`
}

func ValidateReportExportParameters(p *ReportExportPolicy, defs []ReportParameter) error {
	if p == nil {
		return nil
	}
	for _, binding := range []struct {
		name  string
		value int64
	}{{p.PageSizeParameter, int64(p.MaxRows) + 1}, {p.OffsetParameter, 0}} {
		if binding.name == "" {
			continue
		}
		found := false
		for _, parameter := range defs {
			if parameter.Name == binding.name {
				found = true
				if parameter.Type != "integer" {
					return fmt.Errorf("导出分页须绑定整数参数")
				}
				if _, err := normalizeReportParameter(parameter, binding.value); err != nil {
					return fmt.Errorf("导出分页须允许最大导出行数加1，以检测结果是否完整：%w", err)
				}
			}
		}
		if !found {
			return fmt.Errorf("导出分页参数未声明：%s", binding.name)
		}
	}
	if p.PageSizeParameter != "" && p.PageSizeParameter == p.OffsetParameter {
		return fmt.Errorf("导出页大小与偏移量不能绑定同一参数")
	}
	return nil
}

func ValidateReportExportPolicy(p *ReportExportPolicy) error {
	if p != nil && (!p.Enabled || p.MaxRows < 1 || p.MaxRows > 100000) {
		return fmt.Errorf("导出策略须明确启用，且最大行数介于1至100000")
	}
	return nil
}
