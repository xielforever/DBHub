package server

import (
	"net/http"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/iam"
	"github.com/user/dbhub/internal/middleware"
)

// The foundation endpoint explains only the caller's legacy functional
// envelope. It cannot enable roles or stand in for object/data authorization.
func registerIAMFoundation(mux httpx.RouteRegistrar, scoped middleware.Middleware) {
	mux.Handle("GET /api/v1/iam/foundation", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok || claims == nil || claims.IAM == nil {
			httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "当前权限快照不可用"})
			return
		}
		catalog := iam.ReadCatalog()
		decisions := make([]iam.Decision, 0, len(catalog.Capabilities))
		for _, cap := range catalog.Capabilities {
			if cap.Scope == "workspace" {
				decisions = append(decisions, iam.ExplainLegacy(*claims.IAM, cap.ID, time.Now()))
			}
		}
		httpx.OK(w, map[string]any{
			"stage": "FOUNDATION_ONLY", "activation_ready": false,
			"catalog_version": catalog.Version, "runtime_bridge": iam.BridgeVersion,
			"snapshot": claims.IAM, "functional_envelope": decisions,
			"notice": "仅说明现有功能许可上限；对象、数据、字段、审批与任务仍按原路径校验。新角色切换尚未启用。",
		})
	}), scoped))
}
