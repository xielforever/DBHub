package db

import "github.com/user/dbhub/internal/iam"

// Unknown versions are preserved for a compatible future executor. Reading a
// receipt never authorizes interpreting its payload or repeating its action.
func supportedPermissionBridge(bridge string) bool {
	return bridge == "legacy-frozen-v1" || bridge == iam.RuntimeBridge
}

const unsupportedPermissionMessage = "当前程序不支持此任务的权限版本，已暂停执行与交付；原记录和请求编号保留。"
