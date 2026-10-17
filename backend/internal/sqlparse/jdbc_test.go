package sqlparse

import "testing"

func TestJDBCRebindPreservesLiteralsOperatorsAndParameterOrder(t *testing.T) {
	query := `SELECT '$1?', $$ $2? $$, "column?", doc ? $2, $1::text, $2 /* $9 ? */ -- $8 ?
 FROM example`
	got, args, e := RebindJDBC(query, []any{"first", "second"})
	if e != nil {
		t.Fatal(e)
	}
	want := `SELECT '$1?', $$ $2? $$, "column?", doc ?? ?, ?::text, ? /* $9 ? */ -- $8 ?
 FROM example`
	if got != want || len(args) != 3 || args[0] != "second" || args[1] != "first" || args[2] != "second" {
		t.Fatal(got, args)
	}
	if _, _, e = RebindJDBC("SELECT $3", []any{1}); e == nil {
		t.Fatal("missing parameter accepted")
	}
	if _, e = Parse("SELECT 1", "unknown"); e == nil {
		t.Fatal("unknown dialect fell back to postgres")
	}
}
