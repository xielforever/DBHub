package target

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/db"
)

// 只在显式版本矩阵运行，环境缺失/版本不匹配均必须失败，不能静默跳过。
func TestVersionMatrixTarget(t *testing.T) {
	kind := os.Getenv("DBHUB_MATRIX_ENGINE")
	if kind == "" {
		t.Skip("explicit version matrix only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var conn *db.Connection
	var password string
	if kind == "redis" {
		cfg, err := redis.ParseURL(os.Getenv("TEST_REDIS_URL"))
		if err != nil {
			t.Fatal("invalid Redis test configuration")
		}
		host, rawPort, err := net.SplitHostPort(cfg.Addr)
		if err != nil {
			t.Fatal(err)
		}
		port, _ := strconv.Atoi(rawPort)
		conn = &db.Connection{Type: kind, Host: host, Port: port, Database: strconv.Itoa(cfg.DB), Username: cfg.Username, SSLMode: "disable"}
		password = cfg.Password
	} else if kind == "postgres" || kind == "mysql" {
		name := "TEST_POSTGRES_DSN"
		if kind == "mysql" {
			name = "TEST_MYSQL_DSN"
		}
		if os.Getenv(name) == "" {
			t.Fatal("matrix target configuration missing")
		}
		conn, password = integrationConnection(t, kind)
	} else {
		t.Fatal("invalid matrix engine")
	}
	info, err := Test(ctx, conn, password, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Compatibility.Engine != kind || info.Compatibility.Version == "" || info.Compatibility.Branch != os.Getenv("DBHUB_MATRIX_BRANCH") {
		t.Fatalf("unexpected database product/version: %+v", info.Compatibility)
	}
	if kind == "redis" {
		r, err := OpenRedis(ctx, conn, password, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		overview, err := RedisOverviewInfo(ctx, r.Client)
		if err != nil || overview.Version != info.Version || overview.Mode != "standalone" {
			t.Fatalf("INFO overview unavailable: %v", err)
		}
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			t.Fatal(err)
		}
		user := "dbhub_version_" + hex.EncodeToString(nonce[:])
		aclPassword := hex.EncodeToString(nonce[:]) + "-AclProbe!9"
		if err := r.Client.Do(ctx, "ACL", "SETUSER", user, "on", ">"+aclPassword, "~*", "+@all", "-info").Err(); err != nil {
			t.Fatal("could not prepare restricted INFO account")
		}
		defer r.Client.Do(context.Background(), "ACL", "DELUSER", user)
		limited := *conn
		limited.Username = user
		unavailable, err := Test(ctx, &limited, aclPassword, nil)
		if err != nil || unavailable.Compatibility.Status != "unknown" || unavailable.Version != "" {
			t.Fatal("INFO denial must not become fabricated version verification")
		}
	}
	// 只记录规范化版本，不记录 DSN、用户、口令或服务端完整 INFO。
	raw, _ := json.Marshal(info.Compatibility)
	t.Log("DBHUB_COMPAT_RESULT " + string(raw))
}
