package target

import (
	"fmt"
	"strings"
)

// friendlyDialErr 把底层驱动的拨号错误转成面向用户的中文提示，
// 避免直接回显 dial tcp / i/o timeout 等技术细节。
func friendlyDialErr(err error, host string, port int) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	addr := fmt.Sprintf("%s:%d", host, port)
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("目标主机 %s 拒绝连接：端口未开放或对应服务未启动", addr)
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "name resolution"):
		return fmt.Errorf("无法解析主机名 %s，请检查地址配置", host)
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "deadline exceeded"),
		strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "connection reset"), strings.Contains(msg, "handshake failure"),
		strings.Contains(msg, "server closed the connection"):
		return fmt.Errorf("连接 %s 超时或不可达，请检查网络、防火墙或 SSH 隧道", addr)
	case strings.Contains(msg, "authentication failed"), strings.Contains(msg, "access denied"),
		strings.Contains(msg, "password"), strings.Contains(msg, "auth"):
		return fmt.Errorf("目标数据库认证失败，请核对用户名与口令（%s）", addr)
	default:
		return fmt.Errorf("无法连接目标数据库 %s：%s", addr, firstLine(err.Error()))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}
