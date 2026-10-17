package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/connector/jdbc"
)

func jdbcConfig(endpoint string) *config.Config {
	return &config.Config{Env: "development", SecretKey: []byte("test-main-key-32-bytes-long-123456789"), AccessTTL: time.Hour, RefreshTTL: time.Hour, JDBCURL: endpoint, JDBCToken: os.Getenv("DBHUB_JDBC_TOKEN")}
}
func connectorFixture(t *testing.T) *apiTest {
	t.Helper()
	if os.Getenv("DBHUB_JDBC_URL") == "" {
		t.Skip("JDBC service not configured")
	}
	a := setupAPI(t)
	a.server.Close()
	a.deps.Queries.Close()
	a.server = httptest.NewServer(NewRouter(jdbcConfig(os.Getenv("DBHUB_JDBC_URL")), slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps.UserRepo, a.deps))
	return a
}
func templateBody(key, name, product string, driver int64, dialect, uri string, h2 bool) map[string]any {
	fields := []any{}
	if !h2 {
		fields = append(fields, map[string]any{"name": "host", "label": "主机", "type": "string", "required": true}, map[string]any{"name": "port", "label": "端口", "type": "integer", "required": true})
	}
	fields = append(fields, map[string]any{"name": "database", "label": "数据库", "type": "string", "required": true})
	return map[string]any{"template_key": key, "name": name, "product": product, "driver_id": driver, "dialect": dialect, "url_template": uri, "fields": fields}
}

