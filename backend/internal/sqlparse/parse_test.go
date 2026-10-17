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

func TestObviousAlwaysTrueWriteConditions(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		for _, condition := range []string{"TRUE", "1=1", "2>1", "id=1 OR 1=1", "NOT (1=2)", "(1=1 AND 2=2)"} {
			for _, statement := range []string{"DELETE FROM example WHERE ", "UPDATE example SET id=2 WHERE "} {
				ast, err := Parse(statement+condition, dialect)
				if err != nil || !ast.UnsafeWrite {
					t.Errorf("%s failed to guard %s: %v", dialect, statement+condition, err)
				}
			}
		}
		for _, condition := range []string{"id=1", "FALSE", "1=2", "id=1 AND 1=1", "id>0", "id=1 OR 1=2", "id IS NULL"} {
			ast, err := Parse("DELETE FROM example WHERE "+condition, dialect)
			if err != nil || ast.UnsafeWrite {
				t.Errorf("%s incorrectly blocked predicate %s: %v", dialect, condition, err)
			}
		}
	}
	for _, query := range []string{"WITH changed AS (DELETE FROM example WHERE 1=1 RETURNING *) SELECT * FROM changed", "EXPLAIN ANALYZE UPDATE example SET id=2 WHERE TRUE"} {
		ast, err := Parse(query, "postgres")
		if err != nil || !ast.UnsafeWrite {
			t.Errorf("nested always-true write accepted: %v", err)
		}
	}
}
