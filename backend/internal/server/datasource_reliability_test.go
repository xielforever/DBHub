package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func connectionPayload(c *db.Connection, password string) map[string]any {
	return map[string]any{"name": c.Name, "type": c.Type, "host": c.Host, "port": c.Port,
		"database": c.Database, "username": c.Username, "password": password,
		"ssl_mode": c.SSLMode, "connection_timeout": c.ConnectionTimeout,
		"environment": c.Environment, "proxy_id": c.ProxyID, "ssh_tunnel_id": c.SSHTunnelID}
}

func TestDatasourceReliability(t *testing.T) {
	a := setupAPI(t)
	ctx := a.actor()
	original, password, err := a.deps.Conns.GetSecrets(ctx, a.connID)
	if err != nil {
		t.Fatal(err)
	}
	payload := connectionPayload(original, password)
	payload["name"] = "connection-reliability"
	created := a.call("POST", "/connections", a.admin, payload, 201)
	id := int64(created["id"].(float64))
	path := fmt.Sprintf("/connections/%d", id)
	defer a.deps.Conns.Delete(ctx, id)

	t.Run("saved-check-survives-reload-and-draft-uses-saved-password", func(t *testing.T) {
		a.t = t
		tested := a.call("POST", path+"/test", a.admin, nil, 200)
		if tested["recorded"] != true {
			t.Fatal("saved check was not recorded")
		}
		detail := a.call("GET", path, a.admin, nil, 200)
		check := detail["last_check"].(map[string]any)
		if check["ok"] != true || check["checked_at"] == "" || check["version"] == "" {
			t.Fatal("persisted check missing")
		}
		payload["password"] = ""
		draft := a.call("POST", path+"/test", a.admin, payload, 200)
		if draft["ok"] != true || draft["recorded"] != false {
			t.Fatal("draft must use saved secret without publishing a check")
		}
		a.call("POST", path+"/test", a.developer, payload, 404)
		a.call("POST", path+"/test", a.readonly, payload, 404)
		after := a.call("GET", path, a.admin, nil, 200)["last_check"].(map[string]any)
		if after["checked_at"] != check["checked_at"] {
			t.Fatal("draft changed saved check")
		}
	})
	t.Run("saved-password-is-not-sent-to-edited-destination", func(t *testing.T) {
		a.t = t
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		var contacts atomic.Int32
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				contacts.Add(1)
				conn.Close()
			}
		}()
		port := listener.Addr().(*net.TCPAddr).Port
		payload["host"], payload["port"] = "127.0.0.1", port
		res := executionRequest(a.server.Client(), a.server.URL, "POST", path+"/test", a.admin, payload)
		requireExecutionStatus(t, res, 400)
		if !strings.Contains(res.message, "重新输入口令") || contacts.Load() != 0 {
			t.Fatal("edited target contacted with implicit stored credentials")
		}
		payload["host"], payload["port"] = original.Host, original.Port
	})
	t.Run("older-check-cannot-overwrite-newer-check", func(t *testing.T) {
		conn, err := a.deps.Conns.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		older := &db.ConnectionCheck{OK: false, Type: "postgres", Error: "old result", StartedAt: time.Now().Add(-time.Hour), CheckedAt: time.Now()}
		recorded, err := a.deps.Conns.SaveCheck(ctx, id, conn.UpdatedAt, older)
		if err != nil || recorded {
			t.Fatal("older check overwrote newer check")
		}
		loaded, err := a.deps.Conns.Get(ctx, id)
		if err != nil || loaded.LastCheck == nil || !loaded.LastCheck.OK {
			t.Fatal("newer success was lost")
		}
	})
	t.Run("concurrent-edit-loads-credentials-from-one-revision", func(t *testing.T) {
		probe := *original
		probe.DefinitionRevisionID = nil
		probe.DefinitionFamilyID = ""
		probe.ID, probe.Name, probe.Host, probe.Password = 0, "revision-probe", "a.invalid", "password-a"
		if err := a.deps.Conns.Create(ctx, &probe); err != nil {
			t.Fatal(err)
		}
		defer a.deps.Conns.Delete(ctx, probe.ID)
		done := make(chan error, 1)
		go func() {
			for i := 0; i < 80; i++ {
				next := probe
				if i%2 == 0 {
					next.Host, next.Password = "b.invalid", "password-b"
				}
				if err := a.deps.Conns.Update(ctx, &next); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
		mismatched := false
		var readErr error
		for i := 0; i < 120; i++ {
			loaded, secret, err := a.deps.Conns.GetSecrets(ctx, probe.ID)
			if err != nil {
				readErr = err
				break
			}
			if loaded.Host == "a.invalid" && secret != "password-a" || loaded.Host == "b.invalid" && secret != "password-b" {
				mismatched = true
			}
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if mismatched {
			t.Fatal("mixed destination and password from concurrent configuration versions")
		}
	})
	t.Run("configuration-change-invalidates-check-and-failure-is-persisted", func(t *testing.T) {
		a.t = t
		before, err := a.deps.Conns.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		payload["password"] = password
		payload["host"], payload["port"] = "127.0.0.1", 1
		updated := a.call("PUT", path, a.admin, payload, 200)
		if updated["last_check"] != nil {
			t.Fatal("old configuration is still shown as checked")
		}
		stale := &db.ConnectionCheck{OK: true, Type: "postgres", StartedAt: time.Now(), CheckedAt: time.Now()}
		if saved, err := a.deps.Conns.SaveCheck(ctx, id, before.UpdatedAt, stale); err != nil || saved {
			t.Fatal("old configuration check accepted")
		}
		a.call("POST", path+"/test", a.admin, nil, 400)
		failed := a.call("GET", path, a.admin, nil, 200)["last_check"].(map[string]any)
		if failed["ok"] != false || failed["error"] == "" || failed["checked_at"] == "" {
			t.Fatal("failed check was lost on reload")
		}
		raw, _ := json.Marshal(failed)
		if strings.Contains(string(raw), password) {
			t.Fatal("credential leaked into check record")
		}
		payload["host"], payload["port"] = original.Host, original.Port
		a.call("PUT", path, a.admin, payload, 200)
		a.call("POST", path+"/test", a.admin, nil, 200)
		a.call("DELETE", path, a.admin, nil, 200)
		a.call("GET", path, a.admin, nil, 404)
		var remaining int
		if err := a.pool.QueryRow(ctx, "SELECT count(*) FROM sys_connection_checks WHERE connection_id=$1", id).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatal("deleted connection retained check")
		}
	})
}

func TestMetadataSyncReliability(t *testing.T) {
	a := setupAPI(t)
	ctx := a.actor()
	conn, _, err := a.deps.Conns.GetSecrets(ctx, a.connID)
	if err != nil {
		t.Fatal(err)
	}
	name := "sync_" + strings.ReplaceAll(testRequestID(), "-", "")
	schemas := []string{name + "_a", name + "_b"}
	for _, schema := range schemas {
		if _, err := a.pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()+"; CREATE TABLE "+pgx.Identifier{schema, "same"}.Sanitize()+"(id int)"); err != nil {
			t.Fatal(err)
		}
		defer a.pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	}
	snapshot := func(schema, comment string) *db.MetaSnapshot {
		return &db.MetaSnapshot{ConnectionID: a.connID, DatabaseName: conn.Database, SchemaName: schema, TableName: "same", TableType: "table", TableComment: comment}
	}
	publish := func(items []*db.MetaSnapshot) error {
		current, err := a.deps.Conns.Get(ctx, a.connID)
		if err != nil {
			return err
		}
		batch, err := a.deps.Meta.BeginSync(ctx, a.connID, current.UpdatedAt)
		if err != nil {
			return err
		}
		defer batch.Close()
		return batch.Publish(ctx, items)
	}
	digest := func(id int64) string {
		t.Helper()
		var result string
		if err := a.pool.QueryRow(ctx, `SELECT md5(COALESCE(string_agg(to_jsonb(s)::text,'|' ORDER BY database_name,schema_name,table_name),'')) FROM sys_meta_snapshots s WHERE connection_id=$1`, id).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	t.Run("publication-is-atomic-and-identifies-schema", func(t *testing.T) {
		if err := publish([]*db.MetaSnapshot{snapshot(schemas[0], "old"), snapshot(schemas[1], "old")}); err != nil {
			t.Fatal(err)
		}
		before := digest(a.connID)
		bad := snapshot(schemas[1], "bad")
		bad.RawColumns = json.RawMessage(`invalid-json`)
		if err := publish([]*db.MetaSnapshot{snapshot(schemas[0], "new"), bad}); err == nil {
			t.Fatal("invalid snapshot was published")
		}
		if digest(a.connID) != before {
			t.Fatal("failed publication changed old snapshots")
		}
		if err := publish([]*db.MetaSnapshot{snapshot(schemas[0], "kept")}); err != nil {
			t.Fatal(err)
		}
		kept, err := a.deps.Meta.GetSnapshot(ctx, a.connID, conn.Database, schemas[0], "same")
		if err != nil || kept.TableComment != "kept" {
			t.Fatal("successful publication lost a retained object")
		}
		var count int
		if err := a.pool.QueryRow(ctx, "SELECT count(*) FROM sys_meta_snapshots WHERE connection_id=$1 AND schema_name=$2", a.connID, schemas[1]).Scan(&count); err != nil || count != 0 {
			t.Fatal("same table in another schema was incorrectly retained")
		}
		if err := publish(nil); err != nil {
			t.Fatal(err)
		}
		if err := a.pool.QueryRow(ctx, "SELECT count(*) FROM sys_meta_snapshots WHERE connection_id=$1", a.connID).Scan(&count); err != nil || count != 0 {
			t.Fatal("complete empty inventory did not remove old snapshots")
		}
	})
	t.Run("independent-handlers-share-lock-and-config-change-aborts", func(t *testing.T) {
		a.t = t
		current, err := a.deps.Conns.Get(ctx, a.connID)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := a.deps.Meta.BeginSync(ctx, a.connID, current.UpdatedAt)
		if err != nil {
			t.Fatal(err)
		}
		defer batch.Close()
		if other, err := db.NewMetadataRepository(a.pool).BeginSync(ctx, a.connID, current.UpdatedAt); !errors.Is(err, db.ErrSyncBusy) {
			if other != nil {
				other.Close()
			}
			t.Fatal("independent repository bypassed synchronization lock")
		}
		a.call("POST", "/assets/sync", a.admin, map[string]any{"connection_id": a.connID}, 409)
		current.Name += "-edited"
		if err := a.deps.Conns.Update(ctx, current); err != nil {
			t.Fatal(err)
		}
		if err := batch.Publish(ctx, []*db.MetaSnapshot{snapshot(schemas[0], "stale")}); !errors.Is(err, db.ErrConnectionChanged) {
			t.Fatal("outdated source configuration was published")
		}
		batch.Close()
		current, err = a.deps.Conns.Get(ctx, a.connID)
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := a.deps.Meta.BeginSync(ctx, a.connID, current.UpdatedAt)
		if err != nil {
			t.Fatal("failed batch retained lock")
		}
		fresh.Close()
	})
	t.Run("sync-input-cannot-accidentally-select-all-sources", func(t *testing.T) {
		a.t = t
		for _, body := range []any{map[string]any{"connection_id": -1}, map[string]any{"connection_id": 0}, map[string]any{"connection_id": nil}, map[string]any{"unexpected": 1}, (*int)(nil)} {
			a.call("POST", "/assets/sync", a.admin, body, 400)
		}
		a.call("POST", "/assets/sync", a.readonly, map[string]any{"connection_id": a.connID}, 403)
	})
	t.Run("partial-schema-failure-keeps-complete-previous-inventory", func(t *testing.T) {
		a.t = t
		role := name + "_role"
		roleSQL := pgx.Identifier{role}.Sanitize()
		if _, err := a.pool.Exec(ctx, "CREATE ROLE "+roleSQL+" LOGIN PASSWORD 'SyncProbePassword!9'; ALTER ROLE "+roleSQL+" SET statement_timeout='200ms'"); err != nil {
			t.Fatal(err)
		}
		defer a.pool.Exec(ctx, "DROP ROLE "+roleSQL)
		limited := *conn
		limited.DefinitionRevisionID = nil
		limited.DefinitionFamilyID = ""
		limited.ID = 0
		limited.DefinitionRevisionID = nil
		limited.DefinitionFamilyID = ""
		limited.Name = "limited-sync"
		limited.Username = role
		limited.Password = "SyncProbePassword!9"
		if err := a.deps.Conns.Create(ctx, &limited); err != nil {
			t.Fatal(err)
		}
		defer a.deps.Conns.Delete(ctx, limited.ID)
		body := map[string]any{"connection_id": limited.ID}
		first := a.call("POST", "/assets/sync", a.admin, body, 200)
		status := first["connections"].([]any)[0].(map[string]any)
		if status["status"] != "succeeded" {
			t.Fatalf("initial real collection failed: %v", status["errors"])
		}
		before := digest(limited.ID)
		lock, err := a.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Rollback(ctx)
		if _, err := lock.Exec(ctx, "LOCK TABLE "+pgx.Identifier{schemas[0], "same"}.Sanitize()+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		failed := a.call("POST", "/assets/sync", a.admin, body, 200)["connections"].([]any)[0].(map[string]any)
		if failed["status"] != "failed" || failed["tables"] != float64(0) || len(failed["errors"].([]any)) == 0 {
			t.Fatal("partial failure was presented as success")
		}
		if digest(limited.ID) != before {
			t.Fatal("partial collection deleted or modified old inventory")
		}
		lock.Rollback(ctx)
		retried := a.call("POST", "/assets/sync", a.admin, body, 200)["connections"].([]any)[0].(map[string]any)
		if retried["status"] != "succeeded" {
			t.Fatal("failed collection did not release lock")
		}
	})
	t.Run("concurrent-http-sync-and-cancellation-release-lock", func(t *testing.T) {
		a.t = t
		otherDeps := *a.deps
		cfg := &config.Config{Env: "development", SecretKey: []byte("test-main-key-32-bytes-long-123456789"), AccessTTL: time.Hour, RefreshTTL: time.Hour}
		other := httptest.NewServer(NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps.UserRepo, &otherDeps))
		defer other.Close()
		defer otherDeps.Queries.Close()
		client := &http.Client{Timeout: 15 * time.Second}
		body := map[string]any{"connection_id": a.connID}
		for _, cancelled := range []bool{false, true} {
			lock, err := a.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := lock.Exec(ctx, "LOCK TABLE "+pgx.Identifier{schemas[0], "same"}.Sanitize()+" IN ACCESS EXCLUSIVE MODE"); err != nil {
				lock.Rollback(ctx)
				t.Fatal(err)
			}
			before := digest(a.connID)
			requestCtx, cancel := context.WithCancel(ctx)
			done := make(chan executionResponse, 1)
			go func() {
				done <- executionRequestContext(requestCtx, client, a.server.URL, "POST", "/assets/sync", a.admin, body)
			}()
			current, err := a.deps.Conns.Get(ctx, a.connID)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				var held bool
				err = a.pool.QueryRow(ctx, `WITH expected AS (SELECT hashtextextended(current_schema()||':dbhub:metadata-sync:'||$1::bigint::text,0) AS k)
                SELECT EXISTS(SELECT 1 FROM pg_locks l,expected e WHERE l.locktype='advisory' AND l.granted AND l.objsubid=1
                AND l.classid=((e.k>>32)&4294967295)::oid AND l.objid=(e.k&4294967295)::oid)`, a.connID).Scan(&held)
				if held {
					break
				}
				if err != nil || time.Now().After(deadline) {
					cancel()
					lock.Rollback(ctx)
					t.Fatal("HTTP collection did not acquire lock", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			duplicate := executionRequest(client, other.URL, "POST", "/assets/sync", a.admin, body)
			requireExecutionStatus(t, duplicate, 409)
			if cancelled {
				cancel()
			} else {
				lock.Rollback(ctx)
			}
			select {
			case result := <-done:
				if !cancelled {
					requireExecutionStatus(t, result, 200)
				}
			case <-time.After(8 * time.Second):
				cancel()
				lock.Rollback(ctx)
				t.Fatal("collection did not finish")
			}
			cancel()
			deadline = time.Now().Add(5 * time.Second)
			for {
				probe, err := a.deps.Meta.BeginSync(ctx, a.connID, current.UpdatedAt)
				if err == nil {
					probe.Close()
					break
				}
				if !errors.Is(err, db.ErrSyncBusy) || time.Now().After(deadline) {
					lock.Rollback(ctx)
					t.Fatal("HTTP completion retained lock")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if cancelled && digest(a.connID) != before {
				lock.Rollback(ctx)
				t.Fatal("cancelled collection changed inventory")
			}
			lock.Rollback(ctx)
		}
	})
}
