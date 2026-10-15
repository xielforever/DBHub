package sshx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/user/dbhub/internal/db"
	"golang.org/x/crypto/ssh"
)

func TestSSHHostKeyVerification(t *testing.T) {
	_, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(private)
	if e != nil {
		t.Fatal(e)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(signer)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		for {
			raw, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer raw.Close()
				conn, chans, reqs, e := ssh.NewServerConn(raw, cfg)
				if e != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					channel, requests, e := ch.Accept()
					if e != nil {
						return
					}
					go ssh.DiscardRequests(requests)
					_, _ = io.Copy(channel, channel)
					channel.Close()
				}
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	tunnel := &db.SSHTunnel{Host: host, Port: n, Username: "test", AuthType: "password", Password: "test-password", HostKey: ssh.FingerprintSHA256(signer.PublicKey())}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, e := DialClient(ctx, tunnel)
	if e != nil {
		t.Fatal(e)
	}
	client.Close()
	tunnel.HostKey = "SHA256:wrong"
	if _, e = DialClient(ctx, tunnel); e == nil {
		t.Fatal("wrong host key accepted")
	}
	tunnel.HostKey = ""
	if _, e = DialClient(ctx, tunnel); e == nil {
		t.Fatal("missing host key accepted")
	}
}
func TestSSHHandshakeRespectsDeadline(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		raw, e := listener.Accept()
		if e == nil {
			defer raw.Close()
			_, _ = io.Copy(io.Discard, raw)
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e = DialClient(ctx, &db.SSHTunnel{Host: host, Port: n, HostKey: "SHA256:unavailable"}); e == nil {
		t.Fatal("nonresponsive SSH peer returned success")
	}
	if time.Since(start) > time.Second {
		t.Fatal("SSH handshake ignored cancellation")
	}
}
