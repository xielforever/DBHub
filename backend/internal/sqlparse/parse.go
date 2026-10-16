// Package sqlparse provides actual PostgreSQL and Vitess-derived MySQL ASTs.
package sqlparse

import (
	"encoding/json"
	"fmt"
	"strings"

	pgquery "github.com/pganalyze/pg_query_go/v5"
	mysqlparser "github.com/xwb1989/sqlparser"
)

type Table struct{ Database, Schema, Name, Alias string }
type Statement struct {
	Kind        string
	ReadOnly    bool
	UnsafeWrite bool
	Tables      []Table
	Targets     []Table
	PG          map[string]any
	MySQL       mysqlparser.Statement
}

// Parse accepts exactly one statement. Unsupported syntax fails closed.
func Parse(sql, dialect string) (*Statement, error) {
	if dialect == "mysql" {
		return parseMySQL(sql)
	}
	raw, e := pgquery.ParseToJSON(sql)
	if e != nil {
		return nil, fmt.Errorf("PostgreSQL SQL 语法无效")
	}
	var tree map[string]any
	if json.Unmarshal([]byte(raw), &tree) != nil {
		return nil, fmt.Errorf("SQL AST 读取失败")
	}
	stmts, _ := tree["stmts"].([]any)
	if len(stmts) != 1 {
		return nil, fmt.Errorf("每次只允许执行一条 SQL；事务请使用事务接口")
	}
	node := object(object(stmts[0])["stmt"])
	s := &Statement{Kind: "utility", PG: tree}
	for key, value := range node {
		body := object(value)
		switch key {
		case "SelectStmt":
			s.Kind = "select"
			s.ReadOnly = true
		case "ExplainStmt":
			s.Kind = "explain"
			s.ReadOnly = true
		case "InsertStmt":
			s.Kind = "insert"
			s.Targets = append(s.Targets, pgTable(object(body["relation"])))
		case "UpdateStmt":
			s.Kind = "update"
			s.UnsafeWrite = unsafePGWhere(body["whereClause"])
			s.Targets = append(s.Targets, pgTable(object(body["relation"])))
		case "DeleteStmt":
			s.Kind = "delete"
			s.UnsafeWrite = unsafePGWhere(body["whereClause"])
			s.Targets = append(s.Targets, pgTable(object(body["relation"])))
		case "CreateTableAsStmt":
			s.Kind = "create"
			s.Targets = append(s.Targets, pgTable(object(object(body["into"])["rel"])))
		case "ViewStmt":
			s.Kind = "create"
			s.Targets = append(s.Targets, pgTable(object(body["view"])))
		case "CreateStmt", "IndexStmt", "AlterTableStmt", "DropStmt", "TruncateStmt", "GrantStmt", "VacuumStmt", "VariableShowStmt":
			s.Kind = "utility"
		default:
			return nil, fmt.Errorf("该语句类型不允许通过查询接口执行")
		}
	}
	ctes := map[string]bool{}
	Walk(tree, func(key string, value map[string]any) {
		if key == "CommonTableExpr" {
			ctes[text(value["ctename"])] = true
		}
	})
	Walk(tree, func(key string, value map[string]any) {
		if (key == "UpdateStmt" || key == "DeleteStmt") && unsafePGWhere(value["whereClause"]) {
			s.UnsafeWrite = true
		}
		switch key {
		case "InsertStmt", "UpdateStmt", "DeleteStmt", "MergeStmt", "CopyStmt", "CallStmt", "DoStmt", "TransactionStmt":
			s.ReadOnly = false
		}
		if key == "SelectStmt" && (value["intoClause"] != nil || value["lockingClause"] != nil) {
			s.ReadOnly = false
		}
		if key == "ExplainStmt" {
			options, _ := value["options"].([]any)
			for _, opt := range options {
				def := object(object(opt)["DefElem"])
				if strings.EqualFold(text(def["defname"]), "analyze") {
					s.ReadOnly = false
				}
			}
		}
		if key == "RangeVar" {
			t := pgTable(value)
			if t.Name != "" && !(t.Schema == "" && ctes[t.Name]) {
				s.Tables = append(s.Tables, t)
			}
		}
	})
	s.Tables = unique(s.Tables)
	s.Targets = unique(s.Targets)
	return s, nil
}

func parseMySQL(sql string) (*Statement, error) {
	pieces, splitErr := mysqlparser.SplitStatementToPieces(sql)
	if splitErr != nil || len(pieces) != 1 {
		return nil, fmt.Errorf("每次只允许执行一条 MySQL SQL")
	}
	ast, e := mysqlparser.Parse(sql)
	if e != nil {
		return nil, fmt.Errorf("MySQL SQL 语法无效或该语法尚不受支持")
	}
	s := &Statement{Kind: "utility", MySQL: ast}
	switch n := ast.(type) {
	case *mysqlparser.Select, *mysqlparser.Union, *mysqlparser.Show, *mysqlparser.OtherRead:
		s.Kind = "select"
		s.ReadOnly = true
	case *mysqlparser.Insert:
		s.Kind = "insert"
		s.Targets = append(s.Targets, myTable(n.Table))
	case *mysqlparser.Update:
		s.Kind = "update"
		s.UnsafeWrite = unsafeMyWhere(n.Where)
	case *mysqlparser.Delete:
		s.Kind = "delete"
		s.UnsafeWrite = unsafeMyWhere(n.Where)
	case *mysqlparser.DDL:
		s.Kind = "create"
		s.Targets = append(s.Targets, myTable(n.NewName))
		if n.NewName.IsEmpty() {
			s.Targets = []Table{myTable(n.Table)}
		}
	default:
		return nil, fmt.Errorf("该语句类型不允许通过查询接口执行")
	}
	_ = mysqlparser.Walk(func(node mysqlparser.SQLNode) (bool, error) {
		if aliased, ok := node.(*mysqlparser.AliasedTableExpr); ok {
			if t, ok := aliased.Expr.(mysqlparser.TableName); ok {
				table := myTable(t)
				table.Alias = aliased.As.String()
				s.Tables = append(s.Tables, table)
			}
		}
		return true, nil
	}, ast)
	if s.Kind == "update" || s.Kind == "delete" {
		s.Targets = append(s.Targets, s.Tables...)
	}
	s.Tables = unique(append(s.Tables, s.Targets...))
	s.Targets = unique(s.Targets)
	return s, nil
}

func pgTable(m map[string]any) Table {
	return Table{Database: text(m["catalogname"]), Schema: text(m["schemaname"]), Name: text(m["relname"]), Alias: text(object(m["alias"])["aliasname"])}
}
func myTable(t mysqlparser.TableName) Table {
	return Table{Database: t.Qualifier.String(), Name: t.Name.String()}
}
func object(value any) map[string]any { m, _ := value.(map[string]any); return m }
func text(value any) string           { s, _ := value.(string); return s }
func unique(in []Table) []Table {
	out := []Table{}
	seen := map[string]bool{}
	for _, t := range in {
		key := t.Database + "\x00" + t.Schema + "\x00" + t.Name
		if t.Name != "" && !seen[key] {
			seen[key] = true
			out = append(out, t)
		}
	}
	return out
}

// Walk visits PostgreSQL JSON node objects without using text matching.
func Walk(value any, visit func(string, map[string]any)) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if m, ok := child.(map[string]any); ok {
				visit(key, m)
			}
			Walk(child, visit)
		}
	case []any:
		for _, child := range v {
			Walk(child, visit)
		}
	}
}
