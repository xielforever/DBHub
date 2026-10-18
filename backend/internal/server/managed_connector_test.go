package server

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestManagedReportJDBC(t *testing.T) {
	if os.Getenv("DBHUB_JDBC_URL") == "" {
		t.Skip("JDBC service not configured")
	}
	f := setupManaged(t)
	a := f.a
	a.server.Close()
	a.deps.Queries.Close()
	a.server = httptest.NewServer(NewRouter(jdbcConfig(os.Getenv("DBHUB_JDBC_URL")), slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps.UserRepo, a.deps))
	var driver int64
	for _, raw := range a.call("GET", "/connector-drivers/available", a.admin, nil, 200)["items"].([]any) {
		artifact := raw.(map[string]any)
		if artifact["name"] == "postgresql" {
			saved := a.call("POST", "/connector-drivers", a.admin, map[string]any{"visibility": "global", "artifact_id": artifact["id"], "name": "managed PostgreSQL"}, 201)
			driver = int64(saved["id"].(float64))
			break
		}
	}
	if driver == 0 {
		t.Fatal("real PostgreSQL JDBC artifact unavailable")
	}
	template := a.call("POST", "/datasource-templates", a.admin, templateBody("managed-pg-jdbc", "报表 JDBC", "PostgreSQL", driver, "postgres", "jdbc:postgresql://{host}:{port}/{database}", false), 201)
	original, password, e := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if e != nil {
		t.Fatal(e)
	}
	conn := a.call("POST", "/connections", a.admin, map[string]any{"name": "managed jdbc target", "type": "jdbc", "template_revision_id": template["id"], "parameters": map[string]string{"host": original.Host, "port": strconv.Itoa(original.Port), "database": original.Database}, "username": original.Username, "password": password, "ssl_mode": "disable"}, 201)
	definition := a.call("POST", fmt.Sprintf("/connections/%d/publish", int64(conn["id"].(float64))), a.admin, map[string]any{"visibility": "global"}, 201)
	f.definition = int64(definition["id"].(float64))
	personal := a.call("POST", fmt.Sprintf("/datasource-definitions/%d/connections", f.definition), a.developer, map[string]any{"name": "developer jdbc", "username": original.Username, "password": password, "confirmed": true}, 201)
	a.roleConnections[a.developer] = int64(personal["id"].(float64))
	account := a.call("POST", "/report-accounts", a.admin, map[string]any{"request_id": testRequestID(), "name": "JDBC 专用只读账号", "definition_revision_id": f.definition, "username": f.username, "password": f.password}, 200)
	f.account = int64(account["id"].(float64))
	id, _ := f.draft("SELECT current_user AS identity,current_setting('transaction_read_only') AS readonly,id FROM public." + f.view)
	review := f.submit(id, 1)
	f.approve(review, f.validate(review, []int64{3}, nil))
	f.release(id, 0, review, "publish")
	row := f.run(id, 1, 200)["rows"].([]any)[0].([]any)
	if row[0] != f.username || row[1] != "on" {
		t.Fatal("JDBC published identity or read-only mode incorrect", row)
	}
	// Exercise the Java reader's byte budget through the managed HTTP path.
	oversized, _ := f.draft("SELECT repeat('x',2097152) AS oversized")
	oversizedReview := f.submit(oversized, 1)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", oversizedReview), a.admin, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 400)
	a.call("PATCH", fmt.Sprintf("/connector-drivers/%d/status", driver), a.admin, map[string]any{"enabled": false}, 200)
	f.run(id, 1, 403)
}

func TestManagedReportMySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("real MySQL not configured")
	}
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if os.Getenv("MYSQL_ROOT_PASSWORD") == "" {
		t.Fatal("MySQL fixture root binding unavailable")
	}
	a := setupAPI(t)
	rootCfg := *cfg
	rootCfg.User, rootCfg.Passwd = "root", os.Getenv("MYSQL_ROOT_PASSWORD")
	root, e := sql.Open("mysql", rootCfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	name := fmt.Sprintf("mrep_%d", time.Now().UnixNano())
	password := strings.ReplaceAll(testRequestID(), "-", "")
	table, view := name+"_data", name+"_view"
	for _, statement := range []string{`CREATE USER '` + name + `'@'%' IDENTIFIED BY '` + password + `'`, `CREATE TABLE ` + table + `(id INT PRIMARY KEY,category VARCHAR(30),amount DECIMAL(18,2))`, `INSERT INTO ` + table + ` VALUES(1,'visible',12.34),(2,'hidden',56.78)`, `CREATE VIEW ` + view + ` AS SELECT category,amount FROM ` + table + ` WHERE id=1`, `GRANT SELECT ON ` + "`" + strings.ReplaceAll(cfg.DBName, "`", "``") + "`" + `.` + view + ` TO '` + name + `'@'%'`} {
		if _, e = root.Exec(statement); e != nil {
			t.Fatal(e)
		}
	}
	defer root.Exec(`DROP USER IF EXISTS '` + name + `'@'%'`)
	defer root.Exec(`DROP TABLE IF EXISTS ` + table)
	defer root.Exec(`DROP VIEW IF EXISTS ` + view)
	host, portText, e := net.SplitHostPort(cfg.Addr)
	if e != nil {
		t.Fatal(e)
	}
	port, _ := strconv.Atoi(portText)
	conn := a.call("POST", "/connections", a.admin, map[string]any{"name": "mysql managed target", "type": "mysql", "host": host, "port": port, "database": cfg.DBName, "username": cfg.User, "password": cfg.Passwd, "ssl_mode": "disable"}, 201)
	def := a.call("POST", fmt.Sprintf("/connections/%d/publish", int64(conn["id"].(float64))), a.admin, map[string]any{"visibility": "global"}, 201)
	definition := int64(def["id"].(float64))
	personal := a.call("POST", fmt.Sprintf("/datasource-definitions/%d/connections", definition), a.developer, map[string]any{"name": "developer mysql", "username": cfg.User, "password": cfg.Passwd, "confirmed": true}, 201)
	a.roleConnections[a.developer] = int64(personal["id"].(float64))
	account := a.call("POST", "/report-accounts", a.admin, map[string]any{"request_id": testRequestID(), "name": "MySQL 只读报表账号", "definition_revision_id": definition, "username": name, "password": password}, 200)
	f := &managedFixture{a: a, account: int64(account["id"].(float64)), definition: definition, username: name, password: password}
	id, _ := f.draft("SELECT CURRENT_USER() AS identity,category,amount FROM " + view)
	review := f.submit(id, 1)
	f.approve(review, f.validate(review, []int64{3}, nil))
	f.release(id, 0, review, "publish")
	rows := f.run(id, 1, 200)["rows"].([]any)
	row := rows[0].([]any)
	if len(rows) != 1 || !strings.HasPrefix(row[0].(string), name+"@") || row[2] != "12.34" {
		t.Fatal("MySQL service identity, scope, or precision incorrect", row)
	}
	denied, _ := f.draft("SELECT * FROM " + table)
	deniedReview := f.submit(denied, 1)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", deniedReview), a.admin, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 400)
}
