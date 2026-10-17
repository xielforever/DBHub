package netx

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/user/dbhub/internal/db"
)

func TestHTTPConnectAuthenticationAndTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:secret")) {
			w.WriteHeader(407)
			return
		}
		c, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer c.Close()
		_, _ = b.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = b.Flush()
		_, _ = io.Copy(c, c)
	}))
	defer server.Close()
	host, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	p := &db.Proxy{Type: "http", Host: host, Port: n, Username: "user", Password: "secret", Status: "active"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dial, _, e := Dialer(ctx, p, nil)
	if e != nil {
		t.Fatal(e)
	}
	c, e := dial(ctx, "tcp", "database.internal:5432")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = c.Write([]byte("probe"))
	b := make([]byte, 5)
	if _, e = io.ReadFull(c, b); e != nil || string(b) != "probe" {
		t.Fatal("CONNECT did not carry actual bytes")
	}
	c.Close()
	p.Password = "wrong"
	if e = Test(ctx, p, "database.internal:5432"); e == nil {
		t.Fatal("invalid proxy authentication returned success")
	}
}
func TestSOCKS5HandshakeAndErrors(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		reader := bufio.NewReader(c)
		greeting := make([]byte, 3)
		_, _ = io.ReadFull(reader, greeting)
		_, _ = c.Write([]byte{5, 2})
		head := make([]byte, 2)
		_, _ = io.ReadFull(reader, head)
		username := make([]byte, int(head[1]))
		_, _ = io.ReadFull(reader, username)
		size, _ := reader.ReadByte()
		password := make([]byte, int(size))
		_, _ = io.ReadFull(reader, password)
		if string(username) != "u" || string(password) != "p" {
			_, _ = c.Write([]byte{1, 1})
			return
		}
		_, _ = c.Write([]byte{1, 0})
		request := make([]byte, 4)
		_, _ = io.ReadFull(reader, request)
		size, _ = reader.ReadByte()
		address := make([]byte, int(size)+2)
		_, _ = io.ReadFull(reader, address)
		if string(address[:size]) != "database.internal" || binary.BigEndian.Uint16(address[size:]) != 5432 {
			return
		}
		_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
		_, _ = io.Copy(c, c)
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	p := &db.Proxy{Type: "socks5", Host: host, Port: n, Username: "u", Password: "p", Status: "active"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dial, _, e := Dialer(ctx, p, nil)
	if e != nil {
		t.Fatal(e)
	}
	c, e := dial(ctx, "tcp", "database.internal:5432")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_, _ = c.Write([]byte("ok"))
	reply := make([]byte, 2)
	if _, e = io.ReadFull(c, reply); e != nil || string(reply) != "ok" {
		t.Fatal("SOCKS5 forwarding failed")
	}
}
func TestHTTPSProxyRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	host, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	p := &db.Proxy{Type: "https", Host: host, Port: n, Status: "active"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := Test(ctx, p, "database.internal:5432"); e == nil {
		t.Fatal("proxy TLS verification bypassed")
	}
}
