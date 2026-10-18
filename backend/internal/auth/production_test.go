package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/config"
)

type unavailableUsers struct{ err error }

func (u unavailableUsers) FindByUsername(context.Context, string) (*User, error) { return nil, u.err }
func (u unavailableUsers) TouchLogin(context.Context, int64) error               { return u.err }

func TestUserStoreOutageDoesNotInvalidateIdentity(t *testing.T) {
	cfg := &config.Config{SecretKey: []byte("production-test-signing-key-longer-than-32"), AccessTTL: time.Minute, RefreshTTL: time.Hour}
	pair, err := IssuePair(cfg.SecretKey, &User{ID: 1, Username: "example", Role: "admin"}, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []struct {
		name   string
		err    error
		status int
	}{{"outage", errors.New("backend unavailable"), 503}, {"revoked", ErrUserNotFound, 401}} {
		t.Run(fault.name, func(t *testing.T) {
			h := NewHandler(cfg, unavailableUsers{fault.err}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			for _, endpoint := range []string{"login", "refresh", "me"} {
				body := `{"username":"example","password":"example"}`
				if endpoint == "refresh" {
					body = `{"refresh_token":"` + pair.RefreshToken + `"}`
				}
				r := httptest.NewRequest("POST", "/auth/"+endpoint, strings.NewReader(body))
				r = r.WithContext(context.WithValue(r.Context(), ClaimsContextKey, &Claims{Subject: 1, Username: "example"}))
				w := httptest.NewRecorder()
				switch endpoint {
				case "login":
					h.Login(w, r)
				case "refresh":
					h.Refresh(w, r)
				case "me":
					h.Me(w, r)
				}
				if w.Code != fault.status {
					t.Fatalf("%s: got %d want %d", endpoint, w.Code, fault.status)
				}
				if strings.Contains(w.Body.String(), "backend unavailable") {
					t.Fatal("internal error disclosed")
				}
			}
		})
	}
}

func TestLoginBudgetsExpireAndIgnoreSpoofedProxyHeaders(t *testing.T) {
	cfg := &config.Config{SecretKey: []byte("budget-test"), LoginMaxAttempts: 3, LoginIPMaxAttempts: 4, LoginWindow: time.Minute}
	l := newLoginLimiter(cfg, nil)
	now := time.Now()
	l.now = func() time.Time { return now }
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	for i := 0; i < 3; i++ {
		if wait, err := l.allow(context.Background(), "member", r); wait != 0 || err != nil {
			t.Fatal(wait, err)
		}
	}
	r.Header.Set("X-Forwarded-For", "198.51.100.77")
	if wait, err := l.allow(context.Background(), "MEMBER", r); wait <= 0 || err != nil {
		t.Fatal("account limit bypassed", wait, err)
	}
	if wait, err := l.allow(context.Background(), "different", r); wait != 0 || err != nil {
		t.Fatal("blocked attempts consumed the peer budget", wait, err)
	}
	if wait, err := l.allow(context.Background(), "third", r); wait <= 0 || err != nil {
		t.Fatal("forged IP bypassed peer limit", wait, err)
	}
	now = now.Add(time.Minute)
	if wait, err := l.allow(context.Background(), "member", r); wait != 0 || err != nil {
		t.Fatal("expired budget stayed locked", wait, err)
	}
}

func TestClientIPTrustsOnlyConfiguredProxyHops(t *testing.T) {
	for _, test := range []struct {
		peer, forwarded string
		trusted         []netip.Prefix
		want            string
	}{
		{"192.0.2.1:80", "198.51.100.2", nil, "192.0.2.1"},
		{"10.0.0.1:80", "203.0.113.9, 198.51.100.2, 10.0.0.2", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}, "198.51.100.2"},
		{"[::ffff:192.0.2.1]:80", "bad", nil, "192.0.2.1"},
		{"10.0.0.1:80", "not-an-ip", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}, "10.0.0.1"},
	} {
		r := httptest.NewRequest("POST", "/login", nil)
		r.RemoteAddr = test.peer
		r.Header.Set("X-Forwarded-For", test.forwarded)
		if got := ClientIP(r, test.trusted); got != test.want {
			t.Fatalf("%s: got %s want %s", test.peer, got, test.want)
		}
	}
}

func TestRedisLoginBudgetIsAtomicAcrossHandlers(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("real Redis not configured")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	cfg := &config.Config{SecretKey: []byte(fmt.Sprintf("budget-%d", time.Now().UnixNano())), LoginMaxAttempts: 8, LoginIPMaxAttempts: 100, LoginWindow: time.Minute}
	limits := []*loginLimiter{newLoginLimiter(cfg, client), newLoginLimiter(cfg, client)}
	request := httptest.NewRequest("POST", "/login", nil)
	defer client.Del(context.Background(), limits[0].key("account", "member"), limits[0].key("ip", ClientIP(request, nil)))
	var accepted atomic.Int32
	var failed atomic.Int32
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			wait, err := limits[i%2].allow(context.Background(), "member", request)
			if err != nil {
				failed.Add(1)
			} else if wait == 0 {
				accepted.Add(1)
			}
		}(i)
	}
	workers.Wait()
	if failed.Load() != 0 || accepted.Load() != 8 {
		t.Fatal("shared budget was not atomic", accepted.Load(), failed.Load())
	}
	if ttl := client.PTTL(context.Background(), limits[0].key("account", "member")).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatal("budget lost its expiry", ttl)
	}
	failing := redis.NewClient(options)
	failing.Close()
	limits[0].client = failing
	if _, err = limits[0].allow(context.Background(), "other", request); err == nil {
		t.Fatal("unavailable distributed protection failed open")
	}
}
