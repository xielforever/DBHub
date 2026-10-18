package db

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Display settings belong to the approved report content, not its data contract.
// They never change SQL values, units, aggregation or execution identity.
type ReportDisplay struct {
	Labels         map[string]string `json:"labels,omitempty"`
	Grouping       *bool             `json:"grouping,omitempty"`
	FractionDigits *int              `json:"fraction_digits,omitempty"`
}

func ValidateReportDisplay(display *ReportDisplay) error {
	if display == nil {
		return nil
	}
	invalid := func() error {
		return fmt.Errorf("%w: 展示名称最多32项、每项1至64字，小数位须为0至12", ErrManagedInput)
	}
	if len(display.Labels) > 32 || (display.FractionDigits != nil && (*display.FractionDigits < 0 || *display.FractionDigits > 12)) {
		return invalid()
	}
	seen := map[string]bool{}
	for column, label := range display.Labels {
		if strings.TrimSpace(column) == "" || utf8.RuneCountInString(column) > 128 || strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > 64 || strings.IndexFunc(column+label, unicode.IsControl) >= 0 {
			return invalid()
		}
		if seen[strings.TrimSpace(label)] {
			return fmt.Errorf("%w: 展示名称不能重复，以免混淆字段", ErrManagedInput)
		}
		seen[strings.TrimSpace(label)] = true
	}
	return nil
}

func validateContractDisplay(c *DataContract, config json.RawMessage) error {
	if len(config) == 0 {
		return nil
	}
	var cfg struct {
		Display *ReportDisplay `json:"display"`
	}
	if json.Unmarshal(config, &cfg) != nil {
		return ErrManagedInput
	}
	if err := ValidateReportDisplay(cfg.Display); err != nil {
		return err
	}
	if c != nil && cfg.Display != nil {
		columns := map[string]bool{}
		for _, column := range c.Schema {
			columns[column.Name] = true
		}
		for column := range cfg.Display.Labels {
			if !columns[column] {
				return fmt.Errorf("%w: 展示名称引用了数据契约中不存在的字段", ErrManagedInput)
			}
		}
		seen := map[string]bool{}
		for _, column := range c.Schema {
			label := column.Name
			if value, ok := cfg.Display.Labels[column.Name]; ok {
				label = strings.TrimSpace(value)
			}
			if seen[label] {
				return fmt.Errorf("%w: 展示名称与其他字段重名，请使用不同的业务名称", ErrManagedInput)
			}
			seen[label] = true
		}
	}
	return nil
}