func TestConnectorJDBCRealHTTP(t *testing.T) {
	a := connectorFixture(t)
	artifacts := a.call("GET", "/connector-drivers/available", a.admin, nil, 200)["items"].([]any)
	drivers := map[string]int64{}
	hashes := map[string]string{}
	for _, raw := range artifacts {
		v := raw.(map[string]any)
		hashes[v["name"].(string)] = v["sha256"].(string)
		d := a.call("POST", "/connector-drivers", a.admin, map[string]any{"artifact_id": v["id"], "name": v["name"]}, 201)
		drivers[v["name"].(string)] = int64(d["id"].(float64))
	}
	t.Run("admin-only-registration-and-immutable-revisions", func(t *testing.T) {
		a.t = t
		a.call("GET", "/connector-drivers/available", a.readonly, nil, 403)
		a.call("POST", "/connector-drivers", a.developer, map[string]any{"artifact_id": "h2-2.4.240", "name": "bad"}, 403)
		a.call("POST", "/connector-drivers", a.admin, map[string]any{"artifact_id": "h2-2.4.240", "name": "duplicate"}, 409)
		a.call("POST", "/connector-drivers", a.admin, map[string]any{"artifact_id": "missing", "name": "bad"}, 400)
		a.call("POST", "/datasource-templates", a.readonly, map[string]any{}, 403)
	})
	h2Body := templateBody("h2-example", "H2 结构", "H2", drivers["h2"], "", "jdbc:h2:mem:{database};DB_CLOSE_DELAY=-1", true)
	h2Template := a.call("POST", "/datasource-templates", a.admin, h2Body, 201)
	h2ID := int64(h2Template["id"].(float64))
	database := fmt.Sprintf("connector_%d", time.Now().UnixNano())
	password := "integration-h2-only"
	client := jdbc.NewClient(os.Getenv("DBHUB_JDBC_URL"), os.Getenv("DBHUB_JDBC_TOKEN"))
	session, e := (jdbc.Driver{Client: client}).Open(context.Background(), connector.OpenSpec{Source: connector.Source{Database: database}, Username: "sa", Password: password, SSLMode: "disable", JDBC: &connector.JDBCSpec{ArtifactID: "h2-2.4.240", SHA256: hashes["h2"], URL: "jdbc:h2:mem:" + database + ";DB_CLOSE_DELAY=-1", Capabilities: connector.Capabilities{SQL: true}}})
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close(context.Background())
	for _, sql := range []string{"CREATE SCHEMA APP", "CREATE TABLE APP.CONNECTOR_SAMPLE (ID BIGINT, AMOUNT DECIMAL(24,8), LABEL VARCHAR(80), PRIMARY KEY (LABEL,ID))", "CREATE VIEW APP.SAMPLE_VIEW AS SELECT ID FROM APP.CONNECTOR_SAMPLE"} {
		if _, e = session.(connector.SQLExecutor).Execute(context.Background(), connector.ExecuteRequest{SQL: sql}); e != nil {
			t.Fatal(e)
		}
	}
	h2Source := map[string]any{"name": "custom-h2", "type": "jdbc", "template_revision_id": h2ID, "parameters": map[string]string{"database": database}, "username": "sa", "password": password, "ssl_mode": "disable", "environment": "test"}
	h2 := a.call("POST", "/connections", a.admin, h2Source, 201)
	sourceID := int64(h2["id"].(float64))
	sourcePath := fmt.Sprintf("/connections/%d", sourceID)
	t.Run("custom-h2-connects-browses-and-collects-without-sql", func(t *testing.T) {
		a.t = t
		a.call("POST", sourcePath+"/test", a.readonly, nil, 200)
		caps := a.call("GET", sourcePath+"/capabilities", a.readonly, nil, 200)
		if caps["sql"] != false || caps["metadata"] != true {
			t.Fatal(caps)
		}
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": sourceID, "sql": "SELECT 1"}, 400)
		tables := a.call("GET", fmt.Sprintf("/metadata/tables?connection_id=%d&database=%s&schema=APP", sourceID, database), a.readonly, nil, 200)["items"].([]any)
		if len(tables) != 2 {
			t.Fatal(tables)
		}
		columns := a.call("GET", fmt.Sprintf("/metadata/columns?connection_id=%d&database=%s&schema=APP&table=CONNECTOR_SAMPLE", sourceID, database), a.readonly, nil, 200)["items"].([]any)
		if len(columns) != 3 || columns[0].(map[string]any)["is_primary"] != true {
			t.Fatal(columns)
		}
		sync := a.call("POST", "/assets/sync", a.admin, map[string]any{"connection_id": sourceID}, 200)
		if sync["tables"] != float64(1) || sync["views"] != float64(1) {
			t.Fatal(sync)
		}
		snapshot := a.call("GET", fmt.Sprintf("/assets/table?connection_id=%d&database=%s&schema=APP&table=CONNECTOR_SAMPLE", sourceID, database), a.readonly, nil, 200)["snapshot"].(map[string]any)
		status := snapshot["metadata_status"].(map[string]any)
		if status["ddl"] != "unsupported" {
			t.Fatal(snapshot)
		}
		keys := snapshot["raw_keys"].([]any)
		primary := keys[0].(map[string]any)["columns"].([]any)
		if len(primary) != 2 || primary[0] != "LABEL" || primary[1] != "ID" {
			t.Fatal("composite primary key order was lost", keys)
		}
		raw, _ := json.Marshal(a.call("GET", sourcePath, a.admin, nil, 200))
		if strings.Contains(string(raw), password) {
			t.Fatal("secret leaked")
		}
	})
	t.Run("revision-pinning-disabled-state-and-reference-protection", func(t *testing.T) {
		a.t = t
		next := a.call("PUT", fmt.Sprintf("/datasource-templates/%d", h2ID), a.admin, h2Body, 201)
		if next["revision"] != float64(2) {
			t.Fatal(next)
		}
		old := a.call("GET", sourcePath, a.admin, nil, 200)
		if old["template"].(map[string]any)["revision"] != float64(1) {
			t.Fatal("old instance was rebound")
		}
		a.call("DELETE", fmt.Sprintf("/datasource-templates/%d", h2ID), a.admin, nil, 409)
		a.call("DELETE", fmt.Sprintf("/connector-drivers/%d", drivers["h2"]), a.admin, nil, 409)
		a.call("PATCH", fmt.Sprintf("/datasource-templates/%d/status", h2ID), a.admin, map[string]any{"enabled": false}, 200)
		a.call("POST", "/connections", a.admin, h2Source, 400)
		caps := a.call("GET", sourcePath+"/capabilities", a.readonly, nil, 200)
		if caps["metadata"] != false {
			t.Fatal(caps)
		}
		a.call("PATCH", fmt.Sprintf("/datasource-templates/%d/status", h2ID), a.admin, map[string]any{"enabled": true}, 200)
	})

	original, password, e := a.deps.Conns.GetSecrets(context.Background(), a.connID)
	if e != nil {
		t.Fatal(e)
	}
	pgBody := templateBody("pg-jdbc", "PG JDBC", "PostgreSQL", drivers["postgresql"], "postgres", "jdbc:postgresql://{host}:{port}/{database}", false)
	pgTemplate := a.call("POST", "/datasource-templates", a.admin, pgBody, 201)
	pgSource := map[string]any{"name": "pg-jdbc-instance", "type": "jdbc", "template_revision_id": pgTemplate["id"], "parameters": map[string]string{"host": original.Host, "port": strconv.Itoa(original.Port), "database": original.Database}, "username": original.Username, "password": password, "ssl_mode": "disable", "environment": "test"}
	pg := a.call("POST", "/connections", a.admin, pgSource, 201)
	pgID := int64(pg["id"].(float64))
	pgPath := fmt.Sprintf("/connections/%d", pgID)
	t.Run("jdbc-postgres-parameters-types-and-saved-check", func(t *testing.T) {
		a.t = t
		a.call("POST", pgPath+"/test", a.admin, nil, 200)
		got := a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": pgID, "sql": "SELECT 1234567890123456789::bigint AS big, 1234567890.123456789::numeric(30,9) AS dec, :value::text AS param, '{\"x\":1}'::jsonb ? :key AS hit", "params": map[string]any{"value": "O'Reilly", "key": "x"}}, 200)
		row := got["rows"].([]any)[0].([]any)
		if row[0] != "1234567890123456789" || row[1] != "1234567890.123456789" || row[2] != "O'Reilly" || row[3] != true {
			t.Fatal(row)
		}
		inferred := a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": pgID, "sql": "SELECT id FROM " + a.table + " WHERE id=:id", "params": map[string]any{"id": "1"}}, 200)
		if inferred["rows"].([]any)[0].([]any)[0] != "1" {
			t.Fatal(inferred)
		}
		pgSource["password"] = ""
		a.call("POST", pgPath+"/test", a.admin, pgSource, 200)
		changed := map[string]any{}
		for k, v := range pgSource {
			changed[k] = v
		}
		changed["parameters"] = map[string]string{"host": original.Host, "port": "1", "database": original.Database}
		a.call("POST", pgPath+"/test", a.admin, changed, 400)
	})
	t.Run("jdbc-writes-are-not-replayed-and-readonly-is-enforced", func(t *testing.T) {
		a.t = t
		request := testRequestID()
		body := map[string]any{"connection_id": pgID, "request_id": request, "sql": "UPDATE " + a.table + " SET owner_id=owner_id+1 WHERE id=1"}
		var wg sync.WaitGroup
		responses := make(chan executionResponse, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				responses <- executionRequest(a.server.Client(), a.server.URL, "POST", "/query/execute", a.admin, body)
			}()
		}
		wg.Wait()
		close(responses)
		success, duplicates := 0, 0
		for r := range responses {
			if r.status == 200 {
				success++
			} else if r.status == 409 {
				duplicates++
			} else {
				t.Fatal(r)
			}
		}
		if success != 1 || duplicates != 11 {
			t.Fatal(success, duplicates)
		}
		a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": pgID, "sql": "DELETE FROM " + a.table + " WHERE id=1"}, 400)
		got := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "sql": "SELECT owner_id FROM " + a.table + " WHERE id=1"}, 200)
		if got["rows"].([]any)[0].([]any)[0] != "2" {
			t.Fatal(got)
		}
	})
	t.Run("jdbc-transaction-pins-session-commits-and-rolls-back", func(t *testing.T) {
		a.t = t
		tx := a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": pgID}, 200)
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "transaction_id": tx["transaction_id"], "sql": "UPDATE " + a.table + " SET owner_id=99 WHERE id=1"}, 200)
		inside := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "transaction_id": tx["transaction_id"], "sql": "SELECT owner_id FROM " + a.table + " WHERE id=1"}, 200)
		if inside["rows"].([]any)[0].([]any)[0] != "99" {
			t.Fatal(inside)
		}
		a.call("POST", "/query/transaction/rollback", a.admin, map[string]any{"connection_id": pgID, "transaction_id": tx["transaction_id"]}, 200)
		got := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "sql": "SELECT owner_id FROM " + a.table + " WHERE id=1"}, 200)
		if got["rows"].([]any)[0].([]any)[0] != "2" {
			t.Fatal(got)
		}
		tx = a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": pgID}, 200)
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "transaction_id": tx["transaction_id"], "sql": "UPDATE " + a.table + " SET owner_id=7 WHERE id=1"}, 200)
		a.call("POST", "/query/transaction/commit", a.admin, map[string]any{"connection_id": pgID, "transaction_id": tx["transaction_id"]}, 200)
		committed := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "sql": "SELECT owner_id FROM " + a.table + " WHERE id=1"}, 200)
		if committed["rows"].([]any)[0].([]any)[0] != "7" {
			t.Fatal(committed)
		}
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "sql": "UPDATE " + a.table + " SET owner_id=2 WHERE id=1"}, 200)
	})
	t.Run("lost-jdbc-write-response-remains-unknown-without-retry", func(t *testing.T) {
		a.t = t
		var sent atomic.Int32
		forward := http.Client{Timeout: 10 * time.Second}
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, _ := http.NewRequestWithContext(r.Context(), "POST", os.Getenv("DBHUB_JDBC_URL")+r.URL.Path, r.Body)
			req.Header = r.Header.Clone()
			res, e := forward.Do(req)
			if e != nil {
				http.Error(w, "transport", 503)
				return
			}
			defer res.Body.Close()
			raw, _ := io.ReadAll(res.Body)
			if r.URL.Path == "/v1/execute" {
				sent.Add(1)
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.WriteHeader(res.StatusCode)
			w.Write(raw)
		}))
		defer proxy.Close()
		deps := *a.deps
		router := httptest.NewServer(NewRouter(jdbcConfig(proxy.URL), slog.New(slog.NewTextHandler(io.Discard, nil)), deps.UserRepo, &deps))
		defer router.Close()
		defer deps.Queries.Close()
		id := testRequestID()
		body := map[string]any{"connection_id": pgID, "request_id": id, "sql": "UPDATE " + a.table + " SET owner_id=owner_id+1 WHERE id=1"}
		res := executionRequest(router.Client(), router.URL, "POST", "/query/execute", a.admin, body)
		var receipt map[string]any
		json.Unmarshal(res.data, &receipt)
		if res.status != 400 || receipt["status"] != "unknown" {
			t.Fatal(res)
		}
		duplicate := executionRequest(router.Client(), router.URL, "POST", "/query/execute", a.admin, body)
		if duplicate.status != 409 || sent.Load() != 1 {
			t.Fatal(duplicate, sent.Load())
		}
		got := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "sql": "SELECT owner_id FROM " + a.table + " WHERE id=1"}, 200)
		if got["rows"].([]any)[0].([]any)[0] != "3" {
			t.Fatal(got)
		}
	})
	t.Run("jdbc-mysql-real-driver-and-server-readonly", func(t *testing.T) {
		a.t = t
		cfg, e := mysql.ParseDSN(os.Getenv("TEST_MYSQL_DSN"))
		if e != nil || cfg.DBName == "" {
			t.Fatal("real MySQL DSN required")
		}
		host, port, e := net.SplitHostPort(cfg.Addr)
		if e != nil {
			t.Fatal(e)
		}
		body := templateBody("mysql-jdbc", "MySQL JDBC", "MySQL", drivers["mysql"], "mysql", "jdbc:mysql://{host}:{port}/{database}", false)
		tpl := a.call("POST", "/datasource-templates", a.admin, body, 201)
		source := a.call("POST", "/connections", a.admin, map[string]any{"name": "mysql-jdbc", "type": "jdbc", "template_revision_id": tpl["id"], "parameters": map[string]string{"host": host, "port": port, "database": cfg.DBName}, "username": cfg.User, "password": cfg.Passwd, "ssl_mode": "disable"}, 201)
		id := int64(source["id"].(float64))
		a.call("POST", fmt.Sprintf("/connections/%d/test", id), a.readonly, nil, 200)
		table := fmt.Sprintf("jdbc_mysql_%d", time.Now().UnixNano())
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": id, "sql": "CREATE TABLE " + table + " (id INT PRIMARY KEY)"}, 200)
		defer a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": id, "sql": "DROP TABLE " + table}, 200)
		got := a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": id, "sql": "SELECT :value AS value", "params": map[string]any{"value": "真实 JDBC"}}, 200)
		if got["rows"].([]any)[0].([]any)[0] != "真实 JDBC" {
			t.Fatal(got)
		}
		direct, e := (jdbc.Driver{Client: client}).Open(context.Background(), connector.OpenSpec{Source: connector.Source{Database: cfg.DBName}, Username: cfg.User, Password: cfg.Passwd, SSLMode: "disable", JDBC: &connector.JDBCSpec{ArtifactID: "mysql-8.4.0", SHA256: hashes["mysql"], URL: "jdbc:mysql://" + cfg.Addr + "/" + cfg.DBName, Dialect: "mysql", Capabilities: connector.Capabilities{SQL: true, Transactions: true}}})
		if e != nil {
			t.Fatal(e)
		}
		defer direct.Close(context.Background())
		if _, e = direct.(connector.SQLExecutor).Execute(context.Background(), connector.ExecuteRequest{SQL: "INSERT INTO " + table + " VALUES(1)", ReadOnly: true}); e == nil {
			t.Fatal("server readonly was not enforced")
		}
		got = a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": id, "sql": "SELECT COUNT(*) FROM " + table}, 200)
		if got["rows"].([]any)[0].([]any)[0] != "0" {
			t.Fatal(got)
		}
		a.call("GET", fmt.Sprintf("/metadata/columns?connection_id=%d&database=%s&table=%s", id, cfg.DBName, table), a.readonly, nil, 200)
	})
	t.Run("jdbc-null-binary-time-and-field-budget", func(t *testing.T) {
		a.t = t
		got := a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": pgID, "sql": "SELECT NULL::text AS sqlnull, 'null'::jsonb AS jsonnull, decode('0102','hex') AS binary, '2024-06-01 12:00:00'::timestamp AS local_time"}, 200)
		row := got["rows"].([]any)[0].([]any)
		if row[0] != nil || row[1] != "null" || row[2].(map[string]any)["value"] != "AQI=" || row[3] != "2024-06-01 12:00:00" {
			t.Fatal(row)
		}
		a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": pgID, "sql": "SELECT repeat('x',1100000)"}, 400)
	})
	t.Run("jdbc-honors-an-execution-budget-above-thirty-seconds", func(t *testing.T) {
		a.t = t
		a.call("PUT", "/query/settings", a.admin, map[string]any{"timeout_ms": 40000, "max_rows": 1000, "block_unsafe_write": true}, 200)
		defer a.call("PUT", "/query/settings", a.admin, map[string]any{"timeout_ms": 30000, "max_rows": 1000, "block_unsafe_write": true}, 200)
		got := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "sql": "SELECT pg_sleep(31), 1 AS completed", "timeout_ms": 40000}, 200)
		if got["rows"].([]any)[0].([]any)[1] != "1" {
			t.Fatal(got)
		}
	})
	t.Run("jdbc-deadline-and-driver-disable-close-new-access", func(t *testing.T) {
		a.t = t
		id := testRequestID()
		got := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": pgID, "request_id": id, "sql": "SELECT pg_sleep(3)", "timeout_ms": 100}, 400)
		if got["status"] != "unknown" {
			t.Fatal(got)
		}
		a.call("PATCH", fmt.Sprintf("/connector-drivers/%d/status", drivers["postgresql"]), a.admin, map[string]any{"enabled": false}, 200)
		caps := a.call("GET", pgPath+"/capabilities", a.admin, nil, 200)
		if caps["sql"] != false {
			t.Fatal(caps)
		}
		a.call("PATCH", fmt.Sprintf("/connector-drivers/%d/status", drivers["postgresql"]), a.admin, map[string]any{"enabled": true}, 200)
	})
}
