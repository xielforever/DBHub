package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/user/dbhub/internal/db"
)

func TestVersionCompatibilityHTTP(t *testing.T) {
	a := setupAPI(t)
	a.call("GET", "/connections/compatibility", "", nil, 401)
	for _, token := range []string{a.admin, a.developer, a.readonly} {
		matrix := a.call("GET", "/connections/compatibility", token, nil, 200)
		if len(matrix["items"].([]any)) != 12 {
			t.Fatal("incomplete compatibility matrix")
		}
	}
	kind := os.Getenv("DBHUB_MATRIX_ENGINE")
	if kind == "" {
		kind = "postgres"
	}
	id := a.connID
	if kind == "redis" {
		id = a.redisID
	} else if kind == "mysql" {
		cfg, err := mysql.ParseDSN(os.Getenv("TEST_MYSQL_DSN"))
		if err != nil {
			t.Fatal("invalid MySQL test configuration")
		}
		host, rawPort, err := net.SplitHostPort(cfg.Addr)
		if err != nil {
			t.Fatal(err)
		}
		port, _ := strconv.Atoi(rawPort)
		conn := &db.Connection{UserID: 1, Name: "version-mysql", Type: "mysql", Host: host, Port: port, Database: cfg.DBName, Username: cfg.User, Password: cfg.Passwd, SSLMode: "disable", Environment: "dev", ConnectionTimeout: 3}
		if err := a.deps.Conns.Create(context.Background(), conn); err != nil {
			t.Fatal(err)
		}
		id = conn.ID
	}
	for _, token := range []string{a.admin, a.developer, a.readonly} {
		result := a.call("POST", fmt.Sprintf("/connections/%d/test", id), token, nil, 200)
		compatibility, ok := result["compatibility"].(map[string]any)
		if result["ok"] != true || !ok || compatibility["engine"] != kind || compatibility["version"] == "" || compatibility["message"] == "" {
			t.Fatal("connection test lost version identity or compatibility boundary")
		}
		if branch := os.Getenv("DBHUB_MATRIX_BRANCH"); branch != "" && compatibility["branch"] != branch {
			t.Fatal("HTTP test reached a different version branch")
		}
	}
}
