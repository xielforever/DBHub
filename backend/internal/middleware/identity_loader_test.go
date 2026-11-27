package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user/dbhub/internal/auth"
)

type navigationIdentityStore struct{}

func (navigationIdentityStore) FindByUsername(context.Context, string) (*auth.User, error) {
	return nil, errors.New("unexpected fallback identity read")
}
func (navigationIdentityStore) TouchLogin(context.Context, int64) error { return nil }

type navigationMarker struct{}

func TestJoinedIdentityLoaderRetainsSessionSubjectAndCSRFChecks(t *testing.T) {
	for _, scenario := range []string{"current", "changed-password", "changed-auth-version", "wrong-subject", "missing-user", "service-unavailable", "revoked-session", "cookie-mutation"} {
		t.Run(scenario, func(t *testing.T) {
			secret := []byte("isolated-navigation-secret-32-bytes")
			original := &auth.User{ID: 1, Username: "fixture", Role: "developer", PasswordHash: "opaque", IsActive: true, AuthVersion: 1}
			pair, e := auth.IssuePair(secret, original, time.Minute, time.Hour)
			if e != nil {
				t.Fatal(e)
			}
			sessions := auth.NewSessionStore(nil)
			if e = sessions.Create(context.Background(), pair.SessionID, pair.RefreshID, original, time.Hour); e != nil {
				t.Fatal(e)
			}
			if scenario == "revoked-session" {
				if e = sessions.Revoke(context.Background(), pair.SessionID); e != nil {
					t.Fatal(e)
				}
			}
			current := *original
			var loadError error
			want := http.StatusUnauthorized
			switch scenario {
			case "current":
				want = http.StatusNoContent
			case "changed-password":
				current.PasswordHash = "changed-opaque"
			case "changed-auth-version":
				current.AuthVersion++
			case "wrong-subject":
				current.ID = 2
			case "missing-user":
				loadError = auth.ErrUserNotFound
			case "service-unavailable":
				loadError = errors.New("isolated unavailable")
				want = http.StatusServiceUnavailable
			case "cookie-mutation":
				want = http.StatusForbidden
			}
			calls, reached := 0, false
			loader := func(ctx context.Context, name string) (*auth.User, context.Context, error) {
				calls++
				if name != "fixture" {
					t.Fatal("unexpected JWT identity")
				}
				return &current, context.WithValue(ctx, navigationMarker{}, true), loadError
			}
			handler := RequireAuth(secret, navigationIdentityStore{}, sessions, loader)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok || claims.Subject != 1 || r.Context().Value(navigationMarker{}) != true {
					t.Fatal("joined context replaced authenticated subject")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
			if scenario == "cookie-mutation" {
				request.Method = http.MethodPost
				request.AddCookie(&http.Cookie{Name: "dbhub_access_token", Value: pair.AccessToken})
			} else {
				request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != want || calls != 1 || reached != (scenario == "current") {
				t.Fatal("joined identity bypassed authentication", response.Code, want, calls, reached)
			}
		})
	}
}
