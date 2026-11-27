package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseNacosProperties(t *testing.T) {
	content := strings.Join([]string{
		"# comment",
		"",
		"DATABASE_URL = postgres://user:pass@db:5432/dbhub",
		"REDIS_URL='rediss://:pass@redis:6379/0'",
		"UNKNOWN_KEY=ignored",
		"REDIS_URL=rediss://:second@redis:6379/1",
	}, "\n")
	values := parseNacosProperties(content)
	if got := values["DATABASE_URL"]; got != "postgres://user:pass@db:5432/dbhub" {
		t.Fatalf("DATABASE_URL = %q", got)
	}
	if got := values["REDIS_URL"]; got != "rediss://:second@redis:6379/1" {
		t.Fatalf("REDIS_URL = %q (want last value, quotes stripped)", got)
	}
	if _, ok := values["UNKNOWN_KEY"]; ok {
		t.Fatal("unknown key must be ignored")
	}
}

func newFakeNacos(t *testing.T, content *atomic.Value, requireToken bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/auth/user/login", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("username") != "reader" || r.Form.Get("password") != "secret" {
			http.Error(w, "unauthorized", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"accessToken": "token-1"})
	})
	mux.HandleFunc("/v3/console/cs/config", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dataId") != "dbhub.properties" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if requireToken && r.Header.Get("Authorization") != "Bearer token-1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(content.Load().(string)))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func testNacosOptions(serverURL string) NacosOptions {
	return NacosOptions{
		Enabled:      true,
		ServerAddr:   serverURL,
		Namespace:    "dbhub",
		Group:        "DEFAULT_GROUP",
		DataID:       "dbhub.properties",
		Username:     "reader",
		Password:     "secret",
		Timeout:      2 * time.Second,
		PollInterval: 10 * time.Millisecond,
		Required:     true,
		Format:       "properties",
	}
}

func TestFetchNacosValues(t *testing.T) {
	var content atomic.Value
	content.Store("DATABASE_URL=postgres://a:b@db:5432/dbhub\nREDIS_URL=rediss://:c@redis:6379/0\n")
	server := newFakeNacos(t, &content, true)
	values, hash, err := FetchNacosValues(context.Background(), testNacosOptions(server.URL))
	if err != nil {
		t.Fatalf("FetchNacosValues: %v", err)
	}
	if values["DATABASE_URL"] == "" || values["REDIS_URL"] == "" {
		t.Fatalf("values = %+v", values)
	}
	if hash != hashContent(content.Load().(string)) {
		t.Fatal("hash mismatch")
	}
}

func TestLoadNacosOverridesEnvironment(t *testing.T) {
	var content atomic.Value
	content.Store("DATABASE_URL=postgres://nacos:pass@db:5432/dbhub\nREDIS_URL=rediss://:pass@redis:6379/0\n")
	server := newFakeNacos(t, &content, true)

	t.Setenv("DATABASE_URL", "postgres://env:pass@env-db:5432/dbhub")
	t.Setenv("REDIS_URL", "rediss://:pass@env-redis:6379/0")
	t.Setenv("DBHUB_NACOS_ENABLED", "true")
	t.Setenv("DBHUB_NACOS_SERVER_ADDR", server.URL)
	t.Setenv("DBHUB_NACOS_NAMESPACE", "dbhub")
	t.Setenv("DBHUB_NACOS_GROUP", "DEFAULT_GROUP")
	t.Setenv("DBHUB_NACOS_DATA_ID", "dbhub.properties")
	t.Setenv("DBHUB_NACOS_USERNAME", "reader")
	t.Setenv("DBHUB_NACOS_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://nacos:pass@db:5432/dbhub" {
		t.Fatalf("Nacos 必须优先于环境变量，got %q", cfg.DatabaseURL)
	}
	if cfg.RedisURL != "rediss://:pass@redis:6379/0" {
		t.Fatalf("RedisURL = %q", cfg.RedisURL)
	}
	if cfg.NacosInitialHash == "" {
		t.Fatal("NacosInitialHash 未设置")
	}
}

func TestLoadNacosRequiredFailure(t *testing.T) {
	t.Setenv("DBHUB_NACOS_ENABLED", "true")
	t.Setenv("DBHUB_NACOS_SERVER_ADDR", "http://127.0.0.1:1")
	t.Setenv("DBHUB_NACOS_DATA_ID", "dbhub.properties")
	if _, err := Load(); err == nil {
		t.Fatal("Nacos 不可用且 REQUIRED=true 时应返回错误")
	}
}

func TestWatchNacosDetectsChange(t *testing.T) {
	var content atomic.Value
	content.Store("DATABASE_URL=postgres://a:b@db:5432/dbhub\n")
	server := newFakeNacos(t, &content, true)
	opts := testNacosOptions(server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := make(chan map[string]string, 1)
	go WatchNacos(ctx, opts, hashContent(content.Load().(string)), func(values map[string]string, _ string) bool {
		changed <- values
		return true
	}, func(error) {})

	time.Sleep(30 * time.Millisecond)
	content.Store("DATABASE_URL=postgres://a:b@db:5432/dbhub\nREDIS_URL=rediss://:x@redis:6379/0\n")

	select {
	case values := <-changed:
		if values["REDIS_URL"] == "" {
			t.Fatalf("变更内容缺失: %+v", values)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未检测到 Nacos 变更")
	}
}

func TestRedactNacosMessage(t *testing.T) {
	values := parseNacosProperties("DATABASE_URL=postgres://user:supersecret@db:5432/dbhub\n")
	msg := RedactNacosMessage("连接 postgres://user:supersecret@db:5432/dbhub 失败 supersecret", values)
	if strings.Contains(msg, "supersecret") {
		t.Fatalf("口令未脱敏: %s", msg)
	}
}
