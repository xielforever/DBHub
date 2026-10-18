package db

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/user/dbhub/internal/connector"
)

type ContractColumn struct {
	Name     string `json:"name"`
	Type     string `json:"logical_type"`
	Nullable bool   `json:"nullable"`
}
type ContractFreshness struct {
	Mode       string `json:"mode"`
	Column     string `json:"column"`
	MaxAge     int64  `json:"max_data_age_seconds"`
	FutureSkew int64  `json:"max_future_skew_seconds"`
}
type DataContract struct {
	SemanticKey      string            `json:"semantic_key"`
	Meaning          string            `json:"meaning"`
	Schema           []ContractColumn  `json:"result_schema"`
	Dimensions       []string          `json:"dimensions"`
	Metrics          []string          `json:"metrics"`
	ChartType        string            `json:"chart_type"`
	Unit             string            `json:"unit"`
	Currency         string            `json:"currency"`
	Timezone         string            `json:"timezone"`
	ScopeKey         string            `json:"scope_key"`
	ScopeDescription string            `json:"scope_description"`
	TimeWindow       json.RawMessage   `json:"time_window"`
	ComparisonKey    string            `json:"comparison_key"`
	Freshness        ContractFreshness `json:"freshness"`
}
type ContractBoundary struct {
	WorkspaceID int64  `json:"workspace_id"`
	SourceID    int64  `json:"source_revision_id"`
	Database    string `json:"database_name"`
	AccountID   int64  `json:"report_account_id"`
}
type ApprovedContract struct {
	Declaration DataContract     `json:"declaration"`
	Boundary    ContractBoundary `json:"execution_boundary"`
	Hash        string           `json:"hash"`
}

func ValidateDataContract(c *DataContract, chart string) error {
	if c == nil {
		return nil
	}
	if c.Dimensions == nil {
		c.Dimensions = []string{}
	}
	if c.Metrics == nil {
		c.Metrics = []string{}
	} // Legacy standalone reports remain usable, never dashboard eligible.
	invalid := func() error {
		return fmt.Errorf("%w: 请完整声明报表口径、字段、单位、范围、时间窗口和时效规则", ErrManagedInput)
	}
	if len(c.Unit) > 64 || len(c.ScopeKey) > 100 || len([]rune(c.ScopeDescription)) > 2000 || len(c.ComparisonKey) > 100 || len(c.Timezone) > 64 || len(c.Dimensions) > 12 || len(c.Metrics) > 12 {
		return invalid()
	}
	if len(c.Schema) == 0 || len(c.Schema) > 100 || strings.TrimSpace(c.SemanticKey) == "" || len(c.SemanticKey) > 100 || strings.TrimSpace(c.Meaning) == "" || len([]rune(c.Meaning)) > 1000 || c.Unit == "" || c.ScopeKey == "" || c.ScopeDescription == "" || c.ChartType != chart || len(c.TimeWindow) > 2000 {
		return invalid()
	}
	var window map[string]any
	if json.Unmarshal(c.TimeWindow, &window) != nil || window["kind"] == nil {
		return invalid()
	}
	if _, e := time.LoadLocation(c.Timezone); e != nil {
		return invalid()
	}
	if c.Currency != "" && !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(c.Currency) {
		return invalid()
	}
	seen := map[string]bool{}
	for _, col := range c.Schema {
		if col.Name == "" || len(col.Name) > 128 || seen[col.Name] {
			return invalid()
		}
		seen[col.Name] = true
		switch col.Type {
		case "string", "integer", "lossless_numeric", "number", "boolean", "date", "timestamp_with_timezone":
		default:
			return invalid()
		}
	}
	for _, name := range append(append([]string{}, c.Dimensions...), c.Metrics...) {
		if !seen[name] {
			return invalid()
		}
	}
	f := c.Freshness
	if f.Mode != "unverified" && f.Mode != "watermark_column" {
		return invalid()
	}
	if f.Mode == "watermark_column" && (!seen[f.Column] || f.MaxAge < 1 || f.MaxAge > 86400*30 || f.FutureSkew < 0 || f.FutureSkew > 60) {
		return invalid()
	}
	return nil
}

