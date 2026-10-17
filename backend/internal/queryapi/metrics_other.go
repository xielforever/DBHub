//go:build !linux

package queryapi

// 非 Linux 平台保留 Go 运行时指标，不把不可用的进程指标伪装成零值。
func processResources() (*float64, *uint64) { return nil, nil }
