package connectorruntime

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/db"
)

var profileKey = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,79}$`)
var databaseName = regexp.MustCompile(`^[\pL\pN_.-]{1,80}$`)
var placeholder = regexp.MustCompile(`\{([a-z][a-z0-9_]{0,40})\}`)
var forbiddenURL = regexp.MustCompile(`(?i)(password|user(?:name)?|token|secret|init|runscript|socketfactory|trace_level[^=]*)\s*=`)
var hostName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)

func ValidateTemplate(t *db.DatasourceTemplate) error {
	if !profileKey.MatchString(t.Key) || strings.TrimSpace(t.Name) == "" || len([]rune(t.Name)) > 100 || strings.TrimSpace(t.Product) == "" || len([]rune(t.Product)) > 80 {
		return fmt.Errorf("模板标识、名称或产品名称无效")
	}
	expected := ""
	dialect := ""
	needed := map[string]string{}
	switch t.Driver.ClassName {
	case "org.postgresql.Driver":
		expected = "jdbc:postgresql://{host}:{port}/{database}"
		dialect = "postgres"
		needed = map[string]string{"host": "string", "port": "integer", "database": "string"}
	case "com.mysql.cj.jdbc.Driver":
		expected = "jdbc:mysql://{host}:{port}/{database}"
		dialect = "mysql"
		needed = map[string]string{"host": "string", "port": "integer", "database": "string"}
	case "org.h2.Driver":
		expected = "jdbc:h2:mem:{database};DB_CLOSE_DELAY=-1"
		needed = map[string]string{"database": "string"}
	default:
		if !strings.HasPrefix(t.URLTemplate, "jdbc:") || !strings.Contains(t.URLTemplate, "//{host}:{port}") || len(t.URLTemplate) > 2048 || strings.ContainsAny(t.URLTemplate, "\r\n\t\"' ") || forbiddenURL.MatchString(t.URLTemplate) {
			return fmt.Errorf("通用 JDBC 模板必须使用网络端点占位字段，且不能包含凭据或执行属性")
		}
		expected = t.URLTemplate
		for _, match := range placeholder.FindAllStringSubmatch(expected, -1) {
			kind := "string"
			if match[1] == "port" {
				kind = "integer"
			}
			if match[1] == "username" || match[1] == "password" || match[1] == "token" || match[1] == "secret" {
				return fmt.Errorf("凭据必须独立输入")
			}
			needed[match[1]] = kind
		}
		if strings.ContainsAny(placeholder.ReplaceAllString(expected, ""), "{}") || len(needed) > 20 {
			return fmt.Errorf("URL 占位字段无效")
		}

	}
	if t.URLTemplate != expected {
		return fmt.Errorf("此驱动当前支持的 URL 模板为 %s", expected)
	}
	if t.Dialect != "" && t.Dialect != dialect {
		return fmt.Errorf("SQL 方言与驱动不匹配")
	}
	if len(t.Fields) != len(needed) {
		return fmt.Errorf("模板字段必须与 URL 占位字段一致")
	}
	seen := map[string]bool{}
	for _, f := range t.Fields {
		kind, ok := needed[f.Name]
		if !ok || seen[f.Name] || f.Type != kind || !f.Required || strings.TrimSpace(f.Label) == "" || len([]rune(f.Label)) > 60 {
			return fmt.Errorf("模板字段类型、必填规则或名称无效")
		}
		if f.Default != "" {
			switch f.Name {
			case "port":
				n, e := strconv.Atoi(f.Default)
				if e != nil || n < 1 || n > 65535 {
					return fmt.Errorf("端口默认值无效")
				}
			case "host":
				if net.ParseIP(f.Default) == nil && !hostName.MatchString(f.Default) {
					return fmt.Errorf("主机默认值无效")
				}
			default:
				if !databaseName.MatchString(f.Default) {
					return fmt.Errorf("字段默认值无效")
				}
			}
		}
		seen[f.Name] = true
	}
	return nil
}

// NormalizeParameters produces a fresh immutable target snapshot with no URL credentials.
func NormalizeParameters(t *db.DatasourceTemplate, input map[string]string) (map[string]string, error) {
	if e := ValidateTemplate(t); e != nil {
		return nil, e
	}
	out := map[string]string{}
	known := map[string]bool{}
	for _, f := range t.Fields {
		known[f.Name] = true
		v := strings.TrimSpace(input[f.Name])
		if v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			return nil, fmt.Errorf("%s 必填", f.Label)
		}
		switch f.Name {
		case "host":
			if net.ParseIP(v) == nil && !hostName.MatchString(v) {
				return nil, fmt.Errorf("主机地址无效")
			}
		case "port":
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("端口必须在 1-65535 之间")
			}
			v = strconv.Itoa(n)
		default:
			if !databaseName.MatchString(v) {
				return nil, fmt.Errorf("数据库名仅支持文字、数字、点、横线和下划线，最多 80 字符")
			}
		}
		out[f.Name] = v
	}
	for k := range input {
		if !known[k] {
			return nil, fmt.Errorf("模板不接受参数 %s", k)
		}
	}
	return out, nil
}
func BuildJDBCSpec(c *db.Connection) (*connector.JDBCSpec, error) {
	t := c.Template
	if t == nil || !t.Enabled || !t.Driver.Enabled {
		return nil, db.ErrCatalogDisabled
	}
	if c.ProxyID != nil || c.SSHTunnelID != nil {
		return nil, connector.Unsupported("JDBC 首轮尚未支持代理或 SSH")
	}
	input := map[string]string{}
	for k, v := range c.Parameters {
		input[k] = v
	}
	if _, ok := input["database"]; ok && c.Database != "" {
		input["database"] = c.Database
	} else if c.Database != "" && c.Database != ProfileDatabase(input) {
		return nil, connector.Unsupported("此模板不支持切换数据库")
	}
	params, e := NormalizeParameters(t, input)
	if e != nil {
		return nil, e
	}
	u := t.URLTemplate
	for name, v := range params {
		encoded := v
		if name == "host" {
			if strings.Contains(v, ":") {
				encoded = "[" + v + "]"
			}
		} else if name != "port" {
			encoded = url.PathEscape(v)
		}
		u = strings.ReplaceAll(u, "{"+name+"}", encoded)
	}
	return &connector.JDBCSpec{ArtifactID: t.Driver.ArtifactID, SHA256: t.Driver.SHA256, URL: u, Dialect: t.Dialect, Capabilities: c.SupportedCapabilities()}, nil
}

// ProfileDatabase supplies a stable logical scope even for service-name JDBC URLs.
func ProfileDatabase(params map[string]string) string {
	for _, key := range []string{"database", "service_name", "catalog"} {
		if params[key] != "" {
			return params[key]
		}
	}
	return "current"
}
func KnownJDBCClass(class string) bool {
	return class == "org.postgresql.Driver" || class == "com.mysql.cj.jdbc.Driver" || class == "org.h2.Driver"
}
