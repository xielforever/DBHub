package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

func TestIAMRuntimeRoutesHaveFiniteRules(t *testing.T) {
	a := setupAPI(t)
	for _, rule := range a.deps.IAMRoutes {
		if rule.Pattern == "/" {
			continue
		}
		if !rule.Known {
			t.Errorf("unclassified runtime route: %s", rule.Pattern)
		}
		for _, action := range append(append([]string{}, rule.Any...), rule.All...) {
			if c, known := iam.LookupAction(action); !known || c.Scope != "workspace" {
				t.Errorf("invalid action %s for %s", action, rule.Pattern)
			}
		}
	}
	for _, pattern := range iam.RegisteredPatterns() {
		found := false
		for _, rule := range a.deps.IAMRoutes {
			if pattern == rule.Pattern {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("obsolete route authorization: %s", pattern)
		}
	}
}

func TestIAMRestrictionRouteRequiresExactAction(t *testing.T) {
	const pattern = "POST /api/v1/iam/restrictions"
	for _, test := range []struct {
		kind, allowed, dependency string
	}{
		{"member.freeze", "space.members.exit", "space.members.view"},
		{"role.disable", "space.roles.manage", "space.roles.view"},
		{"assignment.revoke", "space.assign.manage", "space.assign.view"},
		{"delegation.revoke", "space.assign.manage", "space.assign.view"},
		{"action.revoke", "space.resources.revoke", "space.resources.view"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			unrelatedRestriction := "space.resources.revoke"
			if test.allowed == unrelatedRestriction {
				unrelatedRestriction = "space.members.exit"
			}
			for _, grant := range []string{test.allowed, unrelatedRestriction} {
				actions := []string{"space.page", test.dependency, grant}
				snapshot := &iam.Snapshot{Target: true, Subject: 1, WorkspaceID: 1, MembershipVersion: 1,
					ConfigVersion: 1, AuthorizationEpoch: 1, Actions: actions, Sources: []iam.Source{{Actions: actions}}}
				payload := `{"kind":"` + test.kind + `"}`
				req := httptest.NewRequest(http.MethodPost, "/api/v1/iam/restrictions", strings.NewReader(payload))
				req.Pattern = pattern
				req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{Subject: 1, IAM: snapshot}))
				out := httptest.NewRecorder()
				functionalGuard(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					claims, _ := auth.ClaimsFromContext(r.Context())
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != payload || claims.CurrentAction != test.allowed {
						t.Fatalf("selected action or request body changed: action=%s err=%v", claims.CurrentAction, err)
					}
					w.WriteHeader(http.StatusNoContent)
				}), out, req)
				want := http.StatusForbidden
				if grant == test.allowed {
					want = http.StatusNoContent
				}
				if out.Code != want {
					t.Fatalf("permission %s received HTTP %d, want %d: %s", grant, out.Code, want, out.Body)
				}
			}
		})
	}
}

func TestIAMNewEndpointCannotInheritLegacyPrefix(t *testing.T) {
	for _, pattern := range []string{"POST /api/v1/metrics/new-control", "GET /api/v1/users/new-private-directory", "POST /api/v1/managed-reports/{id}/new-release-mode"} {
		if iam.Rule(pattern).Known {
			t.Fatalf("unreviewed route inherited rights: %s", pattern)
		}
	}
}
