package lineage

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/user/dbhub/internal/sqlparse"
)

type TableRef struct{ Database, Schema, Table string }

func (t TableRef) String() string {
	parts := []string{}
	for _, p := range []string{t.Database, t.Schema, t.Table} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ".")
}

type ParseResult struct {
	Targets, Sources, All []TableRef
	Kind                  string
}

func Fingerprint(sql string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(sql)))
	return fmt.Sprintf("%x", h[:16])
}

// ParseTables 使用实际 AST；不支持的语法返回错误，不生成近似血缘。
func ParseTables(query, dialect string) (ParseResult, error) {
	ast, e := sqlparse.Parse(query, dialect)
	if e != nil {
		return ParseResult{}, e
	}
	pr := ParseResult{Kind: ast.Kind, Targets: []TableRef{}, Sources: []TableRef{}, All: []TableRef{}}
	targets := map[string]bool{}
	ref := func(t sqlparse.Table) TableRef { return TableRef{t.Database, t.Schema, t.Name} }
	for _, t := range ast.Targets {
		r := ref(t)
		targets[r.String()] = true
		pr.Targets = append(pr.Targets, r)
	}
	for _, t := range ast.Tables {
		r := ref(t)
		pr.All = append(pr.All, r)
		if !targets[r.String()] {
			pr.Sources = append(pr.Sources, r)
		}
	}
	return pr, nil
}
