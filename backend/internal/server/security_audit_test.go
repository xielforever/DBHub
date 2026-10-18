package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/queryapi"
)

func securityReviewer(a *apiTest) string {
	a.t.Helper()
	a.call("POST", "/users", a.admin, map[string]any{"username": "security_reviewer", "password": "TestPassword123!", "role": "admin"}, 201)
	return a.call("POST", "/auth/login", "", map[string]any{"username": "security_reviewer", "password": "TestPassword123!"}, 200)["access_token"].(string)
}

func TestSecurityAuditIndependentApprovals(t *testing.T) {
	a := setupAPI(t)
	reviewer := securityReviewer(a)
	t.Run("ticket", func(t *testing.T) {
		a.t = t
		created := a.call("POST", "/tickets", a.admin, map[string]any{"title": "independent review", "table": a.table, "connection_id": a.connID, "sql": "UPDATE " + a.table + " SET secret='approved' WHERE id=1"}, 200)
		path := fmt.Sprintf("/tickets/%.0f", created["id"])
		a.call("POST", path+"/status", a.admin, map[string]any{"status": "approved"}, 409)
		a.call("POST", path+"/execute", a.admin, nil, 409)
		a.call("POST", path+"/status", reviewer, map[string]any{"status": "approved"}, 200)
		a.call("POST", path+"/status", reviewer, map[string]any{"status": "rejected"}, 409)
		a.call("POST", path+"/execute", a.admin, nil, 200)
		a.call("POST", path+"/execute", a.admin, nil, 409)
	})
	t.Run("export", func(t *testing.T) {
		a.t = t
		created := a.call("POST", "/query/exports", a.admin, map[string]any{"connection_id": a.connID, "sql": "SELECT id FROM " + a.table, "format": "json", "reason": "security regression"}, 200)
		path := fmt.Sprintf("/query/exports/%.0f", created["id"])
		a.call("POST", path+"/review", a.admin, map[string]any{"status": "approved"}, 409)
		a.call("GET", path+"/download", a.admin, nil, 404)
		a.call("POST", path+"/review", reviewer, map[string]any{"status": "approved"}, 200)
		a.call("POST", path+"/review", reviewer, map[string]any{"status": "rejected"}, 409)
		a.call("GET", path+"/download", reviewer, nil, 404)
		a.call("GET", path+"/download", a.admin, nil, 200)
	})
	t.Run("unreleased-report-needs-current-reviewer-role", func(t *testing.T) {
		f := setupManaged(t)
		b := f.a
		reviewer := securityReviewer(b)
		id, _ := f.draft("SELECT 1 AS total")
		review := f.submit(id, 1)
		validation := testRequestID()
		b.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), reviewer, map[string]any{"request_id": validation, "account_id": f.account, "user_ids": []int64{3}}, 200)
		b.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), reviewer, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation}, 200)
		b.call("PATCH", "/workspaces/1/members/4", b.admin, map[string]any{"expected_version": 1, "role": "developer"}, 200)
		b.call("POST", fmt.Sprintf("/managed-reports/%d/releases", id), b.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 0, "review_id": review, "kind": "publish"}, 409)
		b.call("PATCH", "/workspaces/1/members/4", b.admin, map[string]any{"expected_version": 2, "role": "admin"}, 200)
		f.release(id, 0, review, "publish")
		// A historical restore retains its approved history; it need not
		// acquire a new signature from a reviewer who has since left.
		b.call("PATCH", "/workspaces/1/members/4", b.admin, map[string]any{"expected_version": 3, "active": false}, 200)
		f.release(id, 1, 0, "unpublish")
		f.release(id, 2, review, "publish")
		f.run(id, 3, 200)
	})
}

func TestSecurityAuditTicketTargetCannotDrift(t *testing.T) {
	a := setupAPI(t)
	reviewer := securityReviewer(a)
	c, password, err := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if err != nil {
		t.Fatal(err)
	}
	c.ID, c.DefinitionRevisionID, c.DefinitionFamilyID = 0, nil, ""
	c.Password = password
	if err = a.deps.Conns.Create(a.actor(), c); err != nil {
		t.Fatal(err)
	}
	created := a.call("POST", "/tickets", a.admin, map[string]any{"title": "pinned target", "table": a.table, "connection_id": c.ID, "sql": "UPDATE " + a.table + " SET secret='unreviewed-target' WHERE id=1"}, 200)
	path := fmt.Sprintf("/tickets/%.0f", created["id"])
	a.call("POST", path+"/status", reviewer, map[string]any{"status": "approved"}, 200)
	// The fixture hostname and its IP reach the same real database. Swapping
	// the hostname for the IP changes the approved target revision while
	// keeping execution observable without contacting any other system.
	addresses, err := net.LookupHost(c.Host)
	if err != nil || len(addresses) == 0 || addresses[0] == c.Host {
		t.Fatal("fixture needs a resolvable hostname", err)
	}
	c.Host = addresses[0]
	if err = a.deps.Conns.Update(a.actor(), c); err != nil {
		t.Fatal(err)
	}
	a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": c.ID, "sql": "SELECT 1"}, 200)
	a.call("POST", path+"/execute", a.admin, nil, 409)
	var value string
	if err = a.pool.QueryRow(context.Background(), "SELECT secret FROM public."+a.table+" WHERE id=1").Scan(&value); err != nil || value != "one" {
		t.Fatal("an unreviewed target received the approved SQL", value, err)
	}
}

