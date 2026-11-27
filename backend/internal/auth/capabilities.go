package auth

import (
	"github.com/user/dbhub/internal/iam"
	"time"
)

func HasCapability(c *Claims, capability string) bool {
	if c == nil || c.Subject <= 0 {
		return false
	}
	if c.IAM != nil && c.IAM.Target {
		return c.CurrentAction != "" && iam.LegacyResponsibilityForAction(capability, c.CurrentAction) && iam.Allows(c.IAM, c.CurrentAction, time.Now())
	}
	return iam.LegacyCapabilityAllowed(c.Role, c.Capabilities, capability)
}

func HasAction(c *Claims, action string) bool {
	return c != nil && c.Subject > 0 && iam.Allows(c.IAM, action, time.Now())
}

// FunctionalAction keeps legacy predicates only on the legacy path. It must
// never be used for ownership, credentials or SQL data-policy role selection.
func FunctionalAction(c *Claims, action string, legacy bool) bool {
	if c == nil || c.Subject <= 0 {
		return false
	}
	if c.IAM != nil && c.IAM.Target {
		return HasAction(c, action)
	}
	return legacy
}
func ValidCapabilities(values []string) bool {
	if len(values) > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
		switch value {
		case "report_review", "datasource_manage", "audit_view", "operation_review":
		default:
			return false
		}
	}
	return true
}
