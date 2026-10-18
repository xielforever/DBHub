package config

import "testing"

func TestOperationsConfiguration(t *testing.T) {
	for k, v := range map[string]string{"APP_ENV": "development", "DBHUB_JDBC_URL": "", "DBHUB_AUDIT_RETENTION_DAYS": "0", "DBHUB_HISTORY_RETENTION_DAYS": "0", "DBHUB_READ_NOTIFICATION_RETENTION_DAYS": "90", "DBHUB_BACKUP_EXPECTED_HOURS": "0"} {
		t.Setenv(k, v)
	}
	cfg, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if cfg.Operations.AuditRetentionDays != 0 || cfg.Operations.HistoryRetentionDays != 0 || cfg.Operations.ReadNotificationRetentionDays != 90 {
		t.Fatal("destructive retention is enabled by default")
	}
	for _, tc := range []struct{ k, v string }{{"DBHUB_AUDIT_RETENTION_DAYS", "29"}, {"DBHUB_AUDIT_RETENTION_DAYS", "-1"}, {"DBHUB_HISTORY_RETENTION_DAYS", "6"}, {"DBHUB_HISTORY_RETENTION_DAYS", "NaN"}, {"DBHUB_READ_NOTIFICATION_RETENTION_DAYS", "3651"}, {"DBHUB_BACKUP_EXPECTED_HOURS", "169"}} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			t.Setenv(tc.k, tc.v)
			if _, e := Load(); e == nil {
				t.Fatal("unsafe retention configuration accepted")
			}
		})
	}
}