func TestSecurityAuditLegacyShareOwnership(t *testing.T) {
	a := setupAPI(t)
	share := &db.ShareToken{SubjectType: "report", SubjectID: 1234, CreatedBy: 1}
	if err := a.deps.Shares.Create(a.actor(), share, "test-legacy-disabled-share"); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/shares/%d", share.ID)
	list := "/shares?subject_type=report&subject_id=1234"
	if items := a.call("GET", list, a.developer, nil, 200)["items"].([]any); len(items) != 0 {
		t.Error("another user's share metadata was disclosed")
	}
	a.call("POST", path+"/revoke", a.developer, nil, 404)
	a.call("DELETE", path, a.developer, nil, 404)
	a.call("POST", path+"/revoke", a.admin, nil, 200)
	a.call("DELETE", path, a.admin, nil, 200)
}

func TestSecurityAuditLegacyDashboardOwnership(t *testing.T) {
	a := setupAPI(t)
	dashboard := a.call("POST", "/dashboards", a.developer, map[string]any{"name": "private legacy dashboard", "visibility": "private"}, 200)
	path := fmt.Sprintf("/dashboards/%.0f", dashboard["id"])
	for _, token := range []string{a.admin, a.readonly} {
		a.call("GET", path, token, nil, 404)
		a.call("POST", path+"/star", token, map[string]any{"star": true}, 404)
	}
	id := int64(dashboard["id"].(float64))
	if err := a.deps.Dashboards.Delete(a.actor(), id); err == nil {
		t.Fatal("repository deleted another user's private dashboard")
	}
	a.call("POST", path+"/star", a.developer, map[string]any{"star": true}, 200)
	a.call("DELETE", path, a.developer, nil, 200)
}

func TestSecurityAuditHistoryDeletionIsPersonal(t *testing.T) {
	a := setupAPI(t)
	var id int64
	if err := a.pool.QueryRow(context.Background(), `SELECT max(id) FROM sys_query_history WHERE user_id=1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	a.call("DELETE", fmt.Sprintf("/query/history/%d", id), a.developer, nil, 404)
	a.exec(a.developer, "SELECT id FROM "+a.table, 200)
	var developerHistory int64
	if err := a.pool.QueryRow(context.Background(), `SELECT max(id) FROM sys_query_history WHERE user_id=2`).Scan(&developerHistory); err != nil {
		t.Fatal(err)
	}
	a.call("DELETE", fmt.Sprintf("/query/history/%d", developerHistory), a.admin, nil, 404)
	a.call("DELETE", "/query/history", a.admin, nil, 200)
	var own, other int
	if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE user_id=1),count(*) FILTER(WHERE user_id=2) FROM sys_query_history`).Scan(&own, &other); err != nil || own != 0 || other != 1 {
		t.Fatal("clearing personal history changed another user's records", own, other, err)
	}
}

