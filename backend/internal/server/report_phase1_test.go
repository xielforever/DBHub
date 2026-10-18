package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/user/dbhub/internal/db"
)

func TestReportPhase1(t *testing.T) {
	a := setupAPI(t)
	create := func(token, sql, visibility, key string) map[string]any {
		return a.call("POST", "/reports", token, map[string]any{"name": "库存报表", "connection_id": a.connectionFor(token), "sql": sql, "chart_type": "table", "visibility": visibility, "request_id": key}, 200)
	}
	var rep map[string]any
	var path string
	t.Run("create-retry-private-scope-and-real-readonly-execution", func(t *testing.T) {
		a.t = t
		sql := "SELECT current_setting('transaction_read_only') AS mode, SUM(owner_id)::bigint AS total FROM " + a.table
		key := testRequestID()
		rep = create(a.admin, sql, "shared", key)
		path = fmt.Sprintf("/reports/%d", int64(rep["id"].(float64)))
		again := create(a.admin, sql, "shared", key)
		if rep["id"] != again["id"] {
			t.Fatal("uncertain create duplicated the report")
		}
		a.call("POST", "/reports", a.admin, map[string]any{"name": "different", "connection_id": a.connID, "sql": sql, "request_id": key}, 409)
		for _, token := range []string{a.admin, a.developer, a.readonly} {
			response := a.call("POST", path+"/execute", token, map[string]any{"request_id": testRequestID(), "version": rep["version"]}, 200)
			if response["rows"].([]any)[0].([]any)[0] != "on" {
				t.Fatal("report did not run in a database read-only transaction")
			}
		}
		rid := testRequestID()
		body := map[string]any{"request_id": rid, "version": rep["version"]}
		a.call("POST", path+"/execute", a.admin, body, 200)
		a.call("POST", path+"/execute", a.admin, body, 409)
		a.call("POST", path+"/execute", a.admin, map[string]any{"request_id": testRequestID(), "version": rep["version"], "sql": "DELETE FROM " + a.table}, 400)
		private := create(a.developer, "SELECT 1", "private", testRequestID())
		pp := fmt.Sprintf("/reports/%d", int64(private["id"].(float64)))
		for _, other := range []string{a.admin, a.readonly} {
			a.call("GET", pp, other, nil, 404)
			a.call("POST", pp+"/execute", other, map[string]any{"request_id": testRequestID(), "version": 1}, 404)
			a.call("POST", pp+"/star", other, map[string]any{"star": true}, 404)
		}
		a.call("POST", "/reports", a.readonly, map[string]any{}, 403)
		a.call("PUT", path, a.developer, map[string]any{"version": 1, "name": "hijack"}, 403)
		a.call("DELETE", path, a.developer, nil, 404)
	})
	t.Run("write-and-query-shape-bypasses-are-rejected", func(t *testing.T) {
		a.t = t
		missingKey := executionRequest(a.server.Client(), a.server.URL, "POST", "/reports", a.admin, map[string]any{"name": "missing key", "connection_id": a.connID, "sql": "SELECT 1"})
		if missingKey.err != nil || missingKey.status != 400 {
			t.Fatal("report creation accepted a missing idempotency key", missingKey.status, missingKey.err)
		}
		for _, sql := range []string{"UPDATE " + a.table + " SET owner_id=99 WHERE id=1", "WITH d AS (DELETE FROM " + a.table + " WHERE id=1 RETURNING id) SELECT * FROM d", "SELECT * FROM " + a.table + " FOR UPDATE", "SELECT 1; SELECT 2", "SELECT * INTO should_not_exist FROM " + a.table, "EXPLAIN SELECT 1"} {
			a.call("POST", "/reports", a.admin, map[string]any{"name": "unsafe", "connection_id": a.connID, "sql": sql}, 400)
			a.call("POST", "/reports/preview", a.admin, map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": sql}, 400)
		}
		a.call("POST", "/reports/preview", a.admin, map[string]any{"request_id": testRequestID(), "connection_id": a.connectionFor(a.developer), "sql": "SELECT 1"}, 404)
		a.call("POST", "/reports", a.admin, map[string]any{"name": "another identity", "connection_id": a.connectionFor(a.developer), "sql": "SELECT 1"}, 400)
		a.call("PUT", path, a.admin, map[string]any{"version": rep["version"], "connection_id": a.connectionFor(a.developer)}, 400)
		a.call("PUT", path, a.admin, map[string]any{"version": rep["version"], "sql": "DELETE FROM " + a.table + " WHERE id=1"}, 400)
		// ASTs cannot know what a function does. The target transaction must reject
		// a hidden write even for an administrator with a database write account.
		fn := a.table + "_mutate"
		if _, err := a.pool.Exec(context.Background(), `CREATE FUNCTION public.`+fn+`() RETURNS int LANGUAGE plpgsql AS $$ BEGIN UPDATE public.`+a.table+` SET owner_id=99 WHERE id=1; RETURN 1; END $$`); err != nil {
			t.Fatal(err)
		}
		defer a.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS public.`+fn+`()`)
		a.call("POST", "/reports/preview", a.admin, map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": "SELECT public." + fn + "()"}, 400)
		var owner int
		if err := a.pool.QueryRow(context.Background(), `SELECT owner_id FROM public.`+a.table+` WHERE id=1`).Scan(&owner); err != nil || owner != 1 {
			t.Fatal("hidden function wrote through a report", owner, err)
		}
	})
	t.Run("complete-aggregation-truncation-empty-and-errors", func(t *testing.T) {
		a.t = t
		preview := func(sql string, want int) map[string]any {
			return a.call("POST", "/reports/preview", a.admin, map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "sql": sql}, want)
		}
		partial := preview("SELECT generate_series(1,1001) AS id", 200)
		if partial["truncated"] != true || len(partial["rows"].([]any)) != 1000 {
			t.Fatal("report row bound is missing")
		}
		aggregate := preview("SELECT COUNT(*)::bigint AS total FROM generate_series(1,2001)", 200)
		if aggregate["rows"].([]any)[0].([]any)[0] != float64(2001) || aggregate["truncated"] == true {
			t.Fatal("aggregate ran only on a client sample")
		}
		empty := preview("SELECT 1 AS id WHERE false", 200)
		if len(empty["rows"].([]any)) != 0 {
			t.Fatal("empty result changed")
		}
		preview("SELECT * FROM missing_report_phase1_table", 400)
		preview("SELECT ';' AS label", 200)
		precise := preview(`SELECT 9007199254740993::bigint AS big, 12345678901234567890.123456789::numeric AS amount, '{"id":9007199254740993}'::jsonb AS document`, 200)
		row := precise["rows"].([]any)[0].([]any)
		if row[0] != "9007199254740993" || row[1] != "12345678901234567890.123456789" || row[2] != `{"id": 9007199254740993}` {
			t.Fatal("report HTTP response lost numeric precision", row)
		}
	})
	t.Run("optimistic-edit-rejects-stale-writers-and-executions", func(t *testing.T) {
		a.t = t
		original := rep["version"]
		rep = a.call("PUT", path, a.admin, map[string]any{"version": original, "name": "新库存报表"}, 200)
		a.call("PUT", path, a.admin, map[string]any{"version": original, "name": "stale"}, 409)
		a.call("POST", path+"/execute", a.admin, map[string]any{"request_id": testRequestID(), "version": original}, 409)
		a.call("POST", path+"/star", a.readonly, map[string]any{"star": true}, 200)
		list := a.call("GET", "/reports?scope=all", a.readonly, nil, 200)
		found := false
		for _, raw := range list["items"].([]any) {
			item := raw.(map[string]any)
			if item["id"] == rep["id"] {
				found = true
				if item["starred"] != true {
					t.Fatal("favorite flag is wrong outside starred scope")
				}
			}
		}
		if !found {
			t.Fatal("shared report missing")
		}
		for _, q := range []string{"scope=invalid", "page=0", "page_size=101"} {
			a.call("GET", "/reports?"+q, a.admin, nil, 400)
		}
	})
	t.Run("concurrent-create-and-edit-have-one-winner", func(t *testing.T) {
		a.t = t
		key := testRequestID()
		payload := map[string]any{"name": "concurrent", "connection_id": a.connID, "sql": "SELECT 1", "request_id": key}
		var wg sync.WaitGroup
		ids := make(chan float64, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := executionRequest(a.server.Client(), a.server.URL, "POST", "/reports", a.admin, payload)
				if r.status != 200 {
					t.Errorf("create status %d", r.status)
					return
				}
				var item struct {
					ID float64 `json:"id"`
				}
				if err := json.Unmarshal(r.data, &item); err != nil {
					t.Error(err)
					return
				}
				ids <- item.ID
			}()
		}
		wg.Wait()
		close(ids)
		var id float64
		for value := range ids {
			if id != 0 && id != value {
				t.Fatal("concurrent retries duplicated a report")
			}
			id = value
		}
		var won atomic.Int32
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				r := executionRequest(a.server.Client(), a.server.URL, "PUT", fmt.Sprintf("/reports/%d", int64(id)), a.admin, map[string]any{"version": 1, "name": fmt.Sprint("editor", i)})
				if r.status == 200 {
					won.Add(1)
				} else if r.status != 409 {
					t.Errorf("update status %d", r.status)
				}
			}(i)
		}
		wg.Wait()
		if won.Load() != 1 {
			t.Fatal("optimistic edit winners", won.Load())
		}
	})
	t.Run("execution-is-durably-audited-with-report-and-personal-identity", func(t *testing.T) {
		a.t = t
		logs := a.call("GET", "/audit-logs?resource_type=REPORT&action=REPORT_EXECUTE", a.admin, nil, 200)
		found := false
		for _, raw := range logs["items"].([]any) {
			item := raw.(map[string]any)
			ctx := item["context"].(map[string]any)
			if ctx["report_id"] == rep["id"] && ctx["outcome"] == "succeeded" {
				found = true
				if ctx["execution_request_id"] == nil || ctx["database_account"] == nil || ctx["definition_revision_id"] != rep["definition_revision_id"] {
					t.Fatal("report audit lost execution identity")
				}
				if ctx["sql"] != nil {
					t.Fatal("report SQL leaked into operation audit")
				}
			}
		}
		if !found {
			t.Fatal("report execution audit missing")
		}
	})
	t.Run("personal-connection-delete-retains-shared-definition", func(t *testing.T) {
		a.t = t
		before := a.call("GET", path, a.readonly, nil, 200)
		a.call("DELETE", fmt.Sprintf("/connections/%d", a.connID), a.admin, nil, 200)
		owned := a.call("GET", path, a.admin, nil, 200)
		if owned["connection_id"] != float64(0) || owned["definition_revision_id"] != rep["definition_revision_id"] {
			t.Fatal("report lost its immutable target")
		}
		a.call("POST", path+"/execute", a.admin, map[string]any{"request_id": testRequestID(), "version": owned["version"]}, 409)
		viewer := a.call("GET", path, a.readonly, nil, 200)
		if viewer["connection_id"] != before["connection_id"] {
			t.Fatal("creator deletion changed viewer identity")
		}
		a.call("POST", path+"/execute", a.readonly, map[string]any{"request_id": testRequestID(), "version": viewer["version"]}, 200)
		a.call("DELETE", path, a.admin, nil, 200)
		a.call("GET", path, a.readonly, nil, 404)
	})
}

func TestMySQLReportReadOnly(t *testing.T) {
	if os.Getenv("TEST_MYSQL_DSN") == "" {
		t.Skip("real MySQL not configured")
	}
	a := setupAPI(t)
	cfg, err := mysql.ParseDSN(os.Getenv("TEST_MYSQL_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	source := &db.Connection{UserID: 1, Name: "mysql-report", Type: "mysql", Host: host, Port: port, Database: cfg.DBName, Username: cfg.User, Password: cfg.Passwd, SSLMode: "disable", Environment: "test", ConnectionTimeout: 3}
	if err = a.deps.Conns.Create(a.actor(), source); err != nil {
		t.Fatal(err)
	}
	preview := func(sql string, want int) map[string]any {
		return a.call("POST", "/reports/preview", a.admin, map[string]any{"request_id": testRequestID(), "connection_id": source.ID, "sql": sql}, want)
	}
	result := preview("SELECT 12.34 AS amount", 200)
	raw, _ := json.Marshal(result["rows"])
	if string(raw) != `[["12.34"]]` {
		t.Fatal("MySQL report decimal changed", string(raw))
	}
	// MySQL's @@transaction_read_only is the session default, not the active
	// START TRANSACTION READ ONLY mode. Verify actual refusal of a function's
	// hidden DML instead of treating that variable as proof.
	rootConfig := *cfg
	rootConfig.User, rootConfig.Passwd = "root", os.Getenv("MYSQL_ROOT_PASSWORD")
	if rootConfig.Passwd == "" {
		t.Fatal("MySQL root fixture binding is missing")
	}
	root, err := sql.Open("mysql", rootConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	table, fn := a.table+"_report", a.table+"_fn"
	if _, err = root.Exec("CREATE TABLE " + table + " (id INT PRIMARY KEY, amount INT)"); err != nil {
		t.Fatal(err)
	}
	defer root.Exec("DROP TABLE IF EXISTS " + table)
	if _, err = root.Exec("INSERT INTO " + table + " VALUES(1,1)"); err != nil {
		t.Fatal(err)
	}
	if _, err = root.Exec("CREATE FUNCTION " + fn + "() RETURNS INT DETERMINISTIC MODIFIES SQL DATA BEGIN UPDATE " + table + " SET amount=99 WHERE id=1; RETURN 1; END"); err != nil {
		t.Fatal(err)
	}
	defer root.Exec("DROP FUNCTION IF EXISTS " + fn)
	preview("SELECT "+fn+"() AS blocked", 400)
	var amount int
	if err = root.QueryRow("SELECT amount FROM " + table + " WHERE id=1").Scan(&amount); err != nil || amount != 1 {
		t.Fatal("MySQL report function wrote despite read-only transaction", amount, err)
	}
	for _, sql := range []string{"SHOW TABLES", "SELECT 1 FOR UPDATE", "UPDATE anything SET id=2 WHERE id=1"} {
		preview(sql, 400)
	}
}
