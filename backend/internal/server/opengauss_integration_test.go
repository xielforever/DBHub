package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/connector/jdbc"
	"os"
	"strings"
	"testing"
)

// The cloud demo harness creates and destroys the real target. The regular suite
// explicitly skips it when no isolated openGauss fixture is supplied.
func TestOpenGaussDemoHTTP(t *testing.T) {
	path := os.Getenv("DBHUB_OPENGAUSS_FIXTURE")
	if path == "" {
		t.Skip("ephemeral openGauss target not configured")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("cannot read openGauss fixture")
	}
	var f struct{ Host, Password string }
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("invalid fixture")
	}
	a := connectorFixture(t)
	driver := a.call("POST", "/connector-drivers", a.admin, map[string]any{"visibility": "global", "artifact_id": "opengauss-6.0.0-og", "name": "openGauss 6.0"}, 201)
	body := templateBody("opengauss-demo", "openGauss PG mode", "openGauss", int64(driver["id"].(float64)), "postgres", "jdbc:opengauss://{host}:{port}/{database}", false)
	template := a.call("POST", "/datasource-templates", a.admin, body, 201)
	source := map[string]any{"name": "openGauss demo", "type": "jdbc", "template_revision_id": template["id"], "parameters": map[string]string{"host": f.Host, "port": "5432", "database": "postgres"}, "username": "demo_writer", "password": f.Password, "ssl_mode": "disable", "environment": "test"}
	c := a.call("POST", "/connections", a.admin, source, 201)
	id := int64(c["id"].(float64))
	p := fmt.Sprintf("/connections/%d", id)
	t.Run("official-driver-connection-and-version", func(t *testing.T) { a.t = t; a.call("POST", p+"/test", a.admin, nil, 200) })
	query := func(sql string) map[string]any {
		return a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": id, "sql": sql}, 200)
	}
	t.Run("parameters-null-and-decimal", func(t *testing.T) {
		a.t = t
		r := a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": id, "sql": "SELECT :label::text, NULL::text, 1234567890.123456789::numeric(30,9), current_user", "params": map[string]any{"label": "O'Reilly"}}, 200)
		row := r["rows"].([]any)[0].([]any)
		if row[0] != "O'Reilly" || row[1] != nil || row[2] != "1234567890.123456789" || row[3] != "demo_writer" {
			t.Fatal("typed result mismatch")
		}
	})
	t.Run("create-insert-update-delete", func(t *testing.T) {
		a.t = t
		query("DROP TABLE IF EXISTS public.dbhub_demo")
		query("CREATE TABLE public.dbhub_demo (id INTEGER PRIMARY KEY, label VARCHAR(80))")
		query("INSERT INTO public.dbhub_demo VALUES (1,'initial')")
		query("UPDATE public.dbhub_demo SET label='updated' WHERE id=1")
		query("INSERT INTO public.dbhub_demo VALUES (2,'remove')")
		query("DELETE FROM public.dbhub_demo WHERE id=2")
	})
	t.Run("metadata-and-collection", func(t *testing.T) {
		a.t = t
		items := a.call("GET", fmt.Sprintf("/metadata/columns?connection_id=%d&database=postgres&schema=public&table=dbhub_demo", id), a.admin, nil, 200)["items"].([]any)
		if len(items) != 2 || items[0].(map[string]any)["is_primary"] != true {
			t.Fatal("primary key metadata mismatch")
		}
		a.call("POST", "/assets/sync", a.admin, map[string]any{"connection_id": id}, 200)
	})
	t.Run("transaction-rollback-and-commit", func(t *testing.T) {
		a.t = t
		for _, op := range []string{"rollback", "commit"} {
			tx := a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": id}, 200)
			a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": id, "transaction_id": tx["transaction_id"], "sql": "UPDATE public.dbhub_demo SET label='committed' WHERE id=1"}, 200)
			a.call("POST", "/query/transaction/"+op, a.admin, map[string]any{"connection_id": id, "transaction_id": tx["transaction_id"]}, 200)
			r := query("SELECT label FROM public.dbhub_demo WHERE id=1")
			want := "updated"
			if op == "commit" {
				want = "committed"
			}
			if r["rows"].([]any)[0].([]any)[0] != want {
				t.Fatal("transaction outcome mismatch")
			}
		}
	})
	t.Run("server-readonly-and-personal-identity", func(t *testing.T) {
		a.t = t
		ro := a.personalFor(id, a.readonly, f.Password)
		a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": ro, "sql": "DELETE FROM public.dbhub_demo"}, 400)
		r := query("SELECT count(*) FROM public.dbhub_demo")
		if r["rows"].([]any)[0].([]any)[0] != "1" {
			t.Fatal("readonly operation modified data")
		}
		a.call("GET", p, a.readonly, nil, 404)
	})
	t.Run("database-itself-rejects-readonly-write", func(t *testing.T) {
		client := jdbc.NewClient(os.Getenv("DBHUB_JDBC_URL"), os.Getenv("DBHUB_JDBC_TOKEN"))
		artifacts, e := client.Artifacts(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		hash := ""
		for _, v := range artifacts {
			if v.ID == "opengauss-6.0.0-og" {
				hash = v.SHA256
			}
		}
		session, e := (jdbc.Driver{Client: client}).Open(context.Background(), connector.OpenSpec{Source: connector.Source{Database: "postgres"}, Username: "demo_writer", Password: f.Password, SSLMode: "disable", JDBC: &connector.JDBCSpec{ArtifactID: "opengauss-6.0.0-og", SHA256: hash, URL: "jdbc:opengauss://" + f.Host + ":5432/postgres", Dialect: "postgres", Capabilities: connector.Capabilities{SQL: true}}})
		if e != nil {
			t.Fatal(e)
		}
		defer session.Close(context.Background())
		_, e = session.(connector.SQLExecutor).Execute(context.Background(), connector.ExecuteRequest{SQL: "DELETE FROM public.dbhub_demo", ReadOnly: true})
		var ce *connector.Error
		if !errors.As(e, &ce) || !strings.Contains(ce.Message, "25006") {
			t.Fatal("target failed to return readonly SQLSTATE 25006")
		}
	})

	t.Run("timeout-does-not-exhaust-service", func(t *testing.T) {
		a.t = t
		a.call("POST", "/query/execute", a.admin, map[string]any{"connection_id": id, "sql": "SELECT pg_sleep(5)", "timeout_ms": 1000}, 400)
		query("SELECT 1")
	})
}
