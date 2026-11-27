package db

import (
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// BusinessReportParameterBinding restricts a report parameter to one approved
// view column, comparison and value domain.
type BusinessReportParameterBinding struct {
	Column   string          `json:"column"`
	Operator string          `json:"operator"`
	Domain   ReportParameter `json:"domain"`
}

var businessReportOperators = map[string]string{"eq": "=", "gte": ">=", "lte": "<="}
var businessReportParameterCasts = map[string]string{"enum": "text", "boolean": "boolean", "integer": "numeric", "decimal": "numeric", "date": "date"}

func validBusinessParameterDomain(p ReportParameter) bool {
	if !p.Required || businessReportParameterCasts[p.Type] == "" {
		return false
	}
	switch p.Type {
	case "integer", "decimal":
		return p.Min != nil && p.Max != nil
	case "date":
		return p.DateMin != "" && p.DateMax != ""
	default:
		return true
	}
}

func businessReportDomainContains(outer, inner ReportParameter) bool {
	if outer.Type != inner.Type || !validBusinessParameterDomain(outer) || !validBusinessParameterDomain(inner) {
		return false
	}
	switch inner.Type {
	case "integer", "decimal":
		return *outer.Min <= *inner.Min && *inner.Max <= *outer.Max
	case "date":
		return outer.DateMin <= inner.DateMin && inner.DateMax <= outer.DateMax
	case "enum":
		for _, option := range inner.Options {
			if _, err := normalizeReportParameter(outer, option.Value); err != nil {
				return false
			}
		}
	}
	return true
}

func validBusinessParameterBindings(bindings []BusinessReportParameterBinding, cols []string) bool {
	defs := make([]ReportParameter, 0, len(bindings))
	for _, binding := range bindings {
		if !slices.Contains(cols, binding.Column) || !validBusinessParameterDomain(binding.Domain) ||
			businessReportOperators[binding.Operator] == "" {
			return false
		}
		if binding.Operator != "eq" && binding.Domain.Type != "integer" && binding.Domain.Type != "decimal" && binding.Domain.Type != "date" {
			return false
		}
		defs = append(defs, binding.Domain)
	}
	return ValidateReportParameters(defs) == nil
}

func businessReportParameters(d BusinessRowColumnDefinition, defs []ReportParameter) ([]BusinessReportParameterBinding, error) {
	reject := fmt.Errorf("%w: 报表参数缺少匹配的批准列、比较方式或允许范围", ErrManagedConflict)
	if err := ValidateReportParameters(defs); err != nil {
		return nil, reject
	}
	var filters []BusinessReportParameterBinding
	for _, parameter := range defs {
		index := slices.IndexFunc(d.ParameterBindings, func(binding BusinessReportParameterBinding) bool {
			return binding.Domain.Name == parameter.Name
		})
		if index < 0 || !businessReportDomainContains(d.ParameterBindings[index].Domain, parameter) {
			return nil, reject
		}
		binding := d.ParameterBindings[index]
		binding.Domain = parameter
		filters = append(filters, binding)
	}
	for _, filter := range filters {
		if filter.Operator == "eq" {
			continue
		}
		pair := 0
		for _, other := range filters {
			if other.Column == filter.Column && other.Domain.Type == filter.Domain.Type && other.Operator != "eq" {
				pair++
			}
		}
		opposite := "lte"
		if filter.Operator == "lte" {
			opposite = "gte"
		}
		if pair != 2 || !slices.ContainsFunc(filters, func(other BusinessReportParameterBinding) bool {
			return other.Column == filter.Column && other.Domain.Type == filter.Domain.Type && other.Operator == opposite
		}) {
			return nil, reject
		}
	}
	return filters, nil
}

func businessReportPredicates(filters []BusinessReportParameterBinding, executable bool) []string {
	parts := make([]string, 0, len(filters))
	for index, filter := range filters {
		cast := businessReportParameterCasts[filter.Domain.Type]
		column := pgx.Identifier{filter.Column}
		parameter := ":" + filter.Domain.Name + "::" + cast
		if executable {
			column = pgx.Identifier{"v", filter.Column}
			// $1 holds the organization tuples in the existing read plan.
			parameter = "$" + strconv.Itoa(index+2) + "::text::" + cast
		}
		left := column.Sanitize() + "::" + cast
		if cast == "text" {
			left = "(" + left + ` COLLATE "C")`
		}
		parts = append(parts, left+" "+businessReportOperators[filter.Operator]+" "+parameter)
	}
	return parts
}

// BindReportParameters adds only validated data values to the fixed read plan.
func (p *BusinessOutputPlan) BindReportParameters(params map[string]any) ([]any, error) {
	filters := p.ReportTarget.Filters
	if len(filters) == 0 {
		return p.Args, nil
	}
	args := append([]any{}, p.Args...)
	for _, filter := range filters {
		value, err := normalizeReportParameter(filter.Domain, params[filter.Domain.Name])
		if err != nil {
			return nil, err
		}
		args = append(args, fmt.Sprint(value))
	}
	for i, lower := range filters {
		if lower.Operator != "gte" {
			continue
		}
		j := slices.IndexFunc(filters, func(upper BusinessReportParameterBinding) bool {
			return upper.Column == lower.Column && upper.Domain.Type == lower.Domain.Type && upper.Operator == "lte"
		})
		if j < 0 {
			return nil, ErrBusinessReadDenied
		}
		minimum, maximum := args[len(p.Args)+i].(string), args[len(p.Args)+j].(string)
		order := strings.Compare(minimum, maximum)
		if lower.Domain.Type != "date" {
			minimumNumber, _ := new(big.Rat).SetString(minimum)
			maximumNumber, _ := new(big.Rat).SetString(maximum)
			order = minimumNumber.Cmp(maximumNumber)
		}
		if order > 0 {
			return nil, fmt.Errorf("%s不能晚于或大于%s", lower.Domain.Label, filters[j].Domain.Label)
		}
	}
	return args, nil
}