func TestSecurityAuditNetworkDependencyDeletion(t *testing.T) {
	a := setupAPI(t)
	for _, kind := range []string{"ssh-tunnel", "proxy"} {
		t.Run(kind, func(t *testing.T) {
			a.t = t
			var id int64
			var column, path string
			if kind == "ssh-tunnel" {
				item := &db.SSHTunnel{UserID: 1, Name: "retained tunnel", Host: "localhost", Port: 22, Username: "test", AuthType: "password", Password: "fixture-only", HostKey: "SHA256:test"}
				if err := a.deps.Tunnels.Create(a.actor(), item); err != nil {
					t.Fatal(err)
				}
				id, column, path = item.ID, "ssh_tunnel_id", "/ssh-tunnels/"
			} else {
				item := &db.Proxy{UserID: 1, Name: "retained proxy", Type: "socks5", Host: "localhost", Port: 1080}
				if err := a.deps.Proxies.Create(a.actor(), item); err != nil {
					t.Fatal(err)
				}
				id, column, path = item.ID, "proxy_id", "/proxies/"
			}
			if _, err := a.pool.Exec(context.Background(), "UPDATE sys_connections SET "+column+"=$1 WHERE id=$2", id, a.connID); err != nil {
				t.Fatal(err)
			}
			a.call("DELETE", path+fmt.Sprint(id), a.admin, nil, 409)
			var retained int64
			if err := a.pool.QueryRow(context.Background(), "SELECT "+column+" FROM sys_connections WHERE id=$1", a.connID).Scan(&retained); err != nil || retained != id {
				t.Fatal("delete silently changed a connection's route", retained, err)
			}
			if _, err := a.pool.Exec(context.Background(), "UPDATE sys_connections SET "+column+"=NULL WHERE id=$1", a.connID); err != nil {
				t.Fatal(err)
			}
			a.call("DELETE", path+fmt.Sprint(id), a.admin, nil, 200)
		})
	}
	t.Run("connection-retains-query-policies", func(t *testing.T) {
		a.t = t
		conn, password, err := a.deps.Conns.GetSecrets(a.actor(), a.connID)
		if err != nil {
			t.Fatal(err)
		}
		policy := a.call("PUT", "/query/policies", a.admin, map[string]any{"connection_id": a.connID, "database_name": conn.Database, "schema_name": "public", "table_name": a.table, "role_code": "readonly", "column_permissions": map[string]any{}, "row_conditions": []map[string]any{{"column": "owner_id", "operator": "=", "value": 1}}}, 200)
		if len(a.exec(a.readonly, "SELECT id FROM "+a.table, 200)["rows"].([]any)) != 1 {
			t.Fatal("fixture policy did not apply")
		}
		config := db.ConfigOf(conn)
		config.ConnectionTimeout++
		next := a.call("POST", fmt.Sprintf("/datasource-definitions/%d/revisions", *conn.DefinitionRevisionID), a.admin, map[string]any{"name": "next guarded revision", "visibility": "global", "config": config}, 201)
		a.call("POST", fmt.Sprintf("/connections/%d/revision", a.connID), a.admin, map[string]any{"definition_revision_id": next["id"], "username": conn.Username, "password": password, "confirmed": true}, 409)
		response := executionRequest(a.server.Client(), a.server.URL, "DELETE", fmt.Sprintf("/connections/%d", a.connID), a.admin, nil)
		if response.status != 409 {
			rows := a.exec(a.readonly, "SELECT id FROM "+a.table, 200)["rows"].([]any)
			t.Fatalf("connection delete returned %d, want 409; readonly result grew to %d rows", response.status, len(rows))
		}
		if len(a.exec(a.readonly, "SELECT id FROM "+a.table, 200)["rows"].([]any)) != 1 {
			t.Fatal("connection delete relaxed a published source policy")
		}
		a.call("DELETE", fmt.Sprintf("/query/policies/%.0f", policy["id"]), a.admin, nil, 200)
		pending, err := a.pool.Begin(a.actor())
		if err != nil {
			t.Fatal(err)
		}
		defer pending.Rollback(context.Background())
		var concurrentPolicy int64
		if err = pending.QueryRow(a.actor(), `INSERT INTO sys_query_policies(connection_id,database_name,schema_name,table_name,role_code,column_permissions,row_conditions,updated_by) VALUES($1,$2,'public',$3,'developer','{}','[]',1) RETURNING id`, a.connID, conn.Database, a.table).Scan(&concurrentPolicy); err != nil {
			t.Fatal(err)
		}
		result := make(chan executionResponse, 1)
		go func() {
			result <- executionRequest(a.server.Client(), a.server.URL, "DELETE", fmt.Sprintf("/connections/%d", a.connID), a.admin, nil)
		}()
		select {
		case r := <-result:
			t.Fatalf("delete raced past an uncommitted policy: %d", r.status)
		case <-time.After(100 * time.Millisecond):
		}
		if err = pending.Commit(a.actor()); err != nil {
			t.Fatal(err)
		}
		select {
		case r := <-result:
			requireExecutionStatus(t, r, 409)
		case <-time.After(5 * time.Second):
			t.Fatal("delete did not release its dependency lock")
		}
		a.call("DELETE", fmt.Sprintf("/query/policies/%d", concurrentPolicy), a.admin, nil, 200)
		a.call("POST", fmt.Sprintf("/connections/%d/revision", a.connID), a.admin, map[string]any{"definition_revision_id": next["id"], "username": conn.Username, "password": password, "confirmed": true}, 200)
		a.call("DELETE", fmt.Sprintf("/connections/%d", a.connID), a.admin, nil, 200)
	})
}

