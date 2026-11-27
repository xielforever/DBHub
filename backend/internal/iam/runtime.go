package iam

import (
	"slices"
	"time"
)

const CompatActive = "COMPAT_ACTIVE"
const RuntimeBridge = "phase1-bridge-2"

type Source struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	RoleID       string     `json:"role_id,omitempty"`
	RoleName     string     `json:"role_name,omitempty"`
	Revision     int64      `json:"revision,omitempty"`
	GroupID      int64      `json:"group_id,omitempty"`
	GroupVersion int64      `json:"group_version,omitempty"`
	Actions      []string   `json:"actions"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// Allows uses the server-rebound snapshot only. A target denial never falls
// through to legacy admin. Data and object checks remain mandatory.
func Allows(s *Snapshot, action string, now time.Time) bool {
	if s == nil {
		return false
	}
	if !s.Target {
		return ExplainLegacy(*s, action, now).Allowed
	}
	if s.Subject <= 0 || s.WorkspaceID <= 0 || s.MembershipVersion <= 0 || s.ConfigVersion <= 0 || s.AuthorizationEpoch <= 0 {
		return false
	}
	if s.ExpiresAt != nil && !now.Before(*s.ExpiresAt) {
		return false
	}
	if !slices.Contains(s.Actions, action) {
		return false
	}
	var current func(string, map[string]bool) bool
	current = func(id string, path map[string]bool) bool {
		if path[id] || !slices.Contains(s.Actions, id) {
			return false
		}
		cap, known := LookupAction(id)
		if !known {
			return false
		}
		found := false
		for _, source := range s.Sources {
			if slices.Contains(source.Actions, id) && (source.ExpiresAt == nil || now.Before(*source.ExpiresAt)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		path[id] = true
		defer delete(path, id)
		for _, dependency := range cap.Dependencies {
			if !current(dependency, path) {
				return false
			}
		}
		return true
	}
	return current(action, map[string]bool{})
}

func LegacyResponsibilityForAction(name, action string) bool {
	for _, cap := range frozenCatalog.Capabilities {
		if cap.ID == action {
			return slices.Contains(cap.LegacyCapabilities, name)
		}
	}
	return false
}

func LookupAction(action string) (Capability, bool) {
	for _, c := range frozenCatalog.Capabilities {
		if c.ID == action {
			return c, true
		}
	}
	return Capability{}, false
}
