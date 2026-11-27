package server

import (
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
	"net/http"
)

// Delegation augments one workspace's responsibilities. It never changes the
// actor's base role or personal database identity into an administrator.
func workspaceCapability(writers bool, capabilities ...string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, _ := auth.ClaimsFromContext(r.Context())
			if c != nil && c.IAM != nil && c.IAM.Target && c.CurrentAction != "" && auth.HasAction(c, c.CurrentAction) {
				next.ServeHTTP(w, r)
				return
			}
			allowed := c != nil && writers && (c.Role == "admin" || c.Role == "developer")
			for _, capability := range capabilities {
				allowed = allowed || auth.HasCapability(c, capability)
			}
			if !allowed {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "insufficient_capability" })
				httpx.Fail(w, httpx.Forbidden("当前工作空间职责不允许此操作"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
