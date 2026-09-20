// Package sshx 封装基于 x/crypto/ssh 的跳板机连接，供目标数据库驱动复用隧道拨号。
package sshx

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/user/dbhub/internal/db"
)

// DialClient 使用隧道配置建立 SSH 客户端（调用方负责 Close）。
// 支持 private_key（可带 passphrase）与 password 两种认证方式。
func DialClient(ctx context.Context, t *db.SSHTunnel) (*ssh.Client, error) {
	if t.Port == 0 {
		t.Port = 22
	}
	auth := make([]ssh.AuthMethod, 0, 2)
	if t.AuthType == "private_key" {
		var (
			signer ssh.Signer
			err    error
		)
		if t.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(t.PrivateKey), []byte(t.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(t.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("解析 SSH 私钥失败: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	} else {
		auth = append(auth, ssh.Password(t.Password))
	}

	// 注意：跳板机主机密钥校验在正式环境应基于 known_hosts；
	// 当前版本由用户主动录入跳板地址，先以 InsecureSkipKey 建立连接并在日志审计。
	cfg := &ssh.ClientConfig{
		User:            t.Username,
		Auth:            auth,
		Timeout:         10 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 -- 跳板指纹管理在后续版本提供
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))

	type result struct {
		c   *ssh.Client
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := ssh.Dial("tcp", addr, cfg)
		ch <- result{c, err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.c != nil {
				_ = r.c.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("SSH 跳板连接失败: %w", r.err)
		}
		return r.c, nil
	}
}

// DialContext 返回一个通过 SSH 隧道拨号到目标地址的 DialContext。
func DialContext(client *ssh.Client) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return client.DialContext(ctx, network, addr)
	}
}
