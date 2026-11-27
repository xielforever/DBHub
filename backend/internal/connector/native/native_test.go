package native

import (
	"context"
	"errors"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/user/dbhub/internal/connector"
)

func TestNormalizeMySQLExecutionError(t *testing.T) {
	const driverMessage = "模拟的驱动原始消息"
	unknownColumn := &mysql.MySQLError{Number: 1054, SQLState: [5]byte{'4', '2', 'S', '2', '2'}, Message: driverMessage}
	selectDenied := &mysql.MySQLError{Number: 1142, SQLState: [5]byte{'4', '2', '0', '0', '0'}, Message: driverMessage}
	for _, test := range []struct {
		name    string
		err     error
		query   bool
		message string
	}{
		{"unknown column", unknownColumn, true, "MySQL 查询引用的字段不存在（1054）"},
		{"select denied", selectDenied, true, "MySQL 数据库账号没有目标对象的查询权限（1142）"},
		{"unknown table", &mysql.MySQLError{Number: 1146, SQLState: [5]byte{'4', '2', 'S', '0', '2'}}, true, "MySQL 查询引用的表不存在（1146）"},
		{"parse error", &mysql.MySQLError{Number: 1064, SQLState: [5]byte{'4', '2', '0', '0', '0'}}, true, "MySQL 返回 SQL 语法错误"},
		{"syntax error", &mysql.MySQLError{Number: 1149, SQLState: [5]byte{'4', '2', '0', '0', '0'}}, true, "MySQL 返回 SQL 语法错误"},
		{"write", unknownColumn, false, ""},
		{"write denied", selectDenied, false, ""},
		{"timeout", errors.Join(unknownColumn, context.DeadlineExceeded), true, ""},
		{"permission error with timeout", errors.Join(selectDenied, context.DeadlineExceeded), true, ""},
		{"cancellation", errors.Join(unknownColumn, context.Canceled), true, ""},
		{"server shutdown", &mysql.MySQLError{Number: 1053, SQLState: [5]byte{'0', '8', 'S', '0', '1'}}, true, ""},
		{"unmatched state", &mysql.MySQLError{Number: 1054}, true, ""},
		{"permission code without matching state", &mysql.MySQLError{Number: 1142}, true, ""},
		{"no driver rejection", errors.New(driverMessage), true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeExecutionError(test.err, test.query)
			if test.message == "" {
				if got != test.err {
					t.Fatalf("expected original error, got %v", got)
				}
				return
			}
			var outcome *connector.Error
			if !errors.As(got, &outcome) || !outcome.Definite || outcome.Kind != "statement_rejected" ||
				outcome.Message != test.message || !errors.Is(got, test.err) {
				t.Fatalf("unexpected rejection: %#v", got)
			}
		})
	}
}
