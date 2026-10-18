package config

import "testing"

func TestProductionSecurityConfiguration(t *testing.T) {
	for key, value := range map[string]string{"APP_ENV": "production", "APP_SECRET_KEY": "config-test-key-longer-than-32-bytes", "APP_ADMIN_PASSWORD": "ConfigTest9!", "DATABASE_URL": "postgres://example/meta", "REDIS_URL": "redis://example:6379/0", "DBHUB_AUDIT_DIR": "/test/audit", "DBHUB_TARGET_ALLOWLIST": "postgres:5432", "DBHUB_JDBC_URL": "", "DBHUB_LOGIN_MAX_ATTEMPTS": "10", "DBHUB_LOGIN_IP_MAX_ATTEMPTS": "60", "DBHUB_LOGIN_WINDOW_SECONDS": "300", "DBHUB_TRUSTED_PROXY_CIDRS": "", "CORS_ALLOWED_ORIGINS": ""} {
		t.Setenv(key, value)
	}
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ key, value string }{
		{"DBHUB_TARGET_ALLOWLIST", ""}, {"DBHUB_TARGET_ALLOWLIST", "postgres:*"}, {"DBHUB_LOGIN_MAX_ATTEMPTS", "0"}, {"DBHUB_LOGIN_IP_MAX_ATTEMPTS", "bad"}, {"DBHUB_LOGIN_WINDOW_SECONDS", "-1"}, {"DBHUB_TRUSTED_PROXY_CIDRS", "0.0.0.0"}, {"CORS_ALLOWED_ORIGINS", "*"},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid production boundary accepted")
			}
		})
	}
}