func ValidateContractMapping(c *DataContract, config json.RawMessage) error {
	if err := validateContractDisplay(c, config); err != nil {
		return err
	}
	if c == nil || c.ChartType == "table" {
		return nil
	}
	var mapping struct {
		Dimension string   `json:"dimension"`
		Metrics   []string `json:"metrics"`
	}
	if json.Unmarshal(config, &mapping) != nil {
		return ErrManagedInput
	}
	dimensions := []string{}
	if mapping.Dimension != "" {
		dimensions = append(dimensions, mapping.Dimension)
	}
	if reportDigest(dimensions) != reportDigest(c.Dimensions) || reportDigest(mapping.Metrics) != reportDigest(c.Metrics) {
		return fmt.Errorf("%w: 图表维度和指标必须与数据契约映射一致", ErrManagedInput)
	}
	return nil
}
func approvedContract(c *DataContract, wid, source, account int64, database string) *ApprovedContract {
	if c == nil {
		return nil
	}
	a := &ApprovedContract{Declaration: *c, Boundary: ContractBoundary{wid, source, database, account}}
	a.Hash = reportDigest([]any{a.Declaration, a.Boundary})
	return a
}

type ContractResult struct {
	QueryCompletedAt time.Time  `json:"query_completed_at"`
	DataAsOf         *time.Time `json:"data_as_of"`
	Freshness        string     `json:"freshness"`
	ContractHash     string     `json:"contract_hash"`
}

func ValidateContractResult(c *ApprovedContract, result *connector.ExecResult, now time.Time) (ContractResult, error) {
	out := ContractResult{QueryCompletedAt: now, Freshness: "unknown"}
	if c == nil {
		return out, nil
	}
	out.ContractHash = c.Hash
	bad := func() (ContractResult, error) {
		return out, fmt.Errorf("%w: 查询结果与获批字段或时效契约不符", ErrManagedInput)
	}
	if result.Truncated || len(result.Columns) != len(c.Declaration.Schema) {
		return bad()
	}
	watermark := -1
	for i, col := range c.Declaration.Schema {
		if col.Name != result.Columns[i] {
			return bad()
		}
		if col.Name == c.Declaration.Freshness.Column {
			watermark = i
		}
	}
	var data *time.Time
	missingWatermark := false
	for _, row := range result.Rows {
		if len(row) != len(result.Columns) {
			return bad()
		}
		for i, v := range row {
			col := c.Declaration.Schema[i]
			if v == nil {
				if !col.Nullable {
					return bad()
				}
				continue
			}
			switch col.Type {
			case "integer", "lossless_numeric", "number":
				if _, ok := new(big.Rat).SetString(fmt.Sprint(v)); !ok {
					return bad()
				}
				if col.Type == "integer" {
					n, _ := new(big.Rat).SetString(fmt.Sprint(v))
					if !n.IsInt() {
						return bad()
					}
				}
			case "boolean":
				if _, ok := v.(bool); !ok {
					return bad()
				}
			case "string":
				if _, ok := v.(string); !ok {
					return bad()
				}
			case "date":
				if _, e := time.Parse("2006-01-02", fmt.Sprint(v)); e != nil {
					return bad()
				}
			case "timestamp_with_timezone":
				if _, e := contractTime(v); e != nil {
					return bad()
				}
			}
		}
		if c.Declaration.Freshness.Mode == "watermark_column" && watermark >= 0 {
			if row[watermark] == nil {
				missingWatermark = true
				continue
			}
			value, e := contractTime(row[watermark])
			if e != nil || value.After(now.Add(time.Duration(c.Declaration.Freshness.FutureSkew)*time.Second)) {
				return bad()
			}
			if data != nil && !data.Equal(value) {
				return bad()
			}
			data = &value
		}
	}
	if data != nil && !missingWatermark {
		out.DataAsOf = data
		out.Freshness = "fresh"
		if now.Sub(*data) > time.Duration(c.Declaration.Freshness.MaxAge)*time.Second {
			out.Freshness = "stale"
		}
	}
	return out, nil
}
func contractTime(v any) (time.Time, error) {
	if t, ok := v.(time.Time); ok {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, fmt.Sprint(v))
}
