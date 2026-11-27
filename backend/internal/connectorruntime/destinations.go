package connectorruntime

import (
	"os"
	"strings"

	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/targetallowlist"
)

// ValidateDestination 检查数据源及代理是否符合平台的出站目标白名单。
func ValidateDestination(c *db.Connection) error {
	if c.Type == "jdbc" && c.Template != nil && c.Template.Driver.ClassName == "org.h2.Driver" {
		return nil
	}
	value := strings.TrimSpace(os.Getenv(targetallowlist.Env))
	rules, parseErr := targetallowlist.Parse(value)
	check := func(host string, port int) error {
		if !targetallowlist.ValidEndpoint(host, port) {
			return &connector.Error{Kind: "invalid_config", Message: "目标地址或端口无效"}
		}
		if value == "" && os.Getenv("APP_ENV") != "production" {
			return nil
		}
		if parseErr == nil && rules.Allows(host, port) {
			return nil
		}
		return &connector.Error{Kind: "target_denied", Message: "目标地址未获平台允许，请联系管理员配置 DBHUB_TARGET_ALLOWLIST"}
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
