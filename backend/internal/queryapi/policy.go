package queryapi

import (
	"context"
	"fmt"
	"strings"

	pgquery "github.com/pganalyze/pg_query_go/v5"
	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/sqlparse"
	my "github.com/xwb1989/sqlparser"
)

// Policy queries deliberately support a single base-table SELECT. Unsupported
// joins, CTEs, subqueries, functions and writes fail closed for restricted roles.
func (h *Handler) applyPolicies(ctx context.Context, handle *target.RelDB, user int64, role, query string, args []any) (string, []any, error) {
	if role == "admin" || h.controls == nil {
		return query, args, nil
	}
	policies, e := h.controls.Policies(ctx, handle.Conn.ID, role)
	if e != nil {
		return "", nil, e
	}
	if len(policies) == 0 {
		return query, args, nil
	}
	ast, e := sqlparse.Parse(query, handle.Conn.SQLDialect())
	if e != nil {
		return "", nil, e
	}
	if !ast.ReadOnly || ast.Kind != "select" || len(ast.Tables) != 1 {
		return "", nil, fmt.Errorf("此连接启用行列策略，仅允许单表 SELECT；不支持的查询已拒绝")
	}
	t := ast.Tables[0]
	schema := t.Schema
	if schema == "" && handle.Conn.SQLDialect() == "postgres" {
		schema = "public"
	}
	database := t.Database
	if database == "" {
		database = handle.Conn.Database
	}
	var policy *db.QueryPolicy
	for i := range policies {
		p := &policies[i]
		if p.Database == database && p.Schema == schema && p.Table == t.Name {
			policy = p
			break
		}
	}
	if policy == nil {
		return "", nil, fmt.Errorf("当前角色没有该表的访问策略")
	}
	cols, e := target.Columns(ctx, handle, database, schema, t.Name)
	if e != nil {
		return "", nil, e
	}
	names := []string{}
	for _, c := range cols {
		names = append(names, c.Name)
	}
	return rewritePolicy(query, handle.Conn.SQLDialect(), *policy, names, user, args)
}
func quoted(name, dialect string) string {
	if dialect == "mysql" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
func rewritePolicy(query, dialect string, p db.QueryPolicy, columns []string, user int64, args []any) (string, []any, error) {
	ast, e := sqlparse.Parse(query, dialect)
	if e != nil {
		return "", nil, e
	}
	errUnsupported := fmt.Errorf("行列策略仅支持单表 SELECT；JOIN、CTE、子查询、函数或写操作已拒绝")
	if ast.Kind != "select" || !ast.ReadOnly || len(ast.Tables) != 1 {
		return "", nil, errUnsupported
	}
	baseName := quoted(p.Table, dialect)
	if dialect == "mysql" && p.Database != "" {
		baseName = quoted(p.Database, dialect) + "." + baseName
	}
	if dialect != "mysql" && p.Schema != "" {
		baseName = quoted(p.Schema, dialect) + "." + baseName
	}
	projected := []string{}
	for _, c := range columns {
		switch p.Columns[c] {
		case "hidden":
			continue
		case "masked":
			projected = append(projected, "NULL AS "+quoted(c, dialect))
		default:
			projected = append(projected, quoted(c, dialect))
		}
	}
	if len(projected) == 0 {
		return "", nil, fmt.Errorf("该表没有可见列")
	}
	known := map[string]bool{}
	for _, c := range columns {
		known[c] = true
	}
	allArgs := append([]any{}, args...)
	named := map[string]any{}
	for i, v := range args {
		named[fmt.Sprintf("v%d", i+1)] = v
	}
	where := []string{}
	for i, condition := range p.Conditions {
		if !known[condition.Column] {
			return "", nil, fmt.Errorf("策略引用不存在的列")
		}
		op := strings.ToUpper(condition.Operator)
		switch op {
		case "=", "!=", ">", ">=", "<", "<=", "IS NULL", "IS NOT NULL":
		default:
			return "", nil, fmt.Errorf("策略操作符无效")
		}
		fragment := quoted(condition.Column, dialect) + " " + op
		if op != "IS NULL" && op != "IS NOT NULL" {
			value := condition.Value
			if value == "$user_id" {
				value = user
			}
			allArgs = append(allArgs, value)
			param := fmt.Sprintf("$%d", len(allArgs))
			if dialect == "mysql" {
				key := fmt.Sprintf("__dbhub_rls_%d", i)
				param = ":" + key
				named[key] = value
			}
			fragment += " " + param
		}
		where = append(where, fragment)
	}
	base := "SELECT " + strings.Join(projected, ",") + " FROM " + baseName
	if len(where) > 0 {
		base += " WHERE " + strings.Join(where, " AND ")
	}
	if dialect == "mysql" {
		root, ok := ast.MySQL.(*my.Select)
		if !ok || len(root.From) != 1 || root.Lock != "" {
			return "", nil, errUnsupported
		}
		table, ok := root.From[0].(*my.AliasedTableExpr)
		if !ok {
			return "", nil, errUnsupported
		}
		if _, ok = table.Expr.(my.TableName); !ok {
			return "", nil, errUnsupported
		}
		bad := false
		_ = my.Walk(func(n my.SQLNode) (bool, error) {
			switch n.(type) {
			case *my.Subquery, *my.FuncExpr, *my.Union, *my.JoinTableExpr:
				bad = true
			}
			return true, nil
		}, root)
		if bad {
			return "", nil, errUnsupported
		}
		parsed, e := my.Parse(base)
		if e != nil {
			return "", nil, e
		}
		sub := parsed.(*my.Select)
		alias := table.As
		if alias.IsEmpty() {
			alias = my.NewTableIdent(p.Table)
		}
		root.From = my.TableExprs{&my.AliasedTableExpr{Expr: &my.Subquery{Select: sub}, As: alias}}
		return sqlparse.BindNamed(my.String(root), "mysql", named)
	}
	bad := false
	rangeCount := 0
	sqlparse.Walk(ast.PG, func(key string, _ map[string]any) {
		switch key {
		case "CommonTableExpr", "JoinExpr", "SubLink", "RangeSubselect", "RangeFunction", "FuncCall", "SQLValueFunction", "IntoClause":
			bad = true
		case "RangeVar":
			rangeCount++
		}
	})
	if bad || rangeCount != 1 {
		return "", nil, errUnsupported
	}
	tree, e := pgquery.Parse(query)
	if e != nil {
		return "", nil, e
	}
	root := tree.Stmts[0].Stmt.GetSelectStmt()
	if root == nil || len(root.FromClause) != 1 || root.Op != pgquery.SetOperation_SETOP_NONE {
		return "", nil, errUnsupported
	}
	rv := root.FromClause[0].GetRangeVar()
	if rv == nil {
		return "", nil, errUnsupported
	}
	alias := p.Table
	if rv.Alias != nil && rv.Alias.Aliasname != "" {
		alias = rv.Alias.Aliasname
	}
	wrapper, e := pgquery.Parse("SELECT * FROM (" + base + ") AS " + quoted(alias, dialect))
	if e != nil {
		return "", nil, e
	}
	root.FromClause = wrapper.Stmts[0].Stmt.GetSelectStmt().FromClause
	rewritten, e := pgquery.Deparse(tree)
	return rewritten, allArgs, e
}
