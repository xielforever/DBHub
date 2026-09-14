// Package logx 基于 log/slog 的结构化日志初始化。
package logx

import (
	"log/slog"
	"os"
	"strings"
)

// New 根据环境与级别创建 slog 实例：开发环境文本格式，生产环境 JSON。
func New(env, levelText string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(levelText) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
