package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

func iamRuntimeHeartbeat(deps *Deps, next http.Handler) http.Handler {
	if deps == nil || deps.UserRepo == nil || deps.IAMRouteFingerprint == "" {
		return next
	}
	var last atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Unix()
		previous := last.Load()
		if now-previous >= 30 && last.CompareAndSwap(previous, now) {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			_ = deps.UserRepo.IAMHeartbeat(ctx, "api", deps.IAMRouteFingerprint)
			_ = deps.UserRepo.IAMHeartbeat(ctx, "database", deps.IAMRouteFingerprint)
			cancel()
		}
		next.ServeHTTP(w, r)
	})
}
