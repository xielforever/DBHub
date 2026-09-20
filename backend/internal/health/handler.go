// Package health 提供存活/就绪探针接口。
package health

import (
	"net/http"
	"time"

	"github.com/user/dbhub/internal/httpx"
)

// Version 当前后端版本号，随发布流程更新。
const Version = "0.1.0-dev"

// Handler 健康检查处理器。
type Handler struct {
	env       string
	startedAt time.Time
}

// NewHandler 创建健康检查处理器。
func NewHandler(env string) *Handler {
	return &Handler{env: env, startedAt: time.Now()}
}

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
	Env     string `json:"env"`
	UptimeS int64  `json:"uptime_seconds"`
	Time    string `json:"time"`
}

// Health GET /api/health
func (h *Handler) Health(w http.ResponseWriter, _ *http.Request) {
	httpx.OK(w, healthResponse{
		Status:  "ok",
		Service: "dbhub-backend",
		Version: Version,
		Env:     h.env,
		UptimeS: int64(time.Since(h.startedAt).Seconds()),
		Time:    time.Now().UTC().Format(time.RFC3339),
	})
}
