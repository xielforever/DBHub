package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/user/dbhub/internal/db"
)

func TestHTTPConnectionDiagnostics(t *testing.T) {
	a := connectorFixture(t)
	base, password, err := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := a.call("GET", "/connector-drivers/available", a.admin, nil, 200)["items"].([]any)
	templates := map[string]int64{}
	for _, raw := range artifacts {
		v := raw.(map[string]any)
		product := v["name"].(string)
		if product != "postgresql" && product != "mysql" {
			continue
		}
		d := a.call("POST", "/connector-drivers", a.admin, map[string]any{"artifact_id": v["id"], "name": "diagnostics " + product, "visibility": "global"}, 201)
		dialect, url := "postgres", "jdbc:postgresql://{host}:{port}/{database}"
		if product == "mysql" {
			dialect, url = "mysql", "jdbc:mysql://{host}:{port}/{database}"
		}
		profile := a.call("POST", "/datasource-templates", a.admin, templateBody("diagnostics-"+product, product, product, int64(d["id"].(float64)), dialect, url, false), 201)
		templates[product] = int64(profile["id"].(float64))
	}
	if len(templates) != 2 {
		t.Fatal("diagnostic JDBC drivers unavailable")
	}
	wrong := "IncorrectFixtureCredential9!"
	jdbc := func(c *db.Connection, product string) *db.Connection {
		n := *c
		n.Type = "jdbc"
		id := templates[product]
		n.TemplateRevisionID = &id
		n.Parameters = map[string]string{"host": c.Host, "port": strconv.Itoa(c.Port), "database": c.Database}
		return &n
	}
	for _, kind := range []string{"native", "jdbc"} {
		for _, failure := range []string{"authentication_failed", "database_not_found", "network_unreachable", "tls_failed"} {
			t.Run(kind+"-"+failure, func(t *testing.T) {
				a.t = t
				c := *base
				c.Name = "diagnostic " + kind + " " + failure
				credential := password
				switch failure {
				case "authentication_failed":
					credential = wrong
				case "database_not_found":
					c.Database = "dbhub_missing_diagnostic_database"
				case "network_unreachable":
					c.Port = 1
				case "tls_failed":
					c.SSLMode = "verify-full"
				}
				var target *db.Connection = &c
				if kind == "jdbc" {
					target = jdbc(&c, "postgresql")
				}
				payload := connectionPayload(target, credential)
				if kind == "jdbc" {
					payload["template_revision_id"] = target.TemplateRevisionID
					payload["parameters"] = target.Parameters
				}
				created := a.call("POST", "/connections", a.admin, payload, 201)
				p := fmt.Sprintf("/connections/%d", int64(created["id"].(float64)))
				result := a.call("POST", p+"/test", a.admin, nil, 400)
				checkDiagnostic(t, result, failure, wrong, password)
				stored := a.call("GET", p, a.admin, nil, 200)["last_check"].(map[string]any)
				checkDiagnostic(t, stored, failure, wrong, password)
				if failure == "authentication_failed" {
					payload["password"] = password
					a.call("PUT", p, a.admin, payload, 200)
					recovered := a.call("POST", p+"/test", a.admin, nil, 200)
					if recovered["diagnostic"] != nil {
						t.Fatal("successful recovery retained a failure diagnosis")
					}
				}
			})
		}
	}
	// MySQL distinguishes a missing database from an existing database denied
	// to this account using its vendor code, even when SQLSTATE is the same.
	if dsn := os.Getenv("TEST_MYSQL_DSN"); dsn != "" {
		parsed, e := mysql.ParseDSN(dsn)
		if e != nil {
			t.Fatal("invalid MySQL fixture configuration")
		}
		host, port, e := net.SplitHostPort(parsed.Addr)
		if e != nil {
			t.Fatal(e)
		}
		n, _ := strconv.Atoi(port)
		mysqlBase := &db.Connection{Name: "MySQL diagnostics", Type: "mysql", Host: host, Port: n, Database: parsed.DBName, Username: parsed.User, SSLMode: "disable", ConnectionTimeout: 3, Environment: "test"}
		for _, kind := range []string{"native", "jdbc"} {
			for _, failure := range []string{"authentication_failed", "database_not_found", "permission_denied"} {
				t.Run("mysql-"+kind+"-"+failure, func(t *testing.T) {
					a.t = t
					c := *mysqlBase
					credential := parsed.Passwd
					switch failure {
					case "authentication_failed":
						credential = wrong
					case "database_not_found":
						c.Database = "dbhub_missing_diagnostic_database"
					case "permission_denied":
						c.Database = "mysql"
					}
					var target *db.Connection = &c
					if kind == "jdbc" {
						target = jdbc(&c, "mysql")
					}
					payload := connectionPayload(target, credential)
					if kind == "jdbc" {
						payload["template_revision_id"] = target.TemplateRevisionID
						payload["parameters"] = target.Parameters
					}
					result := a.call("POST", "/connections/test", a.admin, payload, 400)
					// MySQL hides nonexistent databases from accounts lacking access.
					expected := failure
					if failure == "database_not_found" {
						expected = "permission_denied"
					}
					checkDiagnostic(t, result, expected, wrong, parsed.Passwd)
				})
			}
		}
	}
}

func checkDiagnostic(t *testing.T, result map[string]any, code string, secrets ...string) {
	t.Helper()
	d, ok := result["diagnostic"].(map[string]any)
	if !ok || d["code"] != code || d["hint"] == "" {
		t.Fatalf("incorrect diagnostic: expected=%s got=%v", code, d)
	}
	encoded, _ := json.Marshal(result)
	for _, s := range secrets {
		if s != "" && strings.Contains(string(encoded), s) {
			t.Fatal("diagnostic exposed credential")
		}
	}
}
