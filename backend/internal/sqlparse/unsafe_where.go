package sqlparse

import (
	"strconv"

	my "github.com/xwb1989/sqlparser"
)

// 只识别 AST 中可确定的字面量恒真条件；不尝试证明任意 SQL 的影响范围。
const unknownTruth = -1

func booleanTruth(value bool) int {
	if value {
		return 1
	}
	return 0
}
func combineTruth(a, b int, and bool) int {
	if and {
		if a == 0 || b == 0 {
			return 0
		}
		if a == 1 && b == 1 {
			return 1
		}
	} else {
		if a == 1 || b == 1 {
			return 1
		}
		if a == 0 && b == 0 {
			return 0
		}
	}
	return unknownTruth
}
func compareIntegers(a, b int64, op string) int {
	switch op {
	case "=":
		return booleanTruth(a == b)
	case "<>", "!=":
		return booleanTruth(a != b)
	case ">":
		return booleanTruth(a > b)
	case ">=":
		return booleanTruth(a >= b)
	case "<":
		return booleanTruth(a < b)
	case "<=":
		return booleanTruth(a <= b)
	}
	return unknownTruth
}
func pgInteger(value any) (int64, bool) {
	constant := object(object(value)["A_Const"])
	if integer, ok := constant["ival"]; ok {
		// PostgreSQL 的 ival 是 int32；更大的数保守地不在这里推断。
		n, _ := object(integer)["ival"].(float64)
		return int64(n), true
	}
	return 0, false
}
func pgTruth(value any) int {
	node := object(value)
	if b, ok := object(node["A_Const"])["boolval"]; ok {
		v, _ := object(b)["boolval"].(bool)
		return booleanTruth(v)
	}
	if expr := object(node["A_Expr"]); expr != nil {
		a, aOK := pgInteger(expr["lexpr"])
		b, bOK := pgInteger(expr["rexpr"])
		names, _ := expr["name"].([]any)
		if aOK && bOK && len(names) == 1 {
			return compareIntegers(a, b, text(object(object(names[0])["String"])["sval"]))
		}
	}
	if expr := object(node["BoolExpr"]); expr != nil {
		args, _ := expr["args"].([]any)
		op := text(expr["boolop"])
		if op == "NOT_EXPR" && len(args) == 1 {
			if v := pgTruth(args[0]); v != unknownTruth {
				return 1 - v
			}
		}
		if (op == "AND_EXPR" || op == "OR_EXPR") && len(args) > 0 {
			v := pgTruth(args[0])
			for _, arg := range args[1:] {
				v = combineTruth(v, pgTruth(arg), op == "AND_EXPR")
			}
			return v
		}
	}
	return unknownTruth
}
func myInteger(expr my.Expr) (int64, bool) {
	v, ok := expr.(*my.SQLVal)
	if !ok || v.Type != my.IntVal {
		return 0, false
	}
	n, err := strconv.ParseInt(string(v.Val), 10, 64)
	return n, err == nil
}
func myTruth(expr my.Expr) int {
	switch n := expr.(type) {
	case my.BoolVal:
		return booleanTruth(bool(n))
	case *my.ParenExpr:
		return myTruth(n.Expr)
	case *my.AndExpr:
		return combineTruth(myTruth(n.Left), myTruth(n.Right), true)
	case *my.OrExpr:
		return combineTruth(myTruth(n.Left), myTruth(n.Right), false)
	case *my.NotExpr:
		if v := myTruth(n.Expr); v != unknownTruth {
			return 1 - v
		}
	case *my.ComparisonExpr:
		a, aOK := myInteger(n.Left)
		b, bOK := myInteger(n.Right)
		if aOK && bOK {
			return compareIntegers(a, b, n.Operator)
		}
	case *my.SQLVal:
		if v, ok := myInteger(n); ok {
			return booleanTruth(v != 0)
		}
	}
	return unknownTruth
}
func unsafePGWhere(value any) bool       { return value == nil || pgTruth(value) == 1 }
func unsafeMyWhere(where *my.Where) bool { return where == nil || myTruth(where.Expr) == 1 }
