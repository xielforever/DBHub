// Package targetallowlist 解析并匹配平台配置的数据源目标规则。
package targetallowlist

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Env 是保存数据源目标规则的环境变量。
const Env = "DBHUB_TARGET_ALLOWLIST"

// Rules 包含精确端点和 IP 网段。
type Rules struct {
	endpoints map[string]struct{}
	prefixes  []netip.Prefix
}

// Parse 解析逗号分隔的主机:端口和 IP CIDR，空值表示无规则。
func Parse(value string) (Rules, error) {
	if strings.TrimSpace(value) == "" {
		return Rules{}, nil
	}
	rules := Rules{endpoints: make(map[string]struct{})}
	for i, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			rules.prefixes = append(rules.prefixes, prefix.Masked())
			continue
		}
		host, port, err := net.SplitHostPort(entry)
		number, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || !ValidEndpoint(host, number) {
			return Rules{}, fmt.Errorf("第 %d 项必须为主机:端口或 IP CIDR", i+1)
		}
		rules.endpoints[strings.ToLower(entry)] = struct{}{}
	}
	return rules, nil
}

// ValidEndpoint 检查目标主机格式和端口范围。
func ValidEndpoint(host string, port int) bool {
	return strings.TrimSpace(host) != "" && !strings.ContainsAny(host, "/\\@?#;=\r\n\t ") &&
		port >= 1 && port <= 65535
}

// Allows 检查已校验的目标。CIDR 仅匹配字面 IP，避免 DNS 结果在检查后变化。
func (r Rules) Allows(host string, port int) bool {
	endpoint := net.JoinHostPort(strings.TrimSuffix(host, "."), strconv.Itoa(port))
	if _, ok := r.endpoints[strings.ToLower(endpoint)]; ok {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, prefix := range r.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
