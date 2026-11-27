package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
	crypto "github.com/user/dbhub/pkg/crypto"
)

type apiTest struct {
	t                          *testing.T
	server                     *httptest.Server
	pool                       *pgxpool.Pool
	targetPool                 *pgxpool.Pool
	deps                       *Deps
	admin, developer, readonly string
	connID, redisID            int64
	roleConnections            map[string]int64
	roleRedis                  map[string]int64
	table                      string
	auditDir                   string
}

// Keep unexpected server failures visible while omitting routine request logs.
type testServerLog struct{ t *testing.T }

func (w testServerLog) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func setupAPI(t *testing.T) *apiTest {
	return setupConfiguredAPI(t, "", nil)
}

func setupConfiguredAPI(t *testing.T, listen string, configure func(*config.Config, string)) *apiTest {
	return setupAPIWithDefaultFixture(t, listen, configure, false)
}

func setupDefaultMatrixAPI(t *testing.T) *apiTest {
	t.Helper()
	return setupAPIWithDefaultFixture(t, "", nil, true)
}

func setupAPIWithDefaultFixture(t *testing.T, listen string, configure func(*config.Config, string), defaultMatrix bool) *apiTest {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	metadataDSN := os.Getenv("TEST_METADATA_DSN")
	if metadataDSN == "" {
		metadataDSN = dsn
	}
	redisURL := os.Getenv("TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("real integration databases not configured")
	}
	ctx := context.WithValue(db.WithWorkspace(context.Background(), 1), auth.ClaimsContextKey, &auth.Claims{Subject: 1, Username: "admin", Role: "admin"})
	root, e := pgxpool.New(ctx, metadataDSN)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("http_test_%d", time.Now().UnixNano())
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(metadataDSN)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["dbhub.workspace_id"] = "1"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	if !defaultMatrix {
		// Existing regression cases at workspace 1 carry legacy actors, old
		// account routes and report fixtures predating business ownership.
		if _, e = pool.Exec(ctx, `UPDATE sys_iam_workspace_state SET matrix_enabled=false WHERE workspace_id=1`); e != nil {
			t.Fatal(e)
		}
	}
	secret := []byte("test-main-key-32-bytes-long-123456789")
	cipher, _ := crypto.NewCipher(secret)
	users := db.NewUserRepository(pool)
	hash, _ := auth.HashPassword("TestPassword123!")
	if e = users.EnsureAdmin(ctx, "admin", hash); e != nil {
		t.Fatal(e)
	}
	for _, role := range []string{"developer", "readonly"} {
		if _, e = pool.Exec(ctx, `INSERT INTO sys_users(username,password_hash,role_id) SELECT $1,$2,id FROM sys_roles WHERE code=$1`, role, hash); e != nil {
			t.Fatal(e)
		}
	}
	ro, e := redis.ParseURL(redisURL)
	if e != nil {
		t.Fatal(e)
	}
	rc := redis.NewClient(ro)
	if e = rc.Ping(ctx).Err(); e != nil {
		t.Fatal(e)
	}
	deps := &Deps{UserRepo: users, Conns: db.NewConnectionRepository(pool, cipher), Tunnels: db.NewTunnelRepository(pool, cipher), Proxies: db.NewProxyRepository(pool, cipher), History: db.NewHistoryRepository(pool), Audit: db.NewAuditRepository(pool), Meta: db.NewMetadataRepository(pool), Reports: db.NewReportRepository(pool), Dashboards: db.NewDashboardRepository(pool), Shares: db.NewShareRepository(pool), Tickets: db.NewTicketRepository(pool), Lineage: db.NewLineageRepository(pool), Snippets: db.NewSnippetRepository(pool), Controls: db.NewQueryControls(pool), Sessions: auth.NewSessionStore(rc), Redis: rc}
	appCfg := &config.Config{Env: "development", SecretKey: secret, AccessTTL: time.Hour, RefreshTTL: time.Hour}
	appCfg.AuditDir = t.TempDir() + "/audit"
	deps.AuditJournal, e = auditlog.Open(appCfg.AuditDir, 10000, deps.Audit, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		t.Fatal(e)
	}
	targetPool := root
	if metadataDSN != dsn {
		targetPool, e = pgxpool.New(ctx, dsn)
		if e != nil {
			t.Fatal(e)
		}
	}
	a := &apiTest{t: t, pool: pool, targetPool: targetPool, deps: deps, table: fmt.Sprintf("dbhub_http_%d", time.Now().UnixNano()), auditDir: appCfg.AuditDir}
	a.server = httptest.NewUnstartedServer(nil)
	if listen != "" {
		a.server.Listener.Close()
		a.server.Listener, e = net.Listen("tcp", listen)
		if e != nil {
			t.Fatal(e)
		}
	}
	if configure != nil {
		configure(appCfg, "http://"+a.server.Listener.Addr().String())
	}
	a.server.Config.Handler = NewRouter(appCfg, slog.New(slog.NewTextHandler(testServerLog{t}, &slog.HandlerOptions{Level: slog.LevelError})), users, deps)
	a.server.Start()
	t.Cleanup(func() {
		a.server.Close()
		deps.Queries.Close()
		_ = deps.AuditJournal.Close(context.Background())
		db.CloseScopedRuntime(pool)
		pool.Close()
		_, _ = root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_, _ = targetPool.Exec(ctx, "DROP TABLE IF EXISTS "+pgx.Identifier{a.table}.Sanitize())
		if targetPool != root {
			targetPool.Close()
		}
		root.Close()
		rc.Close()
	})
	for _, name := range []string{"admin", "developer", "readonly"} {
		data := a.call("POST", "/auth/login", "", map[string]any{"username": name, "password": "TestPassword123!"}, 200)
		token := data["access_token"].(string)
		switch name {
		case "admin":
			a.admin = token
		case "developer":
			a.developer = token
		case "readonly":
			a.readonly = token
		}
	}
	u, _ := url.Parse(dsn)
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()
	conn := &db.Connection{UserID: 1, Name: "real-postgres", Type: "postgres", Host: u.Hostname(), Port: port, Database: strings.TrimPrefix(u.Path, "/"), Username: u.User.Username(), SSLMode: "disable", Environment: "dev", ConnectionTimeout: 3}
	conn.Password = password
	if e = deps.Conns.Create(ctx, conn); e != nil {
		t.Fatal(e)
	}
	a.connID = conn.ID
	redisConn := &db.Connection{UserID: 1, Name: "real-redis", Type: "redis", Host: ro.Addr[:strings.LastIndex(ro.Addr, ":")], Port: 6379, Database: strconv.Itoa(ro.DB), SSLMode: "disable", Environment: "dev", ConnectionTimeout: 3}
	_, p, _ := strings.Cut(ro.Addr, ":")
	redisConn.Port, _ = strconv.Atoi(p)
	redisConn.Password = ro.Password
	if e = deps.Conns.Create(ctx, redisConn); e != nil {
		t.Fatal(e)
	}
	a.redisID = redisConn.ID
	a.roleConnections = map[string]int64{a.admin: a.connID}
	a.roleRedis = map[string]int64{a.admin: a.redisID}
	for _, base := range []*db.Connection{conn, redisConn} {
		def := a.call("POST", fmt.Sprintf("/connections/%d/publish", base.ID), a.admin, map[string]any{"visibility": "global"}, 201)
		defID := int64(def["id"].(float64))
		base.DefinitionRevisionID = &defID
		base.DefinitionFamilyID = def["family_id"].(string)
		if e = deps.Conns.Update(ctx, base); e != nil {
			t.Fatal(e)
		}
		for i, token := range []string{a.developer, a.readonly} {
			clone := *base
			clone.ID = 0
			clone.UserID = int64(i + 2)
			actorCtx := context.WithValue(ctx, auth.ClaimsContextKey, &auth.Claims{Subject: clone.UserID, Role: []string{"developer", "readonly"}[i]})
			if e = deps.Conns.Create(actorCtx, &clone); e != nil {
				t.Fatal(e)
			}
			if base.Type == "redis" {
				a.roleRedis[token] = clone.ID
			} else {
				a.roleConnections[token] = clone.ID
			}
		}
	}
	a.exec(a.admin, "CREATE TABLE "+a.table+" (id INT PRIMARY KEY, owner_id INT, email TEXT, secret TEXT)", 200)
	a.exec(a.admin, "INSERT INTO "+a.table+" VALUES(1,1,'a@example.com','one'),(2,2,'b@example.com','two')", 200)
	return a
}
func (a *apiTest) actor() context.Context {
	return context.WithValue(db.WithWorkspace(context.Background(), 1), auth.ClaimsContextKey, &auth.Claims{Subject: 1, Username: "admin", Role: "admin"})
}
func (a *apiTest) connectionFor(token string) int64 {
	if id, ok := a.roleConnections[token]; ok {
		return id
	}
	return a.connID
}
func (a *apiTest) personalFor(sourceID int64, token, password string) int64 {
	a.t.Helper()
	c, e := a.deps.Conns.Get(a.actor(), sourceID)
	if e != nil {
		a.t.Fatal(e)
	}
	defID := *c.DefinitionRevisionID
	if c.Definition.Visibility != "global" {
		d := a.call("POST", fmt.Sprintf("/connections/%d/publish", sourceID), a.admin, map[string]any{"visibility": "global"}, 201)
		defID = int64(d["id"].(float64))
		a.call("POST", fmt.Sprintf("/connections/%d/revision", sourceID), a.admin, map[string]any{"definition_revision_id": defID, "name": c.Name, "username": c.Username, "password": password, "confirmed": true}, 200)
	}
	uid := int64(2)
	if token == a.readonly {
		uid = 3
	}
	if token == a.admin {
		return sourceID
	}
	var existing int64
	_ = a.pool.QueryRow(a.actor(), `SELECT id FROM sys_connections WHERE user_id=$1 AND definition_revision_id=$2`, uid, defID).Scan(&existing)
	if existing > 0 {
		return existing
	}
	own := a.call("POST", fmt.Sprintf("/datasource-definitions/%d/connections", defID), token, map[string]any{"name": "personal-test", "username": c.Username, "password": password, "confirmed": true}, 201)
	return int64(own["id"].(float64))
}
func (a *apiTest) call(method, path, token string, body any, want int) map[string]any {
	a.t.Helper()
	if method == "POST" && (path == "/query/execute" || path == "/reports") {
		if payload, ok := body.(map[string]any); ok {
			if _, exists := payload["request_id"]; !exists {
				payload["request_id"] = testRequestID()
			}
		}
	}
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, e := http.NewRequest(method, a.server.URL+"/api/v1"+path, reader)
	if e != nil {
		a.t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, e := a.server.Client().Do(req)
	if e != nil {
		a.t.Fatal(e)
	}
	defer res.Body.Close()
	var out struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if e = json.NewDecoder(res.Body).Decode(&out); e != nil {
		a.t.Fatalf("%s %s invalid response", method, path)
	}
	if res.StatusCode != want {
		a.t.Fatalf("%s %s: got %d want %d: %s", method, path, res.StatusCode, want, out.Message)
	}
	data := map[string]any{}
	if len(out.Data) > 0 {
		_ = json.Unmarshal(out.Data, &data)
	}
	return data
}
func testRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}
func (a *apiTest) exec(token, sql string, want int) map[string]any {
	return a.call("POST", "/query/execute", token, map[string]any{"connection_id": a.connectionFor(token), "sql": sql}, want)
}
func TestRealHTTPBackend(t *testing.T) {
	a := setupAPI(t)
	t.Run("postgres-xml-result", func(t *testing.T) {
		a.t = t
		result := a.exec(a.admin, `SELECT '<sample>示例</sample>'::xml, decode('00ff', 'hex'), NULL::xml`, 200)
		values := result["rows"].([]any)[0].([]any)
		if values[0] != "<sample>示例</sample>" || values[1] != "AP8=" || values[2] != nil {
			t.Fatalf("unexpected XML, bytea, or NULL result values: %#v", values)
		}
		plan := a.exec(a.admin, "EXPLAIN (FORMAT XML) SELECT id FROM "+a.table, 200)
		value, ok := plan["rows"].([]any)[0].([]any)[0].(string)
		if !ok || !strings.HasPrefix(value, "<explain") {
			t.Fatalf("expected XML plan text, got %q", value)
		}
	})
	t.Run("query-and-transaction-isolation", func(t *testing.T) {
		a.t = t
		result := a.exec(a.admin, "SELECT email FROM "+a.table+" ORDER BY id", 200)
		if rows := result["rows"].([]any); len(rows) != 2 || rows[0].([]any)[0] != "a@example.com" {
			t.Fatal("real rows missing")
		}
		bound := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "sql": "SELECT email FROM " + a.table + " WHERE id=:id", "params": map[string]any{"id": 1}}, 200)
		if bound["rows"].([]any)[0].([]any)[0] != "a@example.com" {
			t.Fatal("HTTP parameters were not bound correctly")
		}
		a.exec(a.admin, "SELECT invalid_column FROM "+a.table, 400)
		a.exec(a.readonly, "DELETE FROM "+a.table+" WHERE id=1", 400)
		a.exec(a.readonly, "WITH x AS (DELETE FROM "+a.table+" RETURNING *) SELECT * FROM x", 400)
		tx := a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": a.connID}, 200)
		id := tx["transaction_id"]
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": id, "sql": "INSERT INTO " + a.table + " VALUES(3,1,'c@example.com','three')"}, 200)
		if len(a.exec(a.admin, "SELECT * FROM "+a.table, 200)["rows"].([]any)) != 2 {
			t.Fatal("ordinary query inherited the user's active transaction")
		}
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": "invalid", "sql": "SELECT 1"}, 400)
		inside := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": id, "sql": "SELECT * FROM " + a.table}, 200)
		if len(inside["rows"].([]any)) != 3 {
			t.Fatal("explicit transaction was not preserved after an invalid reference")
		}
		if len(a.exec(a.developer, "SELECT * FROM "+a.table, 200)["rows"].([]any)) != 2 {
			t.Fatal("uncommitted write leaked")
		}
		a.call("POST", "/query/transaction/commit", a.developer, map[string]any{"connection_id": a.connID, "transaction_id": id}, 404)
		a.call("POST", "/query/transaction/rollback", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": id}, 200)
		if len(a.exec(a.admin, "SELECT * FROM "+a.table, 200)["rows"].([]any)) != 2 {
			t.Fatal("rollback failed")
		}
	})
	t.Run("mysql-http-transaction-survives-begin-response", func(t *testing.T) {
		a.t = t
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("MySQL not configured")
		}
		cfg, e := mysql.ParseDSN(dsn)
		if e != nil {
			t.Fatal(e)
		}
		host, port, _ := net.SplitHostPort(cfg.Addr)
		n, _ := strconv.Atoi(port)
		conn := &db.Connection{UserID: 1, Name: "HTTP MySQL", Type: "mysql", Host: host, Port: n, Database: cfg.DBName, Username: cfg.User, Password: cfg.Passwd, SSLMode: "disable", Environment: "dev"}
		if e = a.deps.Conns.Create(a.actor(), conn); e != nil {
			t.Fatal(e)
		}
		roMySQL := a.personalFor(conn.ID, a.readonly, cfg.Passwd)
		devMySQL := a.personalFor(conn.ID, a.developer, cfg.Passwd)
		run := func(sql string, token string, want int) map[string]any {
			id := conn.ID
			if token == a.readonly {
				id = roMySQL
			} else if token == a.developer {
				id = devMySQL
			}
			return a.call("POST", "/query/execute", token, map[string]any{"connection_id": id, "sql": sql}, want)
		}
		runTx := func(sql string, id any) map[string]any {
			return a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": conn.ID, "transaction_id": id, "sql": sql}, 200)
		}
		run("CREATE TABLE "+a.table+" (id INT PRIMARY KEY)", a.admin, 200)
		defer run("DROP TABLE "+a.table, a.admin, 200)
		begin := a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": conn.ID}, 200)
		runTx("INSERT INTO "+a.table+" VALUES(1)", begin["transaction_id"])
		if len(run("SELECT * FROM "+a.table, a.admin, 200)["rows"].([]any)) != 0 {
			t.Fatal("ordinary MySQL query inherited the user's active transaction")
		}
		if len(run("SELECT * FROM "+a.table, a.developer, 200)["rows"].([]any)) != 0 {
			t.Fatal("uncommitted MySQL transaction leaked")
		}
		a.call("POST", "/query/transaction/commit", a.admin, map[string]any{"connection_id": conn.ID, "transaction_id": begin["transaction_id"]}, 200)
		if len(run("SELECT * FROM "+a.table, a.admin, 200)["rows"].([]any)) != 1 {
			t.Fatal("MySQL commit did not persist")
		}
		begin = a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": conn.ID}, 200)
		runTx("INSERT INTO "+a.table+" VALUES(2)", begin["transaction_id"])
		a.call("POST", "/query/transaction/rollback", a.admin, map[string]any{"connection_id": conn.ID, "transaction_id": begin["transaction_id"]}, 200)
		if len(run("SELECT * FROM "+a.table, a.admin, 200)["rows"].([]any)) != 1 {
			t.Fatal("MySQL rollback failed")
		}
		requestID := testRequestID()
		write := map[string]any{"request_id": requestID, "connection_id": conn.ID, "sql": "UPDATE " + a.table + " SET id=id+10 WHERE id=1"}
		a.call("POST", "/query/execute", a.admin, write, 200)
		a.call("POST", "/query/execute", a.admin, write, 409)
		if receipt := a.call("GET", "/query/executions/"+requestID, a.admin, nil, 200); receipt["status"] != "succeeded" || receipt["affected_rows"] != float64(1) {
			t.Fatal("MySQL write receipt was not preserved")
		}
		run("DELETE FROM "+a.table+" WHERE 1=1", a.admin, 400)
		if rows := run("SELECT * FROM "+a.table, a.admin, 200)["rows"].([]any); len(rows) != 1 || rows[0].([]any)[0] != float64(11) {
			t.Fatalf("MySQL duplicate or unsafe write modified data: %v", rows)
		}
	})
	t.Run("workbench-history-utf8-boundary", func(t *testing.T) {
		a.t = t
		const prefix = "SELECT 1 AS p34_utf8 /*"
		ascii := prefix + strings.Repeat("x", 9999-len(prefix))
		id := testRequestID()
		a.call("POST", "/query/execute", a.admin, map[string]any{"request_id": id, "connection_id": a.connID, "sql": ascii + "中*/"}, 200)
		records := a.call("GET", "/query/history?page=1&page_size=10", a.admin, nil, 200)["items"].([]any)
		found := false
		for _, item := range records {
			found = found || item.(map[string]any)["sql_text"] == ascii
		}
		if !found {
			t.Fatal("successful Unicode query was missing from history after byte-boundary truncation")
		}
		if receipt := a.call("GET", "/query/executions/"+id, a.admin, nil, 200); receipt["status"] != "succeeded" {
			t.Fatalf("successful query receipt changed: %v", receipt)
		}
	})
	t.Run("workbench-script-ownership", func(t *testing.T) {
		a.t = t
		const scripts = "/query/scripts"
		const folders = "/query/script-folders"
		folder := a.call("POST", folders, a.developer, map[string]any{"name": "分析"}, 200)
		folderID := int64(folder["id"].(float64))
		private := a.call("POST", scripts, a.developer,
			map[string]any{"name": "private", "content": "SELECT 1", "script_type": "postgres", "folder_id": folderID}, 200)
		path := fmt.Sprintf("%s/%d", scripts, int64(private["id"].(float64)))
		a.call("PATCH", path, a.readonly, map[string]any{"version": 1, "name": "stolen"}, 404)
		if len(a.call("GET", scripts+"?view=shared", a.readonly, nil, 200)["items"].([]any)) != 0 {
			t.Fatal("private script leaked")
		}
		candidates := a.call("GET", path+"/share-candidates?q=readonly", a.developer, nil, 200)["items"].([]any)
		if len(candidates) != 1 {
			t.Fatalf("expected one eligible reader, got %d", len(candidates))
		}
		readerID := int64(candidates[0].(map[string]any)["user_id"].(float64))
		grantPath := fmt.Sprintf("%s/readers/%d", path, readerID)
		a.call("PUT", grantPath, a.developer, nil, 200)
		a.call("PUT", grantPath, a.developer, nil, 200)
		a.call("GET", path, a.admin, nil, 404)
		a.call("PATCH", path, a.readonly, map[string]any{"version": 1, "name": "stolen"}, 403)
		a.call("GET", path+"/readers", a.readonly, nil, 403)
		if len(a.call("GET", scripts+"?view=shared", a.readonly, nil, 200)["items"].([]any)) != 1 {
			t.Fatal("shared script missing")
		}
		if shared := a.call("GET", path, a.readonly, nil, 200); shared["content"] != "SELECT 1" ||
			shared["read_only"] != true || shared["default_target"] != nil || shared["folder_id"] != nil || shared["version"] != nil {
			t.Fatalf("shared script returned author-only data or incorrect content: %v", shared)
		}
		a.call("PATCH", fmt.Sprintf("%s/%d", folders, folderID), a.developer, map[string]any{"name": "数据分析"}, 200)
		a.call("PATCH", path, a.developer, map[string]any{"version": 1, "content": "SELECT 2", "folder_id": nil}, 200)
		a.call("PATCH", path, a.developer, map[string]any{"version": 1, "content": "SELECT 3"}, 409)
		if shared := a.call("GET", path, a.readonly, nil, 200); shared["content"] != "SELECT 2" {
			t.Fatal("moving the owner's script lost the reader grant or used stale content")
		}
		var otherSpace int64
		if err := a.pool.QueryRow(a.actor(), "INSERT INTO sys_workspaces(name) VALUES('脚本隔离验收') RETURNING id").Scan(&otherSpace); err != nil {
			t.Fatal(err)
		}
		if _, err := a.pool.Exec(a.actor(), "INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES($1,$2,'readonly')", otherSpace, readerID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := a.pool.Exec(a.actor(), "DELETE FROM sys_workspace_members WHERE workspace_id=$1 AND user_id=$2", otherSpace, readerID); err != nil {
				t.Error(err)
			}
		})
		prefix := fmt.Sprintf("/workspaces/%d", otherSpace)
		a.call("GET", prefix+path, a.readonly, nil, 404)
		if len(a.call("GET", prefix+scripts+"?view=shared", a.readonly, nil, 200)["items"].([]any)) != 0 {
			t.Fatal("shared script leaked across workspaces")
		}
		a.call("DELETE", grantPath, a.developer, nil, 200)
		a.call("DELETE", grantPath, a.developer, nil, 200)
		a.call("GET", "/workspaces/1"+path, a.readonly, nil, 404)
		var auditCount int
		if err := a.pool.QueryRow(a.actor(), `SELECT count(*) FROM sys_audit_logs
			WHERE action IN ('SCRIPT_READ_GRANT','SCRIPT_READ_REVOKE') AND context->>'script_id'=$1`,
			strings.TrimPrefix(path, scripts+"/")).Scan(&auditCount); err != nil || auditCount != 2 {
			t.Fatalf("reader changes must each have one durable audit entry: %d %v", auditCount, err)
		}
		a.call("POST", scripts, a.developer, map[string]any{"name": "huge", "content": strings.Repeat("x", 1<<20), "script_type": "postgres"}, 400)
	})
	t.Run("workbench-script-targets", func(t *testing.T) {
		a.t = t
		const scripts = "/query/scripts"
		postgres, err := a.deps.Conns.Get(a.actor(), a.connID)
		if err != nil {
			t.Fatal(err)
		}
		redis, err := a.deps.Conns.Get(a.actor(), a.redisID)
		if err != nil {
			t.Fatal(err)
		}
		type bindingSample struct {
			kind       string
			connection int64
			database   string
		}
		targets := []bindingSample{
			{"postgres", a.connID, postgres.Database},
			{"redis", a.redisID, redis.Database},
		}
		if raw := os.Getenv("TEST_MYSQL_DSN"); raw != "" {
			config, err := mysql.ParseDSN(raw)
			if err != nil {
				t.Fatal(err)
			}
			host, port, err := net.SplitHostPort(config.Addr)
			if err != nil {
				t.Fatal(err)
			}
			portNumber, err := strconv.Atoi(port)
			if err != nil {
				t.Fatal(err)
			}
			conn := &db.Connection{UserID: 1, Name: "script MySQL", Type: "mysql", Host: host, Port: portNumber, Database: config.DBName,
				Username: config.User, Password: config.Passwd, SSLMode: "disable", Environment: "test"}
			if err = a.deps.Conns.Create(a.actor(), conn); err != nil {
				t.Fatal(err)
			}
			targets = append(targets, bindingSample{"mysql", conn.ID, conn.Database})
		}
		for _, target := range targets {
			request := map[string]any{"name": target.kind + " fixture", "content": "SELECT 1", "script_type": target.kind}
			request["default_target"] = map[string]any{"connection_id": target.connection, "database": target.database}
			script := a.call("POST", scripts, a.admin, request, 200)
			id := int64(script["id"].(float64))
			path := fmt.Sprintf("%s/%d", scripts, id)
			if reply := script["default_target"].(map[string]any); reply["state"] != "available" || reply["database"] != target.database {
				t.Fatalf("invalid default target: %v", reply)
			}
			a.call("POST", scripts, a.developer, request, 400)
			if target.kind != "redis" {
				requestID := testRequestID()
				a.call("POST", "/query/execute", a.admin, map[string]any{"script_id": id, "request_id": requestID, "connection_id": target.connection, "database": target.database, "sql": "SELECT 1"}, 200)
				receipt := a.call("GET", "/query/executions/"+requestID, a.admin, nil, 200)
				if receipt["status"] != "succeeded" || receipt["database"] != target.database {
					t.Fatalf("incorrect execution receipt: %v", receipt)
				}
			}
			wrongTarget := map[string]any{"connection_id": a.connID, "database": "absent_script_db_fixture"}
			a.call("PATCH", path, a.admin, map[string]any{"version": 1, "default_target": wrongTarget}, 400)
			if _, err := a.pool.Exec(a.actor(), `UPDATE sys_connections SET name=name WHERE id=$1`, target.connection); err != nil {
				t.Fatal(err)
			}
			stale := a.call("GET", path, a.admin, nil, 200)["default_target"].(map[string]any)
			if stale["state"] != "unavailable" {
				t.Fatalf("updated connection must require re-confirmation: %v", stale)
			}
			a.call("PATCH", path, a.admin, map[string]any{"version": 1, "default_target": request["default_target"]}, 200)
		}
	})
	t.Run("real-redis-all-types-and-ttl", func(t *testing.T) {
		a.t = t
		base := a.table + ":"
		types := []struct {
			kind  string
			value any
		}{{"string", "hello"}, {"hash", map[string]any{"field": "value"}}, {"list", []string{"one", "two"}}, {"set", []string{"one"}}, {"zset", []map[string]any{{"member": "one", "score": 1}}}}
		for _, item := range types {
			key := base + item.kind
			payload := map[string]any{"connection_id": a.redisID, "key": key, "type": item.kind, "value": item.value, "ttl": 60}
			a.call("POST", "/redis/key", a.admin, payload, 200)
			a.call("POST", "/redis/key", a.admin, payload, 409)
			a.call("DELETE", "/redis/key", a.readonly, map[string]any{"connection_id": a.redisID, "key": key}, 403)
		}
		mutate := func(method, path, kind string, fields map[string]any) {
			fields["connection_id"] = a.redisID
			fields["key"] = base + kind
			a.call(method, path, a.admin, fields, 200)
		}
		mutate("PUT", "/redis/key/value", "string", map[string]any{"value": "updated"})
		mutate("PUT", "/redis/key/ttl", "string", map[string]any{"ttl": -1})
		got := a.call("GET", fmt.Sprintf("/redis/value?connection_id=%d&key=%sstring", a.redisID, base), a.admin, nil, 200)
		if got["value"] != "updated" || got["ttl"].(float64) != -1 {
			t.Fatal("real string/TTL mismatch")
		}
		mutate("PUT", "/redis/hash/field", "hash", map[string]any{"field": "f2", "value": "v2"})
		mutate("DELETE", "/redis/hash/field", "hash", map[string]any{"field": "f2"})
		mutate("POST", "/redis/list/push", "list", map[string]any{"value": "zero", "direction": "left"})
		mutate("POST", "/redis/list/pop", "list", map[string]any{"direction": "right"})
		mutate("POST", "/redis/set/member", "set", map[string]any{"member": "two"})
		mutate("DELETE", "/redis/set/member", "set", map[string]any{"member": "two"})
		mutate("POST", "/redis/zset/member", "zset", map[string]any{"member": "two", "score": 2})
		mutate("DELETE", "/redis/zset/member", "zset", map[string]any{"member": "two"})
		for _, item := range types {
			mutate("DELETE", "/redis/key", item.kind, map[string]any{})
		}
		a.call("GET", fmt.Sprintf("/redis/value?connection_id=%d&key=%sstring", a.roleRedis[a.readonly], base), a.readonly, nil, 404)
	})
	t.Run("policies-preview-sharing-and-enforcement", func(t *testing.T) {
		a.t = t
		connection, err := a.deps.Conns.Get(a.actor(), a.connID)
		if err != nil {
			t.Fatal(err)
		}
		policy := map[string]any{"connection_id": a.connID, "database_name": connection.Database, "schema_name": "public", "table_name": a.table, "role_code": "readonly", "column_permissions": map[string]any{"email": "masked", "secret": "hidden"}, "row_conditions": []map[string]any{{"column": "owner_id", "operator": "=", "value": 1}}}
		a.call("PUT", "/query/policies", a.developer, policy, 403)
		p := a.call("PUT", "/query/policies", a.admin, policy, 200)
		res := a.exec(a.readonly, "SELECT * FROM "+a.table+" WHERE owner_id=2 OR 1=1 ORDER BY id", 200)
		rows := res["rows"].([]any)
		if len(rows) != 1 || len(rows[0].([]any)) != 3 || rows[0].([]any)[2] != nil {
			t.Fatal("policy did not filter/mask before returning")
		}
		a.exec(a.readonly, "SELECT secret FROM "+a.table, 400)
		a.exec(a.readonly, "SELECT * FROM "+a.table+" UNION SELECT * FROM "+a.table, 400)
		preview := a.call("GET", fmt.Sprintf("/data/preview?connection_id=%d&schema=public&table=%s", a.connectionFor(a.readonly), a.table), a.readonly, nil, 200)
		if preview["total"].(float64) != 1 || len(preview["rows"].([]any)) != 1 {
			t.Fatal("preview bypassed policy")
		}
		a.call("POST", "/query/shares", a.readonly, map[string]any{"name": "private snapshot", "connection_id": a.connectionFor(a.readonly), "sql": "SELECT * FROM " + a.table, "include_result": true}, 403)
		a.call("GET", "/public/query-shares/old-token", "", nil, 404)
		export := a.call("POST", "/query/exports", a.readonly, map[string]any{"connection_id": a.connectionFor(a.readonly), "sql": "SELECT * FROM " + a.table, "format": "json", "reason": "verify authorized snapshot"}, 200)
		eid := int64(export["id"].(float64))
		epath := fmt.Sprintf("/query/exports/%d", eid)
		a.call("GET", epath+"/download", a.readonly, nil, 404)
		a.call("POST", epath+"/review", a.developer, map[string]any{"status": "approved"}, 403)
		a.call("POST", epath+"/review", a.admin, map[string]any{"status": "approved"}, 200)
		approved := a.call("GET", epath+"/download", a.readonly, nil, 200)
		if len(approved["result"].(map[string]any)["rows"].([]any)) != 1 {
			t.Fatal("export bypassed row policy")
		}
		a.call("GET", epath+"/download", a.developer, nil, 404)
		a.call("DELETE", fmt.Sprintf("/query/policies/%d", int64(p["id"].(float64))), a.admin, nil, 200)
	})
	t.Run("proxy-ownership-and-real-database-forwarding", func(t *testing.T) {
		a.t = t
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-test-pass")) {
				w.WriteHeader(407)
				return
			}
			target, e := net.DialTimeout("tcp", r.Host, time.Second)
			if e != nil {
				w.WriteHeader(502)
				return
			}
			client, b, e := w.(http.Hijacker).Hijack()
			if e != nil {
				target.Close()
				return
			}
			_, _ = b.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			_ = b.Flush()
			go func() { defer client.Close(); defer target.Close(); _, _ = io.Copy(target, client) }()
			_, _ = io.Copy(client, target)
		}))
		defer proxy.Close()
		host, port, _ := net.SplitHostPort(proxy.Listener.Addr().String())
		n, _ := strconv.Atoi(port)
		p := a.call("POST", "/proxies", a.developer, map[string]any{"name": "test proxy", "type": "http", "host": host, "port": n, "username": "proxy-user", "password": "proxy-test-pass"}, 200)
		id := int64(p["id"].(float64))
		a.call("PUT", fmt.Sprintf("/proxies/%d", id), a.readonly, map[string]any{"name": "stolen"}, 403)
		devCtx := context.WithValue(a.actor(), auth.ClaimsContextKey, &auth.Claims{Subject: 2, Role: "developer"})
		conn, password, e := a.deps.Conns.GetSecrets(devCtx, a.connectionFor(a.developer))
		if e != nil {
			t.Fatal(e)
		}
		a.call("POST", "/proxies/test", a.developer, map[string]any{"id": id, "target_host": conn.Host, "target_port": conn.Port}, 200)
		a.call("POST", "/proxies/test", a.developer, map[string]any{"type": "http", "host": host, "port": n, "username": "proxy-user", "password": "wrong", "target_host": conn.Host, "target_port": conn.Port}, 400)
		conn.ID = 0
		conn.DefinitionRevisionID = nil
		conn.DefinitionFamilyID = ""
		conn.Name = "forwarded postgres"
		conn.Password = password
		conn.ProxyID = &id
		if e = a.deps.Conns.Create(devCtx, conn); e != nil {
			t.Fatal(e)
		}
		result := a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": conn.ID, "sql": "SELECT 42 AS forwarded"}, 200)
		if result["rows"].([]any)[0].([]any)[0].(float64) != 42 {
			t.Fatal("database did not execute through proxy")
		}
	})
	t.Run("ticket-requires-review-and-executes-once", func(t *testing.T) {
		a.t = t
		body := map[string]any{"title": "approved update", "type": "dml", "table": a.table, "connection_id": a.connectionFor(a.developer), "sql": "UPDATE " + a.table + " SET secret='changed' WHERE id=1", "risk": "low"}
		created := a.call("POST", "/tickets", a.developer, body, 200)
		id := int64(created["id"].(float64))
		path := fmt.Sprintf("/tickets/%d", id)
		a.call("POST", path+"/status", a.developer, map[string]any{"status": "approved"}, 403)
		a.call("POST", path+"/execute", a.admin, nil, 409)
		a.call("POST", path+"/status", a.admin, map[string]any{"status": "executed"}, 400)
		a.call("POST", path+"/status", a.admin, map[string]any{"status": "approved"}, 200)
		a.call("POST", path+"/execute", a.admin, nil, 200)
		a.call("POST", path+"/execute", a.admin, nil, 409)
		if a.exec(a.admin, "SELECT secret FROM "+a.table+" WHERE id=1", 200)["rows"].([]any)[0].([]any)[0] != "changed" {
			t.Fatal("ticket did not execute")
		}
		body["title"] = "failed update"
		body["sql"] = "UPDATE missing_table SET id=1 WHERE id=2"
		created = a.call("POST", "/tickets", a.developer, body, 200)
		path = fmt.Sprintf("/tickets/%d", int64(created["id"].(float64)))
		a.call("POST", path+"/status", a.admin, map[string]any{"status": "approved"}, 200)
		a.call("POST", path+"/execute", a.admin, nil, 400)
		if a.call("GET", path, a.admin, nil, 200)["status"] != "failed" {
			t.Fatal("failed ticket marked successful")
		}
	})
	t.Run("presence-ai-settings-queue-cancellation", func(t *testing.T) {
		a.t = t
		a.call("POST", "/query/presence", a.admin, map[string]any{"connection_id": a.connID}, 200)
		online := a.call("POST", "/query/presence", a.developer, map[string]any{"connection_id": a.connectionFor(a.developer)}, 200)
		if len(online["items"].([]any)) != 1 {
			t.Fatal("presence did not use real sessions")
		}
		if a.call("GET", "/ai/status", a.admin, nil, 200)["configured"] != false {
			t.Fatal("AI unexpectedly configured")
		}
		a.call("POST", "/ai/complete", a.admin, map[string]any{"prompt": "test"}, 503)
		a.call("PUT", "/query/settings", a.developer, map[string]any{"timeout_ms": 1000, "max_rows": 1, "block_unsafe_write": false}, 403)
		a.call("PUT", "/query/settings", a.admin, map[string]any{"timeout_ms": 30000, "max_rows": 1, "block_unsafe_write": true}, 200)
		res := a.exec(a.admin, "SELECT * FROM "+a.table, 200)
		if len(res["rows"].([]any)) != 1 || res["truncated"] != true {
			t.Fatal("server max rows not enforced")
		}
		done := make(chan struct{})
		go func() { defer close(done); a.exec(a.admin, "SELECT pg_sleep(10)", 400) }()
		jobID := ""
		for i := 0; i < 50; i++ {
			items := a.call("GET", "/query/jobs", a.admin, nil, 200)["items"].([]any)
			for _, item := range items {
				j := item.(map[string]any)
				if j["sql"] == "SELECT pg_sleep(10)" && j["status"] == "running" {
					jobID = j["id"].(string)
				}
			}
			if jobID != "" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if jobID == "" {
			t.Fatal("running query missing")
		}
		a.call("POST", "/query/jobs/"+jobID+"/cancel", a.developer, nil, 404)
		a.call("POST", "/query/jobs/"+jobID+"/cancel", a.admin, nil, 200)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("database query was not cancelled")
		}
	})
	t.Run("logout-refresh-replay-disabled-user", func(t *testing.T) {
		a.t = t
		login := a.call("POST", "/auth/login", "", map[string]any{"username": "readonly", "password": "TestPassword123!"}, 200)
		access := login["access_token"].(string)
		refresh := login["refresh_token"].(string)
		rotated := a.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": refresh}, 200)
		a.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": refresh}, 401)
		a.call("POST", "/auth/logout", access, nil, 200)
		a.call("GET", "/auth/me", access, nil, 401)
		a.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": rotated["refresh_token"]}, 401)
		_, e := a.pool.Exec(context.Background(), `UPDATE sys_users SET is_active=FALSE WHERE username='developer'`)
		if e != nil {
			t.Fatal(e)
		}
		a.call("GET", "/auth/me", a.developer, nil, 401)
	})
}
