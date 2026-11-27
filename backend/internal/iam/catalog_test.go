package iam

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFrozenCatalogContract(t *testing.T) {
	c := ReadCatalog()
	if c.Version != "DBH-UX-IAM-D1.0" || c.ActivationReady || len(c.Menus) != 11 || len(c.Capabilities) != 96 {
		t.Fatal("catalog or activation contract changed")
	}
	known := map[string]Capability{}
	for _, cap := range c.Capabilities {
		if cap.ID == "" || strings.Contains(cap.ID, "*") {
			t.Fatal("invalid capability", cap.ID)
		}
		if _, exists := known[cap.ID]; exists {
			t.Fatal("duplicate capability", cap.ID)
		}
		known[cap.ID] = cap
		for _, old := range cap.LegacyCapabilities {
			if !KnownLegacyCapability(old) {
				t.Fatal("unknown legacy capability", old)
			}
		}
	}
	var visit func(string, map[string]bool)
	visit = func(id string, path map[string]bool) {
		if path[id] {
			t.Fatal("dependency cycle", id)
		}
		cap, ok := known[id]
		if !ok {
			t.Fatal("missing dependency", id)
		}
		path[id] = true
		for _, dependency := range cap.Dependencies {
			visit(dependency, path)
		}
		delete(path, id)
	}
	for id := range known {
		visit(id, map[string]bool{})
	}
	menuIDs, routes := map[string]bool{}, map[string]bool{}
	for _, m := range c.Menus {
		if menuIDs[m.ID] || routes[m.Path] || !strings.HasSuffix(m.Capability, ".page") || known[m.Capability].Scope != "workspace" {
			t.Fatal("invalid menu", m)
		}
		menuIDs[m.ID], routes[m.Path] = true, true
	}
	if known["platform.identity.manage"].Scope != "platform" || known["self.security"].Scope != "self" || known["source.manage"].State != "compatibility" {
		t.Fatal("independent scopes or retired compatibility alias changed")
	}
	// Mutating a handler's returned catalog cannot grant a new action globally.
	c.Capabilities[0].LegacyRoles = append(c.Capabilities[0].LegacyRoles, "invented")
	if strings.Contains(string(mustJSON(ReadCatalog())), "invented") {
		t.Fatal("catalog is shared mutable state")
	}
}

func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

func TestLegacyCapabilityHasNoFutureAdminWildcard(t *testing.T) {
	for _, role := range []string{"admin", "developer", "readonly"} {
		for _, name := range []string{"future.manage", "report.authorize", "report.reveal", "report.subscribeReview", "platform.governance.recover", "*", ""} {
			if LegacyCapabilityAllowed(role, []string{name}, name) {
				t.Fatal("future action granted", role, name)
			}
		}
	}
	for _, name := range []string{"report_review", "datasource_manage", "audit_view", "operation_review"} {
		if !LegacyCapabilityAllowed("admin", nil, name) || !LegacyCapabilityAllowed("readonly", []string{name}, name) || LegacyCapabilityAllowed("developer", nil, name) {
			t.Fatal("original responsibility changed", name)
		}
	}
}

func TestLegacyExplanationPreservesDataIdentityAndScopes(t *testing.T) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	s := Snapshot{Subject: 9, WorkspaceID: 5, MembershipVersion: 2, ConfigVersion: 1, AuthorizationEpoch: 4, Mode: LegacyGuarded, LegacyDataRole: "readonly", LegacyCapabilities: []string{"report_review", "datasource_manage"}}
	for _, tc := range []struct {
		action  string
		allowed bool
		reason  string
	}{
		{"report.review", true, "legacy_function_only"}, {"source.account.manage", true, "legacy_function_only"},
		{"query.read", true, "legacy_function_only"}, {"query.write", false, "capability_required"},
		{"report.authorize", false, "not_enabled"}, {"report.subscribeReview", false, "not_enabled"},
		{"platform.governance.recover", false, "independent_identity_scope"}, {"self.security", false, "independent_identity_scope"},
		{"source.manage", false, "not_enabled"}, {"invented.action", false, "unknown_action"},
	} {
		d := ExplainLegacy(s, tc.action, now)
		if d.Allowed != tc.allowed || d.Reason != tc.reason || d.LegacyDataRole != "readonly" || !d.RequiresObjectCheck {
			t.Fatalf("%s: %+v", tc.action, d)
		}
	}
	s.Mode = Shadow
	if !ExplainLegacy(s, "query.read", now).Allowed {
		t.Fatal("shadow changed the actual legacy path")
	}
	s.Mode = "COMPAT_ACTIVE"
	if ExplainLegacy(s, "query.read", now).Allowed {
		t.Fatal("incomplete bridge activated")
	}
	s.Mode = LegacyGuarded
	s.ExpiresAt = &now
	if ExplainLegacy(s, "query.read", now).Allowed {
		t.Fatal("expiry boundary allowed")
	}
	s.ExpiresAt = nil
	s.DeniedActions = []string{"query.read"}
	if ExplainLegacy(s, "query.read", now).Allowed || !ExplainLegacy(s, "report.view", now).Allowed {
		t.Fatal("denial affected unrelated action")
	}
	s.DeniedActions = nil
	s.AuthorizationEpoch = 0
	if ExplainLegacy(s, "report.view", now).Allowed {
		t.Fatal("unknown current version accepted")
	}
}
