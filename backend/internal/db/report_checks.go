package db

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/connector"
)

// Decimal parsing is bounded before big.Rat sees an exponent or a large mantissa.
// In particular, result text cannot request unbounded 10^exponent allocation.
var contractDecimalPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE]([+-]?[0-9]+))?$`)
var contractCheckID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func contractDecimal(value any) (*big.Rat, bool) {
	text := fmt.Sprint(value)
	if len(text) > 4096 {
		return nil, false
	}
	match := contractDecimalPattern.FindStringSubmatch(text)
	if match == nil {
		return nil, false
	}
	if match[1] != "" {
		exponent, err := strconv.Atoi(match[1])
		if err != nil || exponent < -1000 || exponent > 1000 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(text)
}

type MetricCheck struct {
	AllowEmpty bool   `json:"allow_empty,omitempty"`
	ID         string `json:"id"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Column     string `json:"column,omitempty"`
	Expected   string `json:"expected,omitempty"`
	Min        string `json:"min,omitempty"`
	Max        string `json:"max,omitempty"`
	Tolerance  string `json:"tolerance,omitempty"`
}
type MetricCheckResult struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

func validateMetricChecks(c *DataContract) error {
	if len(c.Checks) > 16 {
		return fmt.Errorf("%w: 指标校验最多16条", ErrManagedInput)
	}
	seen := map[string]bool{}
	for _, check := range c.Checks {
		bad := func() error {
			return fmt.Errorf("%w: 指标校验定义无效，请核对标识、数值字段、预期值和误差", ErrManagedInput)
		}
		if !contractCheckID.MatchString(check.ID) || seen[check.ID] || strings.TrimSpace(check.Label) == "" || len([]rune(check.Label)) > 128 {
			return bad()
		}
		seen[check.ID] = true
		if check.AllowEmpty && check.Kind != "all_equal" && check.Kind != "range" {
			return bad()
		}
		tol := new(big.Rat)
		if check.Tolerance != "" {
			var ok bool
			tol, ok = contractDecimal(check.Tolerance)
			if !ok || tol.Sign() < 0 {
				return bad()
			}
		}
		if check.Kind == "row_count" {
			n, ok := contractDecimal(check.Expected)
			if !ok || !n.IsInt() || n.Sign() < 0 || n.Cmp(big.NewRat(100000, 1)) > 0 || check.Column != "" || check.Min != "" || check.Max != "" || tol.Sign() != 0 {
				return bad()
			}
			continue
		}
		numeric := false
		for _, col := range c.Schema {
			if col.Name == check.Column && (col.Type == "integer" || col.Type == "number" || col.Type == "lossless_numeric") {
				numeric = true
			}
		}
		if !numeric {
			return bad()
		}
		switch check.Kind {
		case "all_equal", "sum_equals":
			if _, ok := contractDecimal(check.Expected); !ok || check.Min != "" || check.Max != "" {
				return bad()
			}
		case "range":
			if check.Expected != "" || (check.Min == "" && check.Max == "") || tol.Sign() != 0 {
				return bad()
			}
			var low, high *big.Rat
			var ok bool
			if check.Min != "" {
				low, ok = contractDecimal(check.Min)
				if !ok {
					return bad()
				}
			}
			if check.Max != "" {
				high, ok = contractDecimal(check.Max)
				if !ok {
					return bad()
				}
			}
			if low != nil && high != nil && low.Cmp(high) > 0 {
				return bad()
			}
		default:
			return bad()
		}
	}
	return nil
}

// RunMetricChecks never returns values or row samples in diagnostics. Checks are
// approved rules over the complete result. Empty inputs fail unless the frozen
// per-row rule explicitly allows an empty result; NULL and truncation still fail.
func RunMetricChecks(c *DataContract, result *connector.ExecResult) []MetricCheckResult {
	out := []MetricCheckResult{}
	if c == nil {
		return out
	}
	valid := validateMetricChecks(c) == nil
	for _, check := range c.Checks {
		pass := valid && result != nil && !result.Truncated
		if pass && check.Kind == "row_count" {
			expected, _ := contractDecimal(check.Expected)
			pass = expected.Cmp(big.NewRat(int64(len(result.Rows)), 1)) == 0
		} else if pass {
			index := -1
			for i, name := range result.Columns {
				if name == check.Column {
					index = i
					break
				}
			}
			pass = index >= 0 && (len(result.Rows) > 0 || check.AllowEmpty)
			tol := new(big.Rat)
			if check.Tolerance != "" {
				tol, _ = contractDecimal(check.Tolerance)
			}
			var expected, low, high *big.Rat
			if check.Expected != "" {
				expected, _ = contractDecimal(check.Expected)
			}
			if check.Min != "" {
				low, _ = contractDecimal(check.Min)
			}
			if check.Max != "" {
				high, _ = contractDecimal(check.Max)
			}
			sum := new(big.Rat)
			for _, row := range result.Rows {
				if !pass {
					break
				}
				if index >= len(row) || row[index] == nil {
					pass = false
					break
				}
				number, ok := contractDecimal(row[index])
				if !ok {
					pass = false
					break
				}
				switch check.Kind {
				case "all_equal":
					pass = new(big.Rat).Abs(new(big.Rat).Sub(number, expected)).Cmp(tol) <= 0
				case "sum_equals":
					sum.Add(sum, number)
				case "range":
					pass = (low == nil || number.Cmp(low) >= 0) && (high == nil || number.Cmp(high) <= 0)
				}
			}
			if pass && check.Kind == "sum_equals" {
				pass = new(big.Rat).Abs(new(big.Rat).Sub(sum, expected)).Cmp(tol) <= 0
			}
		}
		message := "指标校验通过"
		if !pass {
			message = "指标不符合批准的校验规则，或结果为空、含NULL、截断、字段已变化"
		}
		out = append(out, MetricCheckResult{check.ID, check.Label, pass, message})
	}
	return out
}

type ContractDiagnostics struct {
	Passed   bool                `json:"passed"`
	Expected []ContractColumn    `json:"expected_schema"`
	Actual   []string            `json:"actual_columns"`
	Issues   []string            `json:"issues"`
	Checks   []MetricCheckResult `json:"metric_checks"`
}

// InspectContractResult is for an author's preview. Published delivery remains
// fail closed and does not expose expected fields or checks to unauthorized users.
func InspectContractResult(c *DataContract, result *connector.ExecResult) ContractDiagnostics {
	out := ContractDiagnostics{Expected: c.Schema, Actual: result.Columns, Issues: []string{}, Checks: RunMetricChecks(c, result)}
	if result.Truncated {
		out.Issues = append(out.Issues, "预览已截断，不能确认完整结果的指标校验")
	}
	if len(c.Schema) != len(result.Columns) {
		out.Issues = append(out.Issues, "结果字段数量已变化；依赖此契约的仪表盘需要核对")
	}
	for i, col := range c.Schema {
		if i >= len(result.Columns) || col.Name != result.Columns[i] {
			out.Issues = append(out.Issues, fmt.Sprintf("第%d列与声明字段 %s 不同", i+1, col.Name))
		}
	}
	if _, err := ValidateContractResult(&ApprovedContract{Declaration: *c}, result, time.Now()); err != nil {
		out.Issues = append(out.Issues, "字段名称、顺序、类型、空值、时效或指标规则未通过契约校验")
	}
	out.Passed = len(out.Issues) == 0
	return out
}
