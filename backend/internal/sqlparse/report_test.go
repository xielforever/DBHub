package sqlparse

import "testing"

func TestReportQueryValidation(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		for _, query := range []string{"SELECT 1", "SELECT ';' AS label;", "SELECT 1 UNION SELECT 2"} {
			if err := ValidateReport(query, dialect); err != nil {
				t.Errorf("%s rejected read query: %v", dialect, err)
			}
		}
		for _, query := range []string{"UPDATE x SET id=1 WHERE id=2", "SELECT * FROM x FOR UPDATE", "SELECT 1; SELECT 2", "EXPLAIN SELECT 1", "BEGIN"} {
			if err := ValidateReport(query, dialect); err == nil {
				t.Errorf("%s accepted %s", dialect, query)
			}
		}
	}
	if err := ValidateReport("WITH d AS (DELETE FROM x WHERE id=1 RETURNING *) SELECT * FROM d", "postgres"); err == nil {
		t.Fatal("write CTE accepted")
	}
	if err := ValidateReport("SHOW TABLES", "mysql"); err == nil {
		t.Fatal("management statement accepted")
	}
}
