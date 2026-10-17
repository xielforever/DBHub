package connectorruntime

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/db"
)

// Exact host:port entries are an operator-managed boundary. In production an
// empty allowlist denies outbound target connections, including connection tests.
func ValidateDestination(c *db.Connection) error {
	if c.Type == "jdbc" && c.Template != nil && c.Template.Driver.ClassName == "org.h2.Driver" {
		return nil
	}
	check := func(host string, port int) error {
		if strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/\\@?#;=\r\n\t ") || port < 1 || port > 65535 {
			return fmt.Errorf("目标地址或端口无效")
		}
		rules := strings.TrimSpace(os.Getenv("DBHUB_TARGET_ALLOWLIST"))
		if rules == "" && os.Getenv("APP_ENV") != "production" {
			return nil
		}
		target := strings.ToLower(net.JoinHostPort(strings.TrimSuffix(host, "."), strconv.Itoa(port)))
		for _, entry := range strings.Split(rules, ",") {
			if strings.ToLower(strings.TrimSpace(entry)) == target {
				return nil
			}
		}
		return fmt.Errorf("目标地址未获平台允许，请联系管理员配置 DBHUB_TARGET_ALLOWLIST")
	}
	if e := check(c.Host, c.Port); e != nil {
		return e
	}
	if c.Proxy != nil {
		if e := check(c.Proxy.Host, c.Proxy.Port); e != nil {
			return e
		}
	}
	return nil
}
