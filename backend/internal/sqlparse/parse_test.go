package sqlparse

import "testing"

func TestReadOnlyClassification(t *testing.T) {
	for _, test := range []struct {
		sql, dialect string
		readOnly     bool
	}{
		{"SELECT '; delete from x' AS label", "postgres", true},
		{"/* select */ DELETE FROM x WHERE id=1", "postgres", false},
		{"WITH changed AS (DELETE FROM x RETURNING *) SELECT * FROM changed", "postgres", false},
		{"SELECT * INTO new_table FROM old_table", "postgres", false},
		{"SELECT * FROM x FOR UPDATE", "postgres", false},
		{"EXPLAIN ANALYZE DELETE FROM x WHERE id=1", "postgres", false},
		{"SELECT * FROM `users`", "mysql", true},
		{"UPDATE users SET name='x' WHERE id=1", "mysql", false},
	} {
		t.Run(test.sql, func(t *testing.T) {
			s, e := Parse(test.sql, test.dialect)
			if e != nil || s.ReadOnly != test.readOnly {
				t.Fatalf("classification=%v, error=%v", s, e)
			}
		})
	}
	for _, sql := range []string{"SELECT 1; DELETE FROM x", "BEGIN", "COMMIT", "", "SELECT FROM"} {
		if _, e := Parse(sql, "postgres"); e == nil {
			t.Errorf("unsafe/invalid input accepted: %q", sql)
		}
	}
}
func TestNamedParametersRespectLiteralsAndComments(t *testing.T) {
	query := `SELECT :id, ':ignored', "literal:column", $$:ignored$$, :id::text /* :ignored */ -- :ignored
 FROM example WHERE value={{value}}`
	result, args, e := BindNamed(query, "postgres", map[string]any{"id": 7, "value": "';--"})
	if e != nil || len(args) != 3 {
		t.Fatalf("binding failed: %v", e)
	}
	expected := `SELECT $1, ':ignored', "literal:column", $$:ignored$$, $2::text /* :ignored */ -- :ignored
 FROM example WHERE value=$3`
	if result != expected {
		t.Fatalf("unexpected SQL: %s", result)
	}
	if _, _, e = BindNamed("SELECT :missing", "postgres", nil); e == nil {
		t.Fatal("missing parameter accepted")
	}
}

func TestUnsafeWritesInsideCTEsAndExplain(t *testing.T) {
	for _, query := range []string{"WITH changed AS (DELETE FROM example RETURNING *) SELECT * FROM changed", "EXPLAIN ANALYZE UPDATE example SET id=2"} {
		s, e := Parse(query, "postgres")
		if e != nil || !s.UnsafeWrite {
			t.Fatalf("nested unsafe write not detected: %v", e)
		}
	}
}
