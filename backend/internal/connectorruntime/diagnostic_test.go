package connectorruntime

import (
	"context"
	"crypto/x509"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/connector"
)

type fixtureRedisError string

func (e fixtureRedisError) Error() string { return string(e) }
func (fixtureRedisError) RedisError()     {}

func TestConnectionDiagnosticsPreserveMeaningWithoutDriverText(t *testing.T) {
	secret := "private-fixture-DSN-token-credential"
	cases := []struct {
		name, code string
		err        error
	}{
		{"pg-auth", "authentication_failed", &pgconn.PgError{Code: "28P01", Message: secret}},
		{"pg-permission", "permission_denied", &pgconn.PgError{Code: "42501", Message: secret}},
		{"mysql-auth", "authentication_failed", &mysql.MySQLError{Number: 1045, Message: secret}},
		{"mysql-permission", "permission_denied", &mysql.MySQLError{Number: 1044, Message: secret}},
		{"mysql-database", "database_not_found", &mysql.MySQLError{Number: 1049, Message: secret}},
		{"jdbc-auth", "authentication_failed", &connector.Error{Kind: "statement_rejected", SQLState: "28000", Message: secret}},
		{"jdbc-tls", "tls_failed", &connector.Error{Kind: "tls_failed", Message: secret}},
		{"jdbc-service-auth", "driver_unavailable", &connector.Error{Kind: "authentication", Message: secret}},
		{"jdbc-service-network", "driver_unavailable", &connector.Error{Kind: "transport", Message: secret}},
		{"tls", "tls_failed", x509.HostnameError{Certificate: &x509.Certificate{}, Host: secret}},
		{"dns", "dns_failed", &net.DNSError{Err: secret, Name: secret, IsNotFound: true}},
		{"network", "network_unreachable", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{"eof", "connection_lost", io.EOF},
		{"partial-packet", "connection_lost", io.ErrUnexpectedEOF},
		{"mysql-invalid", "connection_lost", &connector.Error{Kind: "connection_failed", Message: secret, Cause: mysql.ErrInvalidConn}},
		{"sql-bad-connection", "connection_lost", driver.ErrBadConn},
		{"network-eof", "connection_lost", &net.OpError{Op: "read", Net: "tcp", Err: io.EOF}},
		{"text-only-invalid", "unknown", errors.New("invalid connection " + secret)},
		{"canceled-before-disconnect", "canceled", errors.Join(context.Canceled, mysql.ErrInvalidConn)},
		{"deadline-before-disconnect", "timeout", errors.Join(context.DeadlineExceeded, io.EOF)},
		{"timeout", "timeout", fmt.Errorf("%s: %w", secret, context.DeadlineExceeded)},
		{"canceled", "canceled", context.Canceled},
		{"redis-auth", "authentication_failed", fixtureRedisError("WRONGPASS " + secret)},
		{"redis-permission", "permission_denied", fixtureRedisError("NOPERM " + secret)},
		{"capacity", "capacity_exceeded", &connector.Error{Kind: "resource_exhausted", Message: secret}},
		{"unknown", "unknown", errors.New(secret)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := DiagnoseConnectionError(fmt.Errorf("safe wrapper: %w", c.err))
			if d.Code != c.code {
				t.Fatalf("code=%s, want=%s", d.Code, c.code)
			}
			raw, _ := json.Marshal(d)
			if strings.Contains(string(raw), secret) || d.Hint == "" || d.Action == "" {
				t.Fatal("diagnostic exposed driver text or lacks recovery guidance")
			}
		})
	}
}

func TestMySQLGreetingDisconnectDiagnostic(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		// 不发送完整欢迎包，复现驱动读取报头时的unexpected EOF。
		_, err = conn.Write([]byte{10, 0})
		conn.Close()
		done <- err
	}()
	cfg := mysql.NewConfig()
	cfg.Net, cfg.Addr = "tcp", listener.Addr().String()
	driver, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := driver.Connect(ctx)
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("incomplete greeting unexpectedly connected")
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	wrapped := &connector.Error{Kind: "connection_failed", Message: "safe message", Cause: err}
	if got := DiagnoseConnectionError(wrapped); got.Code != "connection_lost" {
		t.Fatalf("diagnostic=%s driver error type=%T", got.Code, err)
	}
}
