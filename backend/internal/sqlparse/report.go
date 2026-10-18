package sqlparse

import (
	"fmt"

	mysqlparser "github.com/xwb1989/sqlparser"
)

// ValidateReport accepts a single SELECT, including read-only CTEs and unions.
// Execution must ALSO use a database read-only transaction: functions can have
// effects that cannot be established by inspecting their invocation's AST.
func ValidateReport(query, dialect string) error {
	bound, _, err := BindNamed(query, dialect, nil)
	if err != nil {
		return err
	}
	stmt, err := Parse(bound, dialect)
	if err != nil {
		return err
	}
	if !stmt.ReadOnly || stmt.Kind != "select" {
		return fmt.Errorf("报表仅允许单条只读 SELECT；不允许写入、锁定、事务或管理语句")
	}
	if dialect == "mysql" {
		switch stmt.MySQL.(type) {
		case *mysqlparser.Select, *mysqlparser.Union:
		default:
			return fmt.Errorf("报表仅允许单条只读 SELECT")
		}
		locked := false
		_ = mysqlparser.Walk(func(node mysqlparser.SQLNode) (bool, error) {
			switch value := node.(type) {
			case *mysqlparser.Select:
				locked = locked || value.Lock != ""
			case *mysqlparser.Union:
				locked = locked || value.Lock != ""
			}
			return true, nil
		}, stmt.MySQL)
		if locked {
			return fmt.Errorf("报表不允许锁定查询")
		}
	}
	return nil
}
