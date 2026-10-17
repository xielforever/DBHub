package connectorruntime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/connector"
)

func diagnostic(code string) connector.Diagnostic {
	texts := map[string][3]string{
		"authentication_failed": {"数据库身份验证失败", "核对本人的数据库账号、密码及账号登录来源限制；更新个人凭据后重新测试。", "update_credentials"},
		"permission_denied":     {"数据库账号权限不足", "请数据库管理员检查本人账号的连接、目标库及版本信息读取权限。", "contact_admin"},
		"database_not_found":    {"目标数据库或服务不存在", "核对发布的数据库或服务名；共享目标请联系发布者修正并发布新修订。", "review_target"},
		"tls_failed":            {"TLS 握手或证书验证失败", "检查服务器证书、信任链、主机名和 TLS 支持；请联系管理员修复配置后重新测试。", "contact_admin"},
		"dns_failed":            {"无法解析目标主机名", "请管理员检查目标域名和服务端 DNS；共享目标变更需要发布新修订。", "review_target"},
		"network_unreachable":   {"无法连接目标网络端点", "检查目标地址、端口、防火墙、代理和数据库监听状态；请联系管理员确认网络可达。", "review_target"},
		"timeout":               {"连接检查超时", "检查网络和数据库负载；确认目标可达后可手动重新测试。", "retry_check"},
		"capacity_exceeded":     {"连接或并发容量已满", "稍后手动重新测试；持续发生时请管理员检查数据库和连接服务的容量。", "retry_check"},
		"driver_unavailable":    {"驱动服务暂不可用", "请管理员检查 JDBC 服务、服务认证、已安装制品及驱动规则状态。", "contact_admin"},
		"target_denied":         {"连接目标未获平台允许", "请平台管理员确认目标及代理端点在允许范围内。", "contact_admin"},
		"configuration_invalid": {"连接配置或能力不受支持", "核对数据源类型、驱动规则及连接参数；共享配置请联系发布者。", "review_target"},
		"canceled":              {"连接检查已取消", "本次检查未完成，需要时可手动重新测试。", "retry_check"},
		"unknown":               {"连接检查失败，原因尚未确定", "请联系管理员根据检查时间和数据源定位问题；不要反复修改密码或自动重试。", "contact_admin"},
	}
	v, ok := texts[code]
	if !ok {
		code = "unknown"
		v = texts[code]
	}
	return connector.Diagnostic{Code: code, Title: v[0], Hint: v[1], Action: v[2]}
}

// DiagnoseConnectionError classifies known protocol codes and typed causes. An
// unrecognized error stays unknown; it is never guessed to be a bad password.
func DiagnoseConnectionError(err error) connector.Diagnostic {
	if errors.Is(err, context.Canceled) {
		return diagnostic("canceled")
	}
	var certificate *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	var record tls.RecordHeaderError
	if errors.As(err, &certificate) || errors.As(err, &unknownCA) || errors.As(err, &hostname) || errors.As(err, &invalidCertificate) || errors.As(err, &record) {
		return diagnostic("tls_failed")
	}
	// These fixed driver messages represent a server refusing TLS before a
	// certificate exists. Inspect only for classification, never for display.
	for cause, n := err, 0; cause != nil && n < 20; cause, n = errors.Unwrap(cause), n+1 {
		message := cause.Error()
		if message == "TLS requested but server does not support TLS" || strings.Contains(message, "server refused TLS connection") {
			return diagnostic("tls_failed")
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return diagnostic("timeout")
	}
	var c *connector.Error
	if errors.As(err, &c) {
		switch c.Kind {
		case "authentication_failed", "permission_denied", "database_not_found", "tls_failed", "dns_failed", "network_unreachable", "target_denied":
			return diagnostic(c.Kind)
		case "deadline":
			return diagnostic("timeout")
		case "transport", "unavailable", "authentication", "artifact_changed":
			return diagnostic("driver_unavailable")
		case "resource_exhausted":
			return diagnostic("capacity_exceeded")
		case "unsupported", "invalid_config":
			return diagnostic("configuration_invalid")
		}
	}
	state := ""
	if c != nil {
		state = c.SQLState
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		state = p.Code
	}
	var m *mysql.MySQLError
	if errors.As(err, &m) {
		switch m.Number {
		case 1045, 1698:
			return diagnostic("authentication_failed")
		case 1044, 1142, 1143, 1227:
			return diagnostic("permission_denied")
		case 1049:
			return diagnostic("database_not_found")
		case 1040, 1203:
			return diagnostic("capacity_exceeded")
		case 3159:
			return diagnostic("tls_failed")
		}
		state = string(m.SQLState[:])
	}
	switch {
	case strings.HasPrefix(state, "28"):
		return diagnostic("authentication_failed")
	case state == "42501":
		return diagnostic("permission_denied")
	case state == "3D000":
		return diagnostic("database_not_found")
	case state == "53300":
		return diagnostic("capacity_exceeded")
	}
	for _, prefix := range []string{"WRONGPASS", "NOAUTH"} {
		if redis.HasErrorPrefix(err, prefix) {
			return diagnostic("authentication_failed")
		}
	}
	if redis.HasErrorPrefix(err, "NOPERM") {
		return diagnostic("permission_denied")
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		if dns.IsTimeout {
			return diagnostic("timeout")
		}
		return diagnostic("dns_failed")
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return diagnostic("timeout")
	}
	var op *net.OpError
	if errors.As(err, &op) || strings.HasPrefix(state, "08") {
		return diagnostic("network_unreachable")
	}
	return diagnostic("unknown")
}
