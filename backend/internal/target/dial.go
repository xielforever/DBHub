package target

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"sync"

	"github.com/go-sql-driver/mysql"

	"github.com/user/dbhub/internal/db"
)

// registerMySQLDial 注册一个请求级的 MySQL 自定义拨号器（走 SSH 隧道），
// 返回拨号器名；调用方在连接关闭时 DeregisterDialContext。
func registerMySQLDial(dial func(ctx context.Context, network, addr string) (net.Conn, error)) string {
	name := "dbhub-ssh-" + strconv.FormatInt(mysqlDialSeq.Add(1), 10)
	mysql.RegisterDialContext(name, func(ctx context.Context, addr string) (net.Conn, error) {
		return dial(ctx, "tcp", addr)
	})
	return name
}

var tlsRegisterOnce sync.Once

// mysqlTLSName 根据连接的 SSL 模式返回 MySQL 驱动的 TLS 配置名。
// require / verify-ca：加密但跳过主机名校验（自签证书场景）；
// verify-full：完整校验证书链与主机名。
func mysqlTLSName(conn *db.Connection) string {
	tlsRegisterOnce.Do(func() {
		_ = mysql.RegisterTLSConfig(tlsSkipName, &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- 对应用户显式选择的 require 模式
		_ = mysql.RegisterTLSConfig(tlsVerifyName, &tls.Config{MinVersion: tls.VersionTLS12})
	})
	if conn.SSLMode == "verify-full" {
		return tlsVerifyName
	}
	return tlsSkipName
}

const (
	tlsSkipName   = "dbhub-skip-verify"
	tlsVerifyName = "dbhub-verify"
)
