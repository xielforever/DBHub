package queryapi

import (
	"context"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/target"
)

func TestRealPolicyRewrite(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			c := &db.Connection{Type: dialect, SSLMode: "disable"}
			password := ""
			if dialect == "postgres" {
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("real database unavailable")
				}
				u, _ := url.Parse(dsn)
				c.Host = u.Hostname()
				c.Port, _ = strconv.Atoi(u.Port())
				c.Database = u.Path[1:]
				c.Username = u.User.Username()
				password, _ = u.User.Password()
			} else {
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("real database unavailable")
				}
				cfg, e := mysql.ParseDSN(dsn)
				if e != nil {
					t.Fatal(e)
				}
				host, port, _ := net.SplitHostPort(cfg.Addr)
				c.Host = host
				c.Port, _ = strconv.Atoi(port)
				c.Database = cfg.DBName
				c.Username = cfg.User
				password = cfg.Passwd
			}
			handle, e := target.OpenRelational(context.Background(), c, password, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer handle.Close()
			run := func(q string) {
				t.Helper()
				if _, e := target.Execute(context.Background(), handle, q, false); e != nil {
					t.Fatal(e)
				}
			}
			run("DROP TABLE IF EXISTS dbhub_policy_probe")
			run("CREATE TABLE dbhub_policy_probe(id INT, owner_id INT, email VARCHAR(100), secret VARCHAR(100))")
			defer run("DROP TABLE dbhub_policy_probe")
			run("INSERT INTO dbhub_policy_probe VALUES(1,1,'a','hidden'),(2,2,'b','hidden')")
			p := db.QueryPolicy{Table: "dbhub_policy_probe", Columns: map[string]string{"email": "masked", "secret": "hidden"}, Conditions: []db.RowCondition{{Column: "owner_id", Operator: "=", Value: 1}}}
			if dialect == "postgres" {
				p.Schema = "public"
			}
			for _, test := range []struct {
				query string
				args  []any
			}{{"SELECT * FROM dbhub_policy_probe WHERE owner_id=2 OR 1=1", nil}, {"SELECT x.* FROM dbhub_policy_probe AS x WHERE id = " + map[bool]string{true: "$1", false: "?"}[dialect == "postgres"], []any{1}}} {
				q, args, e := rewritePolicy(test.query, dialect, p, []string{"id", "owner_id", "email", "secret"}, 1, test.args)
				if e != nil {
					t.Fatal(e)
				}
				res, e := target.ExecuteWithOptions(context.Background(), handle, q, true, target.ExecuteOptions{Args: args})
				if e != nil {
					t.Fatal(e)
				}
				if len(res.Rows) != 1 || len(res.Columns) != 3 || res.Rows[0][2] != nil {
					t.Fatal("policy did not filter or mask")
				}
			}
			for _, q := range []string{"SELECT secret FROM dbhub_policy_probe", "SELECT * FROM dbhub_policy_probe JOIN dbhub_policy_probe b ON 1=1", "SELECT * FROM dbhub_policy_probe UNION SELECT * FROM dbhub_policy_probe", "SELECT (SELECT secret FROM dbhub_policy_probe) FROM dbhub_policy_probe", "SELECT coalesce(secret,'') FROM dbhub_policy_probe"} {
				rewritten, args, e := rewritePolicy(q, dialect, p, []string{"id", "owner_id", "email", "secret"}, 1, nil)
				if e == nil {
					_, e = target.ExecuteWithOptions(context.Background(), handle, rewritten, true, target.ExecuteOptions{Args: args})
				}
				if e == nil {
					t.Fatalf("bypass accepted: %s", q)
				}
			}
		})
	}
}
