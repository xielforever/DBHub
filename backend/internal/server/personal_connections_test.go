package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
)

// Exercise the complete published JDBC definition -> personal credential flow
// against two genuinely different PostgreSQL principals, including an admin who
// must not inherit either identity through DBHub's administration role.
func TestPersonalConnectionIsolation(t *testing.T) {
	a := connectorFixture(t)
	base, adminPassword, e := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if e != nil {
		t.Fatal(e)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	reader, writer := "personal_reader_"+suffix, "personal_writer_"+suffix
	readerPassword, writerPassword := "ReaderFixture9!"+suffix, "WriterFixture9!"+suffix
	for _, v := range []struct{ name, password string }{{reader, readerPassword}, {writer, writerPassword}} {
		if _, e = a.pool.Exec(a.actor(), fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", pgx.Identifier{v.name}.Sanitize(), v.password)); e != nil {
			t.Fatal(e)
		}
		name := v.name
		t.Cleanup(func() {
			_, _ = a.pool.Exec(a.actor(), "DROP OWNED BY "+pgx.Identifier{name}.Sanitize())
			_, _ = a.pool.Exec(a.actor(), "DROP ROLE "+pgx.Identifier{name}.Sanitize())
		})
		if _, e = a.pool.Exec(a.actor(), "GRANT USAGE ON SCHEMA public TO "+pgx.Identifier{v.name}.Sanitize()); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = a.pool.Exec(a.actor(), "GRANT SELECT ON public."+pgx.Identifier{a.table}.Sanitize()+" TO "+pgx.Identifier{reader}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	if _, e = a.pool.Exec(a.actor(), "GRANT SELECT,UPDATE ON public."+pgx.Identifier{a.table}.Sanitize()+" TO "+pgx.Identifier{writer}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	artifacts := a.call("GET", "/connector-drivers/available", a.admin, nil, 200)["items"].([]any)
	artifact := ""
	for _, raw := range artifacts {
		v := raw.(map[string]any)
		if v["name"] == "postgresql" {
			artifact = v["id"].(string)
		}
	}
	if artifact == "" {
		t.Fatal("PostgreSQL JDBC artifact missing")
	}
	d := a.call("POST", "/connector-drivers", a.admin, map[string]any{"artifact_id": artifact, "name": "共享 PG", "visibility": "global"}, 201)
	driverID := int64(d["id"].(float64))
	tpl := a.call("POST", "/datasource-templates", a.admin, templateBody("personal-pg", "PG 连接规则", "PostgreSQL", driverID, "postgres", "jdbc:postgresql://{host}:{port}/{database}", false), 201)
	tplID := int64(tpl["id"].(float64))
	cfg := db.ConfigOf(base)
	cfg.Type = "jdbc"
	cfg.TemplateRevisionID = &tplID
	cfg.Environment = "test"
	cfg.Parameters = map[string]string{"host": base.Host, "port": strconv.Itoa(base.Port), "database": base.Database}
	definition := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "库存数据源", "description": "每个人使用自己的数据库账号", "visibility": "global", "config": cfg}, 201)
	defID := int64(definition["id"].(float64))
	path := fmt.Sprintf("/datasource-definitions/%d/connections", defID)
	identity := func(name, password string) map[string]any {
		return map[string]any{"name": "我的库存", "username": name, "password": password, "confirmed": true}
	}
	t.Run("optional-definition-fields-normalize-before-binding", func(t *testing.T) {
		a.t = t
		source := a.call("POST", "/datasource-definitions", a.developer, map[string]any{"name": "个人默认配置", "config": db.SourceConfig{Type: "postgres", Host: base.Host, Port: base.Port, Database: base.Database, SSLMode: "disable"}}, 201)
		own := a.call("POST", fmt.Sprintf("/datasource-definitions/%d/connections", int64(source["id"].(float64))), a.developer, identity(reader, readerPassword), 201)
		if own["environment"] != "dev" || own["connection_timeout"] != float64(10) {
			t.Fatal("definition defaults drifted from the personal connection")
		}
	})
	var devID, roID, adminID int64
	t.Run("catalog-has-no-credentials-and-requires-personal-confirmation", func(t *testing.T) {
		a.t = t
		catalog := a.call("GET", "/datasource-definitions", a.developer, nil, 200)
		raw, _ := json.Marshal(catalog)
		for _, secret := range []string{adminPassword, readerPassword, writerPassword} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("catalog exposed authentication")
			}
		}
		if definition["config"].(map[string]any)["username"] != nil {
			t.Fatal("published username")
		}
		a.call("POST", path, a.developer, map[string]any{"username": reader, "confirmed": true}, 400)
		a.call("POST", path, a.developer, map[string]any{"username": reader, "password": readerPassword}, 400)
		malicious := map[string]any{}
		encoded, _ := json.Marshal(cfg)
		_ = json.Unmarshal(encoded, &malicious)
		malicious["password"] = readerPassword
		a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "bad", "visibility": "global", "config": malicious}, 400)
		a.call("POST", "/datasource-definitions", a.developer, map[string]any{"name": "bad", "visibility": "global", "config": cfg}, 403)
	})
	t.Run("actual-database-identities-and-both-permission-layers", func(t *testing.T) {
		a.t = t
		dev := a.call("POST", path, a.developer, identity(reader, readerPassword), 201)
		devID = int64(dev["id"].(float64))
		ro := a.call("POST", path, a.readonly, identity(writer, writerPassword), 201)
		roID = int64(ro["id"].(float64))
		own := a.call("POST", path, a.admin, identity(base.Username, adminPassword), 201)
		adminID = int64(own["id"].(float64))
		for _, v := range []struct {
			id          int64
			token, name string
		}{{devID, a.developer, reader}, {roID, a.readonly, writer}, {adminID, a.admin, base.Username}} {
			result := a.call("POST", "/query/execute", v.token, map[string]any{"connection_id": v.id, "sql": "SELECT current_user AS principal"}, 200)
			if result["rows"].([]any)[0].([]any)[0] != v.name {
				t.Fatal("executed under another database identity")
			}
		}
		a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": devID, "sql": "UPDATE " + a.table + " SET owner_id=9 WHERE id=1"}, 400)
		a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": roID, "sql": "UPDATE " + a.table + " SET owner_id=9 WHERE id=1"}, 400)
	})
	t.Run("personal-connection-ids-do-not-grant-access-even-to-admin", func(t *testing.T) {
		a.t = t
		for _, token := range []string{a.admin, a.readonly} {
			p := fmt.Sprintf("/connections/%d", devID)
			a.call("GET", p, token, nil, 404)
			a.call("POST", p+"/test", token, nil, 404)
			a.call("DELETE", p, token, nil, 404)
			a.call("POST", "/query/execute", token, map[string]any{"connection_id": devID, "sql": "SELECT current_user"}, 404)
			a.call("GET", fmt.Sprintf("/metadata/tables?connection_id=%d", devID), token, nil, 404)
		}
		if _, _, e = a.deps.Conns.GetSecrets(context.Background(), devID); e == nil {
			t.Fatal("missing authenticated context exposed credentials")
		}
		list := a.call("GET", "/connections", a.admin, nil, 200)["items"].([]any)
		for _, raw := range list {
			if raw.(map[string]any)["id"] == float64(devID) {
				t.Fatal("admin listed another person's connection")
			}
		}
	})
	t.Run("published-database-cannot-be-overridden-at-execution", func(t *testing.T) {
		a.t = t
		a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": devID, "database": "postgres", "sql": "SELECT current_user"}, 400)
		a.call("GET", fmt.Sprintf("/metadata/tables?connection_id=%d&database=postgres", devID), a.developer, nil, 400)
		items := a.call("GET", fmt.Sprintf("/metadata/databases?connection_id=%d", devID), a.developer, nil, 200)["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["name"] != base.Database {
			t.Fatal("public definition exposed an unconfirmed database scope")
		}
	})
	t.Run("private-drivers-and-templates-with-global-dependency-checks", func(t *testing.T) {
		a.t = t
		private := a.call("POST", "/connector-drivers", a.developer, map[string]any{"artifact_id": artifact, "name": "我的 PG"}, 201)
		privateID := int64(private["id"].(float64))
		if private["visibility"] != "private" {
			t.Fatal("driver not private by default")
		}
		for _, token := range []string{a.admin, a.readonly} {
			for _, raw := range a.call("GET", "/connector-drivers", token, nil, 200)["items"].([]any) {
				if raw.(map[string]any)["id"] == float64(privateID) {
					t.Fatal("private driver was visible")
				}
			}
			a.call("PATCH", fmt.Sprintf("/connector-drivers/%d/status", privateID), token, map[string]any{"enabled": false}, 404)
		}
		body := templateBody("my-pg", "个人 PG", "PostgreSQL", privateID, "postgres", "jdbc:postgresql://{host}:{port}/{database}", false)
		body["visibility"] = "private"
		personal := a.call("POST", "/datasource-templates", a.developer, body, 201)
		a.call("DELETE", fmt.Sprintf("/datasource-templates/%d", int64(personal["id"].(float64))), a.admin, nil, 404)
		tp := fmt.Sprintf("/datasource-templates/%d/status", int64(personal["id"].(float64)))
		a.call("PATCH", tp, a.developer, map[string]any{"enabled": false}, 200)
		found := false
		for _, raw := range a.call("GET", "/datasource-templates", a.developer, nil, 200)["items"].([]any) {
			if raw.(map[string]any)["id"] == personal["id"] {
				found = true
			}
		}
		if !found {
			t.Fatal("private owner cannot find a disabled rule to re-enable it")
		}
		a.call("PATCH", tp, a.developer, map[string]any{"enabled": true}, 200)
		body["visibility"] = "global"
		a.call("POST", "/datasource-templates", a.developer, body, 403)
		a.call("DELETE", "/users/2", a.admin, nil, 409)
		a.call("POST", "/connector-drivers", a.developer, map[string]any{"artifact_id": "uninstalled-user-jar", "name": "untrusted"}, 400)
	})
	t.Run("metadata-export-and-shared-report-use-viewers-identity", func(t *testing.T) {
		a.t = t
		a.call("POST", "/assets/sync", a.developer, map[string]any{"connection_id": devID}, 200)
		a.call("GET", fmt.Sprintf("/assets/table?connection_id=%d&database=%s&schema=public&table=%s", devID, base.Database, a.table), a.admin, nil, 404)
		export := a.call("POST", "/query/exports", a.developer, map[string]any{"connection_id": devID, "sql": "SELECT current_user", "format": "json", "reason": "verify personal identity"}, 200)
		ep := fmt.Sprintf("/query/exports/%d", int64(export["id"].(float64)))
		a.call("POST", ep+"/review", a.admin, map[string]any{"status": "approved"}, 200)
		a.call("GET", ep+"/download", a.admin, nil, 404)
		own := a.call("GET", ep+"/download", a.developer, nil, 200)
		if own["result"].(map[string]any)["rows"].([]any)[0].([]any)[0] != reader {
			t.Fatal("export used another identity")
		}
		rep := a.call("POST", "/reports", a.admin, map[string]any{"name": "共享库存定义", "connection_id": adminID, "database": base.Database, "sql": "SELECT current_user", "chart_type": "table", "visibility": "shared"}, 200)
		rp := fmt.Sprintf("/reports/%d", int64(rep["id"].(float64)))
		viewer := a.call("GET", rp, a.developer, nil, 200)
		if viewer["connection_id"] != float64(devID) {
			t.Fatal("shared report inherited the creator's connection")
		}
		result := a.call("POST", rp+"/execute", a.developer, map[string]any{"request_id": testRequestID(), "version": viewer["version"]}, 200)
		if result["rows"].([]any)[0].([]any)[0] != reader {
			t.Fatal("shared report executed with another database identity")
		}
		a.call("POST", "/shares", a.admin, map[string]any{"subject_type": "report", "subject_id": rep["id"]}, 403)
	})
	t.Run("concurrent-add-creates-one-binding", func(t *testing.T) {
		a.t = t
		extra := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "并发添加", "visibility": "global", "config": cfg}, 201)
		p := fmt.Sprintf("/datasource-definitions/%d/connections", int64(extra["id"].(float64)))
		var wg sync.WaitGroup
		var won atomic.Int32
		errs := make(chan string, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := executionRequest(a.server.Client(), a.server.URL, "POST", p, a.developer, identity(reader, readerPassword))
				if r.status == 201 {
					won.Add(1)
				} else if r.status != 409 {
					errs <- fmt.Sprint(r.status, r.message)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if won.Load() != 1 {
			t.Fatal("duplicate personal bindings", won.Load())
		}
	})
	t.Run("approved-ticket-returns-summary-without-approver-result-rows", func(t *testing.T) {
		a.t = t
		body := map[string]any{"title": "库存变更", "type": "dml", "table": a.table, "connection_id": devID, "sql": "SELECT secret FROM " + a.table, "risk": "low"}
		a.call("POST", "/tickets", a.developer, body, 400)
		body["sql"] = "UPDATE " + a.table + " SET owner_id=owner_id WHERE id=1 RETURNING secret"
		created := a.call("POST", "/tickets", a.developer, body, 200)
		path := fmt.Sprintf("/tickets/%d", int64(created["id"].(float64)))
		a.call("POST", path+"/status", a.admin, map[string]any{"status": "approved"}, 200)
		result := a.call("POST", path+"/execute", a.admin, nil, 200)["result"].(map[string]any)
		if result["rows"] != nil || result["columns"] != nil {
			t.Fatal("approver result rows were returned")
		}
		stored := a.call("GET", path, a.developer, nil, 200)["execution_result"].(map[string]any)
		if stored["rows"] != nil || stored["columns"] != nil {
			t.Fatal("approver result rows were persisted for the requester")
		}
	})
	t.Run("new-revision-never-sends-saved-password-to-new-target", func(t *testing.T) {
		a.t = t
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer listener.Close()
		var contacts atomic.Int32
		go func() {
			for {
				c, e := listener.Accept()
				if e != nil {
					return
				}
				contacts.Add(1)
				c.Close()
			}
		}()
		next := cfg
		next.Host = "127.0.0.1"
		next.Port = listener.Addr().(*net.TCPAddr).Port
		next.Parameters = map[string]string{"host": next.Host, "port": strconv.Itoa(next.Port), "database": base.Database}
		created := a.call("POST", fmt.Sprintf("/datasource-definitions/%d/revisions", defID), a.admin, map[string]any{"name": "库存新目标", "visibility": "global", "config": next}, 201)
		nextID := int64(created["id"].(float64))
		a.call("POST", fmt.Sprintf("/connections/%d/revision", devID), a.developer, map[string]any{"definition_revision_id": nextID, "confirmed": true, "username": reader}, 400)
		result := a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": devID, "sql": "SELECT current_user"}, 200)
		if result["rows"].([]any)[0].([]any)[0] != reader || contacts.Load() != 0 {
			t.Fatal("old identity was silently rebound")
		}
		a.call("PATCH", fmt.Sprintf("/datasource-definitions/%d/status", defID), a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": 3}, 200)
		a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": devID, "sql": "SELECT 1"}, 400)
		a.call("PATCH", fmt.Sprintf("/datasource-definitions/%d/status", defID), a.admin, map[string]any{"enabled": true}, 200)
	})
	t.Run("demoted-publisher-cannot-manage-global-resources", func(t *testing.T) {
		a.t = t
		ctx := context.WithValue(a.actor(), auth.ClaimsContextKey, &auth.Claims{Subject: 1, Role: "developer"})
		if e := a.deps.Conns.Catalog().SetEnabled(ctx, "driver", driverID, false); e == nil {
			t.Fatal("demoted publisher could disable a global driver")
		}
		if e := a.deps.Conns.SetDefinitionEnabled(ctx, defID, false, nil, nil); e == nil {
			t.Fatal("demoted publisher could disable a global definition")
		}
	})
	t.Run("credential-change-clears-private-metadata-and-checks", func(t *testing.T) {
		a.t = t
		a.call("POST", fmt.Sprintf("/connections/%d/test", devID), a.developer, nil, 200)
		payload := map[string]any{"name": "我的库存", "type": "jdbc", "template_revision_id": tplID, "parameters": cfg.Parameters, "username": reader, "password": readerPassword, "ssl_mode": cfg.SSLMode, "connection_timeout": cfg.ConnectionTimeout, "environment": cfg.Environment}
		a.call("PUT", fmt.Sprintf("/connections/%d", devID), a.developer, payload, 200)
		actor := context.WithValue(db.WithWorkspace(context.Background(), 1), auth.ClaimsContextKey, &auth.Claims{Subject: 2, Role: "developer"})
		snapshots, e := a.deps.Meta.ListSnapshots(actor, devID)
		if e != nil || len(snapshots) != 0 {
			t.Fatal("old metadata survived an identity change", e)
		}
		saved := a.call("GET", fmt.Sprintf("/connections/%d", devID), a.developer, nil, 200)
		if saved["last_check"] != nil {
			t.Fatal("old identity check survived")
		}
	})
}
