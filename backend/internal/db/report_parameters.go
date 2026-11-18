package db

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/user/dbhub/internal/sqlparse"
)

// ReportParameter is part of the immutable approved definition, never an SQL
// fragment. Defaults and limits are validated again at each execution boundary.
type ReportParameter struct {
	Name         string                  `json:"name"`
	Label        string                  `json:"label"`
	Type         string                  `json:"type"`
	Required     bool                    `json:"required,omitempty"`
	Default      any                     `json:"default,omitempty"`
	RelativeDays *int                    `json:"default_relative_days,omitempty"`
	Timezone     string                  `json:"timezone,omitempty"`
	Options      []ReportParameterOption `json:"options,omitempty"`
	Min          *float64                `json:"min,omitempty"`
	Max          *float64                `json:"max,omitempty"`
	DateMin      string                  `json:"date_min,omitempty"`
	DateMax      string                  `json:"date_max,omitempty"`
	MaxLength    int                     `json:"max_length,omitempty"`
}
type ReportParameterOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

var reportParameterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]{0,47}$`)
var decimalParameter = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$`)

// Integer bindings stay native for the driver. Frozen values delivered to JSON
// use exact text so large identifiers cannot be rounded by a browser or Excel.
func ReportParameterDisplayValues(defs []ReportParameter, values map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range values {
		out[key] = value
	}
	for _, definition := range defs {
		if definition.Type == "integer" && out[definition.Name] != nil {
			out[definition.Name] = fmt.Sprint(out[definition.Name])
		}
	}
	return out
}

func reportParametersJSON(defs []ReportParameter) string {
	if len(defs) == 0 {
		return "[]"
	}
	raw, _ := json.Marshal(defs)
	return string(raw)
}

