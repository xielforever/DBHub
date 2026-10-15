// Package netx implements verified target transports shared by database drivers.
package netx

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/sshx"
)

type DialFunc func(context.Context, string, string) (net.Conn, error)

func Dialer(ctx context.Context, p *db.Proxy, t *db.SSHTunnel) (DialFunc, func() error, error) {
	if p != nil && t != nil {
		return nil, nil, fmt.Errorf("代理与 SSH 隧道不能同时启用")
	}
	direct := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	if t != nil {
		client, e := sshx.DialClient(ctx, t)
		if e != nil {
			return nil, nil, e
		}
		return sshx.DialContext(client), client.Close, nil
	}
	if p == nil {
		return direct, nil, nil
	}
	if p.Status != "active" {
		return nil, nil, fmt.Errorf("代理未启用")
	}
	if p.Host == "" || p.Port < 1 || p.Port > 65535 {
		return nil, nil, fmt.Errorf("代理地址无效")
	}
	if p.Type != "http" && p.Type != "https" && p.Type != "socks5" && p.Type != "db_proxy" {
		return nil, nil, fmt.Errorf("不支持该代理协议")
	}
	return func(c context.Context, network, addr string) (net.Conn, error) {
		endpoint := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
		raw, e := direct(c, "tcp", endpoint)
		if e != nil {
			return nil, fmt.Errorf("代理连接失败")
		}
		ok := false
		defer func() {
			if !ok {
				_ = raw.Close()
			}
		}()
		deadline := time.Now().Add(10 * time.Second)
		if d, exists := c.Deadline(); exists && d.Before(deadline) {
			deadline = d
		}
		_ = raw.SetDeadline(deadline)
		if p.Type == "https" {
			secure := tls.Client(raw, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.Host})
			if e = secure.HandshakeContext(c); e != nil {
				return nil, fmt.Errorf("HTTPS 代理证书或握手校验失败")
			}
			raw = secure
		}
		if p.Type == "http" || p.Type == "https" {
			req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
			if p.Username != "" {
				req.SetBasicAuth(p.Username, p.Password)
				req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
				req.Header.Del("Authorization")
			}
			if e = req.Write(raw); e != nil {
				return nil, fmt.Errorf("代理 CONNECT 请求失败")
			}
			reader := bufio.NewReader(raw)
			response, e := http.ReadResponse(reader, req)
			if e != nil || response.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("代理拒绝 CONNECT（检查认证与目标权限）")
			}
			raw = &bufferedConn{Conn: raw, reader: reader}
		} else if p.Type == "socks5" {
			if e = socksConnect(raw, p, addr); e != nil {
				return nil, e
			}
		}
		_ = raw.SetDeadline(time.Time{})
		ok = true
		return raw, nil
	}, nil, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

func socksConnect(c net.Conn, p *db.Proxy, addr string) error {
	methods := []byte{5, 1, 0}
	if p.Username != "" {
		methods = []byte{5, 1, 2}
	}
	if _, e := c.Write(methods); e != nil {
		return fmt.Errorf("SOCKS5 握手失败")
	}
	response := make([]byte, 2)
	if _, e := io.ReadFull(c, response); e != nil || response[0] != 5 {
		return fmt.Errorf("SOCKS5 握手失败")
	}
	if response[1] == 2 {
		if p.Username == "" {
			return fmt.Errorf("SOCKS5 认证方式不匹配")
		}
		if len(p.Username) > 255 || len(p.Password) > 255 {
			return fmt.Errorf("SOCKS5 凭据过长")
		}
		auth := append([]byte{1, byte(len(p.Username))}, p.Username...)
		auth = append(auth, byte(len(p.Password)))
		auth = append(auth, p.Password...)
		if _, e := c.Write(auth); e != nil {
			return fmt.Errorf("SOCKS5 认证失败")
		}
		if _, e := io.ReadFull(c, response); e != nil || response[0] != 1 || response[1] != 0 {
			return fmt.Errorf("SOCKS5 认证失败")
		}
	} else if response[1] != 0 || p.Username != "" {
		return fmt.Errorf("SOCKS5 认证方式不匹配")
	}
	host, port, e := net.SplitHostPort(addr)
	if e != nil {
		return fmt.Errorf("SOCKS5 目标无效")
	}
	number, e := strconv.Atoi(port)
	if e != nil || number < 1 || number > 65535 {
		return fmt.Errorf("SOCKS5 目标端口无效")
	}
	request := []byte{5, 1, 0}
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		request = append(request, 1)
		request = append(request, ip4...)
	} else if ip != nil {
		request = append(request, 4)
		request = append(request, ip.To16()...)
	} else {
		if len(host) > 255 {
			return fmt.Errorf("目标主机名过长")
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(number))
	if _, e = c.Write(request); e != nil {
		return fmt.Errorf("SOCKS5 CONNECT 失败")
	}
	head := make([]byte, 4)
	if _, e = io.ReadFull(c, head); e != nil || head[0] != 5 || head[1] != 0 {
		return fmt.Errorf("SOCKS5 目标连接被拒绝")
	}
	size := 0
	switch head[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		b := []byte{0}
		if _, e = io.ReadFull(c, b); e != nil {
			return e
		}
		size = int(b[0])
	default:
		return fmt.Errorf("SOCKS5 地址响应无效")
	}
	_, e = io.CopyN(io.Discard, c, int64(size+2))
	return e
}

// Test verifies actual proxy transport; a destination checks CONNECT as well.
func Test(ctx context.Context, p *db.Proxy, destination string) error {
	if destination == "" && p.Type != "db_proxy" {
		return fmt.Errorf("代理测试必须提供目标地址")
	}
	if destination == "" {
		destination = net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	}

	dial, cleanup, e := Dialer(ctx, p, nil)
	if e != nil {
		return e
	}
	if cleanup != nil {
		defer cleanup()
	}
	c, e := dial(ctx, "tcp", destination)
	if e != nil {
		return e
	}
	return c.Close()
}
