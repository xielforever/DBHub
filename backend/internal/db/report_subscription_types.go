package db

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/user/dbhub/internal/connector"
)

type MetricAlertRule struct {
	Column    string `json:"column"`
	Operator  string `json:"operator"`
	Threshold string `json:"threshold"`
}
type ReportSubscriptionDefinition struct {
	Name            string           `json:"name"`
	ReportID        int64            `json:"report_id"`
	PublicationNo   int64            `json:"publication_no"`
	IntervalMinutes int              `json:"interval_minutes"`
	Params          map[string]any   `json:"params"`
	Alert           *MetricAlertRule `json:"alert_rule,omitempty"`
	Webhook         bool             `json:"webhook"`
	Enabled         bool             `json:"enabled"`
}
type ReportSubscription struct {
	ID             int64                        `json:"id"`
	Version        int64                        `json:"version"`
	Archived       bool                         `json:"archived"`
	ReviewRequired bool                         `json:"review_required,omitempty"`
	ReviewUntil    *time.Time                   `json:"review_until,omitempty"`
	Definition     ReportSubscriptionDefinition `json:"definition"`
	NextRunAt      time.Time                    `json:"next_run_at"`
	LastRunAt      *time.Time                   `json:"last_run_at,omitempty"`
	LastStatus     string                       `json:"last_status"`
}
type ReportSubscriptionRun struct {
	PermissionBridge    string                       `json:"permission_bridge"`
	PermissionSupported bool                         `json:"permission_supported"`
	DeliveryID          int64                        `json:"delivery_id"`
	ID                  string                       `json:"id"`
	SubscriptionID      int64                        `json:"subscription_id"`
	SubscriptionVersion int64                        `json:"subscription_version"`
	WorkspaceID         int64                        `json:"-"`
	UserID              int64                        `json:"-"`
	MemberVersion       int64                        `json:"-"`
	WorkerToken         string                       `json:"-"`
	RequestID           string                       `json:"-"`
	ReportID            int64                        `json:"report_id"`
	PublicationNo       int64                        `json:"publication_no"`
	Name                string                       `json:"name"`
	Definition          ReportSubscriptionDefinition `json:"-"`
	Status              string                       `json:"status"`
	Message             string                       `json:"message"`
	Rows                int                          `json:"row_count"`
	SHA256              string                       `json:"snapshot_sha256"`
	CreatedAt           time.Time                    `json:"created_at"`
	FinishedAt          *time.Time                   `json:"finished_at,omitempty"`
	ExpiresAt           time.Time                    `json:"expires_at"`
}
type ReportSubscriptionSnapshot struct {
	WorkspaceID         int64                `json:"workspace_id"`
	BusinessPolicyStamp string               `json:"business_policy_stamp,omitempty"`
	RunID               string               `json:"run_id"`
	ReportID            int64                `json:"report_id"`
	PublicationNo       int64                `json:"publication_no"`
	RequesterID         int64                `json:"requester_id"`
	Name                string               `json:"name"`
	CreatedAt           time.Time            `json:"created_at"`
	Params              map[string]any       `json:"params"`
	ManifestHash        string               `json:"manifest_sha256"`
	ChartType           string               `json:"chart_type"`
	ChartConfig         json.RawMessage      `json:"chart_config"`
	Contract            *DataContract        `json:"data_contract"`
	Result              connector.ExecResult `json:"result"`
}
type MetricIncident struct {
	ID               int64           `json:"id"`
	SubscriptionID   int64           `json:"subscription_id"`
	ReportID         int64           `json:"report_id"`
	PublicationNo    int64           `json:"publication_no"`
	Name             string          `json:"name"`
	Rule             MetricAlertRule `json:"rule"`
	Value            string          `json:"value"`
	Status           string          `json:"status"`
	Active           bool            `json:"condition_active"`
	Version          int64           `json:"version"`
	FirstRunID       string          `json:"first_run_id"`
	LastRunID        string          `json:"last_run_id"`
	OpenedAt         time.Time       `json:"opened_at"`
	LastSeenAt       time.Time       `json:"last_seen_at"`
	RecoveredAt      *time.Time      `json:"recovered_at,omitempty"`
	ClosedAt         *time.Time      `json:"closed_at,omitempty"`
	valuePolicyStamp string
}
type MetricIncidentEvent struct {
	ID            int64     `json:"id"`
	Type          string    `json:"event_type"`
	Comment       string    `json:"comment"`
	CreatedAt     time.Time `json:"created_at"`
	ObservedValue *string   `json:"observed_value,omitempty"`
}

func ValidateMetricAlertRule(rule *MetricAlertRule, contract *DataContract) error {
	if rule == nil {
		return nil
	}
	if len(rule.Threshold) > 256 {
		return ErrAutomationInput
	}
	if _, ok := contractDecimal(rule.Threshold); !ok {
		return fmt.Errorf("%w: 告警阈值必须为有限十进制数值", ErrAutomationInput)
	}
	if rule.Operator != "lt" && rule.Operator != "lte" && rule.Operator != "gt" && rule.Operator != "gte" && rule.Operator != "eq" && rule.Operator != "ne" {
		return ErrAutomationInput
	}
	if contract != nil {
		for _, column := range contract.Schema {
			if column.Name == rule.Column && (column.Type == "integer" || column.Type == "number" || column.Type == "lossless_numeric") {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: 告警字段须来自批准的数值字段", ErrAutomationInput)
}
func EvaluateMetricAlert(rule *MetricAlertRule, result *connector.ExecResult) (bool, string, error) {
	if rule == nil {
		return false, "", nil
	}
	if result == nil || result.Truncated || len(result.Rows) != 1 {
		return false, "", fmt.Errorf("告警查询须返回唯一且未截断的一行数值")
	}
	index := -1
	for i, column := range result.Columns {
		if column == rule.Column {
			index = i
			break
		}
	}
	if index < 0 || index >= len(result.Rows[0]) || result.Rows[0][index] == nil {
		return false, "", fmt.Errorf("告警字段不存在或为空")
	}
	text := fmt.Sprint(result.Rows[0][index])
	value, ok := contractDecimal(text)
	threshold, valid := contractDecimal(rule.Threshold)
	if !ok || !valid {
		return false, "", fmt.Errorf("告警字段或阈值不是有效数值")
	}
	comparison := value.Cmp(threshold)
	switch rule.Operator {
	case "lt":
		return comparison < 0, text, nil
	case "lte":
		return comparison <= 0, text, nil
	case "gt":
		return comparison > 0, text, nil
	case "gte":
		return comparison >= 0, text, nil
	case "eq":
		return comparison == 0, text, nil
	case "ne":
		return comparison != 0, text, nil
	}
	return false, "", ErrAutomationInput
}
func validateSubscriptionDefinition(d ReportSubscriptionDefinition) error {
	if strings.TrimSpace(d.Name) == "" || strings.ContainsRune(d.Name, 0) || len([]rune(d.Name)) > 120 || d.ReportID < 1 || d.PublicationNo < 1 || d.IntervalMinutes < 5 || d.IntervalMinutes > 10080 {
		return ErrAutomationInput
	}
	if len(canonicalReportJSON(d)) > 64<<10 {
		return ErrAutomationInput
	}
	return nil
}