func ValidateReportParameters(defs []ReportParameter) error {
	if len(defs) > 20 {
		return fmt.Errorf("最多定义20个报表参数")
	}
	seen := map[string]bool{}
	for _, p := range defs {
		if !reportParameterName.MatchString(p.Name) || seen[p.Name] || strings.HasPrefix(p.Name, "__") || p.Name == "__proto__" || p.Name == "constructor" || p.Name == "prototype" {
			return fmt.Errorf("参数名称无效或重复：%s", p.Name)
		}
		seen[p.Name] = true
		if strings.TrimSpace(p.Label) == "" || utf8.RuneCountInString(p.Label) > 80 || strings.ContainsRune(p.Label, 0) {
			return fmt.Errorf("参数显示名称必填，且最多80字")
		}
		switch p.Type {
		case "text", "integer", "decimal", "date", "datetime", "boolean", "enum":
		default:
			return fmt.Errorf("参数 %s 的类型无效", p.Label)
		}
		if p.MaxLength < 0 || p.MaxLength > 4096 {
			return fmt.Errorf("参数最大长度须介于1至4096")
		}
		if p.Min != nil && (math.IsNaN(*p.Min) || math.IsInf(*p.Min, 0)) || p.Max != nil && (math.IsNaN(*p.Max) || math.IsInf(*p.Max, 0)) || p.Min != nil && p.Max != nil && *p.Min > *p.Max {
			return fmt.Errorf("参数数值范围无效")
		}
		if (p.Min != nil || p.Max != nil) && p.Type != "integer" && p.Type != "decimal" {
			return fmt.Errorf("只有数字参数可以设置数值范围")
		}
		if p.DateMin != "" || p.DateMax != "" {
			if p.Type != "date" || p.DateMin != "" && !validReportDate(p.DateMin) ||
				p.DateMax != "" && !validReportDate(p.DateMax) || p.DateMin != "" && p.DateMax != "" && p.DateMin > p.DateMax {
				return fmt.Errorf("日期参数范围无效")
			}
		}
		if p.RelativeDays != nil && (p.Type != "date" || p.Default != nil || *p.RelativeDays < -36500 || *p.RelativeDays > 36500) {
			return fmt.Errorf("相对日期默认值无效，不能同时设置固定默认值")
		}
		if p.Timezone != "" {
			if _, err := time.LoadLocation(p.Timezone); err != nil {
				return fmt.Errorf("参数时区无效")
			}
		}
		if p.Type == "enum" {
			if len(p.Options) == 0 || len(p.Options) > 100 {
				return fmt.Errorf("枚举参数须有1至100个选项")
			}
			options := map[string]bool{}
			for _, option := range p.Options {
				if option.Value == "" || len(option.Value) > 1024 || options[option.Value] || strings.TrimSpace(option.Label) == "" || len([]rune(option.Label)) > 80 || strings.ContainsRune(option.Value+option.Label, 0) {
					return fmt.Errorf("枚举选项无效或重复")
				}
				options[option.Value] = true
			}
		} else if len(p.Options) != 0 {
			return fmt.Errorf("只有枚举参数可以定义选项")
		}
		if p.Default != nil {
			if _, err := normalizeReportParameter(p, p.Default); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResolveReportParameters rejects undeclared keys and compound values. Optional
// missing values become SQL NULL, rather than changing the query or its predicate.
func ResolveReportParameters(defs []ReportParameter, input map[string]any, now time.Time) (map[string]any, error) {
	if err := ValidateReportParameters(defs); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, p := range defs {
		known[p.Name] = true
	}
	for name := range input {
		if !known[name] {
			return nil, fmt.Errorf("未知报表参数：%s", name)
		}
	}
	values := map[string]any{}
	for _, p := range defs {
		v, exists := input[p.Name]
		if !exists {
			v = p.Default
			if p.RelativeDays != nil {
				zone := time.UTC
				if p.Timezone != "" {
					zone, _ = time.LoadLocation(p.Timezone)
				}
				v = now.In(zone).AddDate(0, 0, *p.RelativeDays).Format("2006-01-02")
			}
		}
		if v == nil || v == "" && p.Type != "text" {
			if p.Required {
				return nil, fmt.Errorf("请填写%s", p.Label)
			}
			values[p.Name] = nil
			continue
		}
		if p.Required && p.Type == "text" && strings.TrimSpace(fmt.Sprint(v)) == "" {
			return nil, fmt.Errorf("请填写%s", p.Label)
		}
		var err error
		values[p.Name], err = normalizeReportParameter(p, v)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func validReportDate(value string) bool {
	date, err := time.Parse("2006-01-02", value)
	return err == nil && date.Year() >= 1 && date.Format("2006-01-02") == value
}

func normalizeReportParameter(p ReportParameter, v any) (any, error) {
	bad := func() (any, error) { return nil, fmt.Errorf("%s的值、格式或范围无效", p.Label) }
	if v == nil {
		return bad()
	}
	var result any
	switch p.Type {
	case "text", "enum", "date", "datetime":
		s, ok := v.(string)
		if !ok || strings.ContainsRune(s, 0) {
			return bad()
		}
		limit := p.MaxLength
		if limit == 0 {
			limit = 1024
		}
		if utf8.RuneCountInString(s) > limit {
			return bad()
		}
		switch p.Type {
		case "enum":
			found := false
			for _, option := range p.Options {
				found = found || option.Value == s
			}
			if !found {
				return bad()
			}
		case "date":
			date, err := time.Parse("2006-01-02", s)
			if err != nil || date.Format("2006-01-02") != s ||
				p.DateMin != "" && s < p.DateMin || p.DateMax != "" && s > p.DateMax {
				return bad()
			}
		case "datetime":
			date, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return bad()
			}
			s = date.UTC().Format(time.RFC3339Nano)
		}
		result = s
	case "boolean":
		switch value := v.(type) {
		case bool:
			result = value
		case string:
			if value == "true" {
				result = true
			} else if value == "false" {
				result = false
			} else {
				return bad()
			}
		default:
			return bad()
		}
	case "integer", "decimal":
		var text string
		switch value := v.(type) {
		case string:
			text = value
		case json.Number:
			text = value.String()
		case int:
			text = strconv.Itoa(value)
		case int64:
			text = strconv.FormatInt(value, 10)
		case float64:
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 9007199254740991 {
				return bad()
			}
			text = strconv.FormatFloat(value, 'f', -1, 64)
		default:
			return bad()
		}
		if len(text) > 128 || !decimalParameter.MatchString(text) {
			return bad()
		}
		if p.Type == "integer" {
			n, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				return bad()
			}
			result = n
		} else {
			result = text
		}
		number, ok := new(big.Rat).SetString(text)
		if !ok {
			return bad()
		}
		for _, bound := range []struct {
			value *float64
			lower bool
		}{{p.Min, true}, {p.Max, false}} {
			if bound.value == nil {
				continue
			}
			limit, _ := new(big.Rat).SetString(strconv.FormatFloat(*bound.value, 'f', -1, 64))
			if bound.lower && number.Cmp(limit) < 0 || !bound.lower && number.Cmp(limit) > 0 {
				return bad()
			}
		}
	default:
		return bad()
	}
	return result, nil
}

// ValidateReportSQL checks the complete placeholder contract before saving.
// Sample NULLs validate syntax without requiring viewer input during publication.
func ValidateReportSQL(query, dialect string, defs []ReportParameter) error {
	if err := ValidateReportParameters(defs); err != nil {
		return err
	}
	names, err := sqlparse.NamedParameters(query, dialect)
	if err != nil {
		return err
	}
	values := map[string]any{}
	for _, p := range defs {
		values[p.Name] = nil
	}
	if len(names) != len(defs) {
		return fmt.Errorf("SQL 命名参数须与参数定义一一对应")
	}
	for _, name := range names {
		if _, ok := values[name]; !ok {
			return fmt.Errorf("SQL 参数 %s 未定义", name)
		}
	}
	return sqlparse.ValidateReport(query, dialect, values)
}
