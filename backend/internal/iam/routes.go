package iam

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

func RouteFingerprint(rules []RouteRule) string {
	copy := append([]RouteRule{}, rules...)
	slices.SortFunc(copy, func(a, b RouteRule) int { return strings.Compare(a.Pattern, b.Pattern) })
	for _, r := range copy {
		if r.Pattern == "/" {
			continue
		}
		if !r.Known {
			return ""
		}
		for _, id := range append(append([]string{}, r.Any...), r.All...) {
			if c, ok := LookupAction(id); !ok || c.Scope != "workspace" {
				return ""
			}
		}
	}
	raw, _ := json.Marshal(copy)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// RouteRule is functional authorization, never an object/audience grant. Any
// is an explicit alternative; adding a route without a rule denies target
// principals. Identity and public routes keep their separate authentication.
type RouteRule struct {
	Pattern  string   `json:"pattern"`
	Scope    string   `json:"scope"`
	Any      []string `json:"any"`
	All      []string `json:"all,omitempty"`
	Selector string   `json:"selector,omitempty"`
	Known    bool     `json:"known"`
}

//go:embed routes.json
var routeRegistryJSON []byte
var routeRegistry = loadRouteRegistry()

// A new handler cannot inherit authorization from a URL prefix. It must be
// reviewed and explicitly entered here before any legacy or target caller uses
// it. The expanded-router test detects missing and obsolete registrations.
func loadRouteRegistry() map[string]RouteRule {
	var document struct {
		Version string      `json:"version"`
		Routes  []RouteRule `json:"routes"`
	}
	if e := json.Unmarshal(routeRegistryJSON, &document); e != nil {
		panic(e)
	}
	if document.Version != "DBH-UX-IAM-D1.0" {
		panic("invalid route registry version")
	}
	out := map[string]RouteRule{}
	scopes := map[string]bool{"public": true, "identity": true, "workspace": true, "own_space": true, "legacy_governance": true, "blocked": true}
	selectors := map[string]bool{"": true, "query_execution": true, "release_kind": true, "review_decision": true, "dashboard_refresh_mode": true, "dashboard_receipt_mode": true, "source_task_kind": true, "restriction_kind": true}
	for _, rule := range document.Routes {
		if _, ok := out[rule.Pattern]; ok || !rule.Known || !scopes[rule.Scope] || !selectors[rule.Selector] {
			panic("invalid route registry entry")
		}
		for _, id := range append(append([]string{}, rule.Any...), rule.All...) {
			c, ok := LookupAction(id)
			if !ok || c.Scope != "workspace" {
				panic("unknown registered functional action")
			}
		}
		out[rule.Pattern] = rule
	}
	return out
}

// RestrictionAction returns the workspace permission for a specific tightening action.
func RestrictionAction(kind string) string {
	switch kind {
	case "member.freeze":
		return "space.members.exit"
	case "role.disable":
		return "space.roles.manage"
	case "assignment.revoke", "delegation.revoke":
		return "space.assign.manage"
	case "action.revoke":
		return "space.resources.revoke"
	default:
		return ""
	}
}
func Rule(pattern string) RouteRule {
	rule, ok := routeRegistry[pattern]
	if !ok {
		return RouteRule{Pattern: pattern, Scope: "workspace"}
	}
	rule.Any = append([]string{}, rule.Any...)
	rule.All = append([]string{}, rule.All...)
	return rule
}
func RegisteredPatterns() []string {
	out := make([]string, 0, len(routeRegistry))
	for pattern := range routeRegistry {
		out = append(out, pattern)
	}
	slices.Sort(out)
	return out
}
