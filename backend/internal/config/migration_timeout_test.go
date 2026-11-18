package config

import (
	"testing"
	"time"
)

func TestMigrationTimeout(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	for _, tc := range []struct {
		value   string
		seconds int
	}{{"", 30}, {"1", 1}, {"180", 180}, {"600", 600}} {
		t.Setenv("DBHUB_MIGRATION_TIMEOUT_SECONDS", tc.value)
		cfg, err := Load()
		if err != nil || cfg.MigrationTimeout != time.Duration(tc.seconds)*time.Second {
			t.Fatalf("timeout %q: %v", tc.value, err)
		}
	}
	for _, value := range []string{"0", "-1", "601", "1.5", "invalid"} {
		t.Setenv("DBHUB_MIGRATION_TIMEOUT_SECONDS", value)
		if _, err := Load(); err == nil {
			t.Fatalf("accepted invalid timeout %q", value)
		}
	}
}

func TestReadinessTimeoutConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DBHUB_READINESS_TIMEOUT_SECONDS", "10")
	t.Setenv("DBHUB_REDIS_PROBE_TIMEOUT_MS", "3000")
	cfg, err := Load()
	if err != nil || cfg.ReadinessTimeout != 10*time.Second || cfg.RedisProbeTimeout != 3*time.Second {
		t.Fatalf("probe configuration: %v", err)
	}
	for _, setting := range []struct{ key, value string }{{"DBHUB_READINESS_TIMEOUT_SECONDS", "61"}, {"DBHUB_READINESS_TIMEOUT_SECONDS", "0"}, {"DBHUB_REDIS_PROBE_TIMEOUT_MS", "10001"}, {"DBHUB_REDIS_PROBE_TIMEOUT_MS", "bad"}} {
		t.Run(setting.key+setting.value, func(t *testing.T) {
			t.Setenv(setting.key, setting.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid probe timeout accepted")
			}
		})
	}
}
