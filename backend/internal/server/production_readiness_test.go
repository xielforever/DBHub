package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/health"
)

func TestProductionReadinessRequiresConfiguredDependencies(t *testing.T) {
	h := readinessHandler(&config.Config{Env: "production"}, health.NewHandler("production"), nil)
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/api/ready", nil))
	if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing dependencies reported ready", w.Code)
	}
}

func TestReadinessTracksSessionDependencyRecovery(t *testing.T) {
	a := setupAPI(t)
	probe := func(path string) int {
		r, err := http.Get(a.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		return r.StatusCode
	}
	if probe("/api/ready") != 200 {
		t.Fatal("healthy dependencies rejected")
	}
	failing := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer failing.Close()
	dependencies := *a.deps
	working := dependencies.Redis
	dependencies.Redis = failing
	h := readinessHandler(&config.Config{}, health.NewHandler("development"), &dependencies)
	check := func() int {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", "/api/ready", nil))
		return w.Code
	}
	if check() != 503 || probe("/api/health") != 200 {
		t.Fatal("readiness/liveness did not distinguish dependency outage")
	}
	dependencies.Redis = working
	if check() != 200 {
		t.Fatal("dependency recovery required a process restart")
	}
	a.pool.Close()
	if probe("/api/ready") != 503 {
		t.Fatal("identity pool outage reported ready")
	}
	a.call("GET", "/auth/me", a.admin, nil, 503)
}
