package server

import (
	"context"
	"net/http"
	"time"

	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/health"
	"github.com/user/dbhub/internal/httpx"
)

// Readiness covers dependencies required by all authenticated operations.
// Liveness stays independent so an external outage does not restart the process.
func readinessHandler(cfg *config.Config, h *health.Handler, deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		unavailable := func() {
			w.Header().Set("Retry-After", "2")
			httpx.JSON(w, http.StatusServiceUnavailable, httpx.Envelope{Code: 50300, Message: "服务依赖尚未就绪"})
		}
		if cfg.IsProduction() && (deps == nil || deps.UserRepo == nil || deps.Redis == nil) {
			unavailable()
			return
		}
		if deps != nil {
			if deps.Conns != nil && (deps.AuditJournal == nil || !deps.AuditJournal.Status().Ready) {
				unavailable()
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if deps.UserRepo != nil && deps.UserRepo.Ready(ctx) != nil {
				unavailable()
				return
			}
			// Bound command I/O even for clients whose default options ignore the
			// context deadline during a socket read. The clone shares its pool.
			if deps.Redis != nil && deps.Redis.WithTimeout(500*time.Millisecond).Ping(ctx).Err() != nil {
				unavailable()
				return
			}
		}
		h.Health(w, r)
	}
}
