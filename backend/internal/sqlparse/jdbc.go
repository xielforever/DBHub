package sqlparse

import (
	"fmt"
	pgquery "github.com/pganalyze/pg_query_go/v5"
	"strconv"
	"strings"
)

// RebindJDBC uses PostgreSQL's lexer, preserving literals/comments and repeated args.
func RebindJDBC(query string, args []any) (string, []any, error) {
	scan, e := pgquery.Scan(query)
	if e != nil {
		return "", nil, e
	}
	var out strings.Builder
	values := []any{}
	position := 0
	for _, t := range scan.Tokens {
		start, end := int(t.Start), int(t.End)
		if t.Token == pgquery.Token_PARAM {
			index, e := strconv.Atoi(query[start+1 : end])
			if e != nil || index < 1 || index > len(args) {
				return "", nil, fmt.Errorf("参数编号无效")
			}
			out.WriteString(query[position:start])
			out.WriteByte('?')
			values = append(values, args[index-1])
			position = end
		} else if (t.Token == pgquery.Token_Op || t.Token == pgquery.Token_ASCII_63) && strings.Contains(query[start:end], "?") {
			out.WriteString(query[position:start])
			out.WriteString(strings.ReplaceAll(query[start:end], "?", "??"))
			position = end
		}
	}

	out.WriteString(query[position:])
	return out.String(), values, nil
}
