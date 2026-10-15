package target

import (
	"context"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/sqlparse"
)

func integrationConnection(t *testing.T, dialect string) (*db.Connection, string) {
	t.Helper()
	c := &db.Connection{Type: dialect, SSLMode: "disable", ConnectionTimeout: 2}
	if dialect == "postgres" {
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("TEST_POSTGRES_DSN not configured")
		}
		u, e := url.Parse(dsn)
		if e != nil {
			t.Fatal("invalid test DSN")
		}
		c.Host = u.Hostname()
		c.Port, _ = strconv.Atoi(u.Port())
		c.Database = u.Path[1:]
		c.Username = u.User.Username()
		password, _ := u.User.Password()
		return c, password
	}
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not configured")
	}
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal("invalid test DSN")
	}
	host, port, e := net.SplitHostPort(cfg.Addr)
	if e != nil {
		t.Fatal(e)
	}
	c.Host = host
	c.Port, _ = strconv.Atoi(port)
	c.Database = cfg.DBName
	c.Username = cfg.User
	return c, cfg.Passwd
}
func TestIntegrationRealSQLAndTransactions(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			c, password := integrationConnection(t, dialect)
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			r, e := OpenRelational(ctx, c, password, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			run := func(q string) *ExecResult {
				t.Helper()
				v, e := Execute(ctx, r, q, false)
				if e != nil {
					t.Fatalf("statement failed: %v", e)
				}
				return v
			}
			run("DROP TABLE IF EXISTS dbhub_probe_child")
			run("DROP TABLE IF EXISTS dbhub_probe")
			run("CREATE TABLE dbhub_probe(id INTEGER PRIMARY KEY,note VARCHAR(100) NOT NULL)")
			defer func() {
				_, _ = Execute(context.Background(), r, "DROP TABLE IF EXISTS dbhub_probe_child", false)
				_, _ = Execute(context.Background(), r, "DROP TABLE IF EXISTS dbhub_probe", false)
			}()
			run("CREATE TABLE dbhub_probe_child(id INTEGER PRIMARY KEY,parent_id INTEGER,CONSTRAINT probe_parent FOREIGN KEY(parent_id) REFERENCES dbhub_probe(id))")
			if run("INSERT INTO dbhub_probe(id,note) VALUES (1,'alpha'),(2,'beta')").AffectedRows != 2 {
				t.Fatal("write count must come from database")
			}
			values, e := ExecuteWithOptions(ctx, r, "SELECT id,note FROM dbhub_probe ORDER BY id", false, ExecuteOptions{MaxRows: 1})
			if e != nil || len(values.Rows) != 1 || !values.Truncated || str(values.Rows[0][1]) != "alpha" {
				t.Fatalf("actual row/limit failed: %v", e)
			}
			if _, e = Execute(ctx, r, "SELECT missing_column FROM dbhub_probe", false); e == nil {
				t.Fatal("invalid SQL returned fabricated success")
			}
			if _, e = Execute(ctx, r, "UPDATE dbhub_probe SET note='unsafe'", false); e == nil {
				t.Fatal("unsafe write accepted")
			}
			if _, e = Execute(ctx, r, "DELETE FROM dbhub_probe WHERE id=1", true); e == nil {
				t.Fatal("readonly write accepted")
			}
			if _, e = Execute(ctx, r, "SELECT id FROM dbhub_probe; DELETE FROM dbhub_probe", true); e == nil {
				t.Fatal("multi-statement bypass accepted")
			}
			if dialect == "postgres" {
				if _, e = Execute(ctx, r, "WITH changed AS (DELETE FROM dbhub_probe RETURNING *) SELECT * FROM changed", false); e == nil {
					t.Fatal("unsafe write inside CTE bypassed guard")
				}
			}
			if dialect == "postgres" {
				if _, e = Execute(ctx, r, "WITH x AS (DELETE FROM dbhub_probe RETURNING id) SELECT * FROM x", true); e == nil {
					t.Fatal("write CTE bypass accepted")
				}
			}
			if e = r.Begin(ctx, false); e != nil {
				t.Fatal(e)
			}
			run("INSERT INTO dbhub_probe VALUES(3,'rollback')")
			if e = r.Rollback(ctx); e != nil {
				t.Fatal(e)
			}
			if len(run("SELECT id FROM dbhub_probe WHERE id=3").Rows) != 0 {
				t.Fatal("rollback did not undo write")
			}
			if e = r.Begin(ctx, false); e != nil {
				t.Fatal(e)
			}
			run("INSERT INTO dbhub_probe VALUES(4,'commit')")
			if e = r.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			other, e := OpenRelational(ctx, c, password, nil)
			if e != nil {
				t.Fatal(e)
			}
			v, e := Execute(ctx, other, "SELECT note FROM dbhub_probe WHERE id=4", true)
			other.Close()
			if e != nil || len(v.Rows) != 1 || str(v.Rows[0][0]) != "commit" {
				t.Fatal("commit not visible on separate connection")
			}
			query, args, e := sqlparse.BindNamed("SELECT :value AS value", dialect, map[string]any{"value": "x'; DROP TABLE dbhub_probe;--"})
			if e != nil {
				t.Fatal(e)
			}
			v, e = ExecuteWithOptions(ctx, r, query, true, ExecuteOptions{Args: args})
			if e != nil || str(v.Rows[0][0]) != "x'; DROP TABLE dbhub_probe;--" {
				t.Fatal("parameter binding changed literal")
			}
			schema := "public"
			if dialect == "mysql" {
				schema = c.Database
			}
			cols, rows, total, e := PreviewTable(ctx, r, c.Database, schema, "dbhub_probe", 2, 0)
			if e != nil || len(cols) != 2 || len(rows) != 2 || total != 3 {
				t.Fatalf("real preview failed: %v", e)
			}
			metadata, e := IntrospectSchema(ctx, r, c.Database, schema)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, table := range metadata {
				if table.Name == "dbhub_probe_child" {
					found = true
					if len(table.Columns) != 2 || len(table.Keys) < 2 || table.DDL == "" {
						t.Fatal("incomplete actual metadata")
					}
				}
			}
			if !found {
				t.Fatal("fixture absent from metadata")
			}
			bad := *c
			bad.Host = "127.0.0.1"
			bad.Port = 1
			if _, e = OpenRelational(ctx, &bad, password, nil); e == nil {
				t.Fatal("invalid connection accepted")
			}
			if _, e = Test(ctx, &bad, password, nil); e == nil {
				t.Fatal("failed test returned mock success")
			}
		})
	}
}
