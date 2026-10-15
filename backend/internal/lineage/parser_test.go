package lineage

import "testing"

func TestASTLineage(t *testing.T) {
	tests := []struct{ sql, dialect, target, source string }{
		{`WITH x AS (SELECT id FROM warehouse."Order Items") INSERT INTO analytics.summary SELECT * FROM x`, "postgres", "analytics.summary", `warehouse.Order Items`},
		{"INSERT INTO result_table SELECT u.id FROM users u JOIN orders o ON o.user_id=u.id", "mysql", "result_table", "users"},
		{`CREATE VIEW public.v AS SELECT id FROM public.source`, "postgres", "public.v", "public.source"},
	}
	for _, tt := range tests {
		p, e := ParseTables(tt.sql, tt.dialect)
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Targets) != 1 || p.Targets[0].String() != tt.target {
			t.Fatalf("target incorrect: %+v", p)
		}
		found := false
		for _, src := range p.Sources {
			if src.String() == tt.source {
				found = true
			}
			if src.Table == "x" || src.Table == "u" || src.Table == "o" {
				t.Fatal("CTE/alias treated as physical table")
			}
		}
		if !found {
			t.Fatalf("source incorrect: %+v", p)
		}
	}
	if _, e := ParseTables("SELECT FROM", "postgres"); e == nil {
		t.Fatal("invalid SQL fabricated lineage")
	}
}
