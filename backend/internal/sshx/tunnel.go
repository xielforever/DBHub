// Package sshx 封装基于 x/crypto/ssh 的跳板机连接，供目标数据库驱动复用隧道拨号。
package sshx

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/db"
	"golang.org/x/crypto/ssh"
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

	if !strings.HasPrefix(t.HostKey, "SHA256:") {
		return nil, fmt.Errorf("请配置跳板机 host_key（SHA256 主机公钥指纹）")
	}
	cfg := &ssh.ClientConfig{
		User:    t.Username,
		Auth:    auth,
		Timeout: 10 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			if ssh.FingerprintSHA256(key) != t.HostKey {
				return fmt.Errorf("SSH 主机公钥指纹不匹配")
			}
			return nil
		},
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))

	raw, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SSH 跳板连接失败")
	}
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = raw.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	connection, channels, requests, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("SSH 认证或主机密钥校验失败")
	}
	_ = raw.SetDeadline(time.Time{})
	return ssh.NewClient(connection, channels, requests), nil

}

// DialContext 返回一个通过 SSH 隧道拨号到目标地址的 DialContext。
func DialContext(client *ssh.Client) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return client.DialContext(ctx, network, addr)
	}
}