func TestSecurityAuditSSHTestHonorsAllowlist(t *testing.T) {
	a := setupAPI(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DBHUB_TARGET_ALLOWLIST", "allowed.invalid:22")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	contacted := make(chan bool, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			contacted <- true
			conn.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	a.call("POST", "/ssh-tunnels/test", a.developer, map[string]any{"name": "denied destination", "host": "127.0.0.1", "port": n, "username": "test", "auth_type": "password", "password": "fixture-only", "host_key": "SHA256:test"}, 400)
	select {
	case <-contacted:
		t.Fatal("SSH connection test contacted a destination outside the operator allowlist")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSecurityAuditRedisRevocationBeforeDispatch(t *testing.T) {
	a := setupAPI(t)
	key := "security-audit:" + testRequestID()
	if err := a.deps.Redis.Set(context.Background(), key, "must-survive", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	defer a.deps.Redis.Del(context.Background(), key)
	c, err := a.deps.Conns.Get(a.actor(), a.redisID)
	if err != nil {
		t.Fatal(err)
	}
	drivers := connectorruntime.New()
	// Opening a real Redis session may take time. Revoke access immediately
	// after it opens, before the handler dispatches its destructive command.
	drivers.RecordUsage = func(ctx context.Context, c *db.Connection) {
		bindings := int64(3)
		if err := a.deps.Conns.SetDefinitionEnabled(a.actor(), *c.DefinitionRevisionID, false, &bindings, nil); err != nil {
			t.Error(err)
		}
	}
	handler := queryapi.NewHandler(a.deps.Conns, a.deps.Tunnels, a.deps.History, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps.Controls, drivers)
	defer handler.Close()
	ctx, err := a.deps.UserRepo.BindWorkspace(a.actor(), 1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"connection_id": c.ID, "key": key})
	request := httptest.NewRequest("DELETE", "/api/v1/redis/key", bytes.NewReader(body)).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.RedisMutation("delete")(response, request)
	if response.Code < 400 || strings.Contains(response.Body.String(), `"deleted":true`) {
		t.Errorf("revoked session still dispatched delete: %d", response.Code)
	}
	if value, err := a.deps.Redis.Get(context.Background(), key).Result(); err != nil || value != "must-survive" {
		t.Fatal("revoked Redis operation changed target data", err)
	}
}

func TestSecurityAuditReportBeneficiaryCannotBypassProposal(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	reviewer := securityReviewer(a)
	for _, offline := range []bool{false, true} {
		t.Run(fmt.Sprintf("offline-%v", offline), func(t *testing.T) {
			a.t = t
			id, body := f.draft("SELECT id FROM public." + f.view)
			path := fmt.Sprintf("/managed-reports/%d", id)
			first := f.submit(id, 1)
			f.approve(first, f.validate(first, []int64{3}, nil))
			f.release(id, 0, first, "publish")
			if offline {
				f.release(id, 1, 0, "unpublish")
			}
			body["request_id"], body["version"], body["name"] = testRequestID(), 1, "ordinary new draft"
			a.call("PUT", path+"/draft", a.developer, body, 200)
			review := f.submit(id, 2)
			validationBody := map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{1, 3}}
			// The existing publisher cannot become a new recipient by using
			// ordinary draft submission instead of the audience-change route.
			a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), a.admin, validationBody, 400)
			validationBody["request_id"] = testRequestID()
			a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), reviewer, validationBody, 200)
			a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), reviewer, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validationBody["request_id"]}, 200)
		})
	}
}

func TestSecurityAuditCookieMutationNeedsCSRFHeader(t *testing.T) {
	a := setupAPI(t)
	for _, csrf := range []bool{false, true} {
		body := strings.NewReader(`{"name":"CSRF regression","sql_text":"SELECT 1","visibility":"private"}`)
		req, err := http.NewRequest("POST", a.server.URL+"/api/v1/snippets", body)
		if err != nil {
			t.Fatal(err)
		}
		// text/plain is a browser-simple cross-origin request. CORS alone
		// cannot prevent it from reaching the server with same-site cookies.
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Origin", "https://untrusted.example.test")
		req.AddCookie(&http.Cookie{Name: "dbhub_access_token", Value: a.admin})
		want := 403
		if csrf {
			req.Header.Set("X-DBHub-CSRF", "1")
			want = 200
		}
		res, err := a.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("cookie mutation with csrf=%v returned %d, want %d", csrf, res.StatusCode, want)
		}
	}
}
