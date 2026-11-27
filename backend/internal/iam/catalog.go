// Package iam 定义稳定功能许可。功能许可不代替对象、原数据身份或工作流校验。
package iam

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

//go:embed catalog.json
var catalogJSON []byte

var frozenCatalog = ReadCatalog()

const (
	LegacyGuarded = "LEGACY_GUARDED"
	Shadow        = "SHADOW"
	BridgeVersion = "phase1-foundation-1"
)

type Capability struct {
	ID                 string   `json:"id"`
	Scope              string   `json:"scope"`
	Dependencies       []string `json:"dependencies"`
	State              string   `json:"state"`
	LegacyRoles        []string `json:"legacy_roles"`
	LegacyCapabilities []string `json:"legacy_capabilities"`
}

type Menu struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Group      string `json:"group"`
	Capability string `json:"capability"`
	Path       string `json:"path"`
}

type LegacyCapability struct {
	ID      string   `json:"id"`
	Actions []string `json:"actions"`
}

type Catalog struct {
	Version            string             `json:"version"`
	RuntimeBridge      string             `json:"runtime_bridge"`
	ActivationReady    bool               `json:"activation_ready"`
	Capabilities       []Capability       `json:"capabilities"`
	Menus              []Menu             `json:"menus"`
	LegacyCapabilities []LegacyCapability `json:"legacy_capabilities"`
}

// ReadCatalog returns a detached copy; callers cannot mutate the server catalog.
func ReadCatalog() Catalog {
	var c Catalog
	if e := json.Unmarshal(catalogJSON, &c); e != nil {
		panic(fmt.Errorf("IAM目录无效: %w", e))
	}
	return c
}

func KnownLegacyCapability(value string) bool {
	switch value {
	case "report_review", "datasource_manage", "audit_view", "operation_review":
		return true
	default:
		return false
	}
}

// LegacyCapabilityAllowed freezes the original four responsibilities. Admin
// never supplies a wildcard for newly introduced action IDs.
func LegacyCapabilityAllowed(role string, values []string, name string) bool {
	return KnownLegacyCapability(name) && (role == "admin" || slices.Contains(values, name))
}

type Snapshot struct {
	Target             bool       `json:"target"`
	RollbackGuard      bool       `json:"rollback_guard,omitempty"`
	Actions            []string   `json:"actions"`
	Sources            []Source   `json:"sources"`
	GovernanceOnly     bool       `json:"governance_only,omitempty"`
	Subject            int64      `json:"subject"`
	WorkspaceID        int64      `json:"workspace_id"`
	MembershipVersion  int64      `json:"membership_version"`
	ConfigVersion      int64      `json:"config_version"`
	AuthorizationEpoch int64      `json:"authorization_epoch"`
	RevocationSequence int64      `json:"revocation_sequence"`
	Mode               string     `json:"mode"`
	LegacyDataRole     string     `json:"legacy_data_role"`
	LegacyCapabilities []string   `json:"legacy_capabilities"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	DeniedActions      []string   `json:"denied_actions"`
}

type Decision struct {
	Action  string   `json:"action"`
	Allowed bool     `json:"allowed"`
	Reason  string   `json:"reason"`
	Sources []string `json:"sources"`
	// Always true: a functional decision cannot authorize an object or SQL data.
	RequiresObjectCheck bool   `json:"requires_object_check"`
	LegacyDataRole      string `json:"legacy_data_role"`
}

// ExplainLegacy evaluates only the fixed legacy functional envelope. SHADOW
// preserves the actual legacy path. Unsupported/active modes fail closed until
// the API, SQL and worker bridge has passed the later activation gate.
func ExplainLegacy(s Snapshot, action string, now time.Time) Decision {
	return explainLegacy(s, action, now, map[string]bool{})
}
func explainLegacy(s Snapshot, action string, now time.Time, path map[string]bool) Decision {
	d := Decision{Action: action, Reason: "unknown_action", Sources: []string{}, RequiresObjectCheck: true, LegacyDataRole: s.LegacyDataRole}
	var cap *Capability
	for i := range frozenCatalog.Capabilities {
		if frozenCatalog.Capabilities[i].ID == action {
			cap = &frozenCatalog.Capabilities[i]
			break
		}
	}
	if cap == nil {
		return d
	}
	if path[action] {
		d.Reason = "dependency_cycle"
		return d
	}
	path[action] = true
	defer delete(path, action)
	if s.Subject <= 0 || s.WorkspaceID <= 0 || s.MembershipVersion <= 0 || s.ConfigVersion <= 0 || s.AuthorizationEpoch <= 0 {
		d.Reason = "current_qualification_required"
		return d
	}
	if s.ExpiresAt != nil && !now.Before(*s.ExpiresAt) {
		d.Reason = "membership_expired"
		return d
	}
	if s.Mode != LegacyGuarded && s.Mode != Shadow {
		d.Reason = "activation_bridge_incomplete"
		return d
	}
	if cap.Scope != "workspace" {
		d.Reason = "independent_identity_scope"
		return d
	}
	if cap.State != "legacy" {
		d.Reason = "not_enabled"
		return d
	}
	if slices.Contains(s.DeniedActions, action) {
		d.Reason = "revoked"
		return d
	}
	if slices.Contains(cap.LegacyRoles, s.LegacyDataRole) {
		d.Sources = append(d.Sources, "legacy-role:"+s.LegacyDataRole)
	}
	for _, name := range cap.LegacyCapabilities {
		if slices.Contains(s.LegacyCapabilities, name) {
			d.Sources = append(d.Sources, "legacy-capability:"+name)
		}
	}
	d.Allowed = len(d.Sources) > 0
	if d.Allowed {
		d.Reason = "legacy_function_only"
		for _, dependency := range cap.Dependencies {
			if !explainLegacy(s, dependency, now, path).Allowed {
				d.Allowed, d.Reason = false, "dependency_not_allowed"
				break
			}
		}
	} else {
		d.Reason = "capability_required"
	}
	return d
}
