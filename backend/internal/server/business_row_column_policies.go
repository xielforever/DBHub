package server

import (
	"net/http"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

func registerBusinessRowColumnPolicies(mux httpx.RouteRegistrar, users *db.UserRepository, requireAuth middleware.Middleware) {
	policy := func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if r.Method != http.MethodPut {
			out, err := users.BusinessRowColumnPolicy(r.Context(), key)
			writeBusinessMapping(w, out, err)
			return
		}
		var in db.BusinessRowColumnPolicyInput
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.SaveBusinessRowColumnPolicy(r.Context(), key, in)
		writeBusinessMapping(w, out, err)
	}
	plan := func(w http.ResponseWriter, r *http.Request) {
		userID, err := positiveWorkspaceUserID(r.PathValue("user"))
		if err != nil {
			writeBusinessMapping(w, nil, err)
			return
		}
		var in db.BusinessReadPlanInput
		if err = httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.CompileBusinessReadPlan(r.Context(), userID, r.PathValue("key"), in)
		writeBusinessMapping(w, out, err)
	}
	admin := middleware.RequireRoles("admin")
	mux.Handle("GET /api/v1/business-datasets/{key}/row-column-policy", middleware.Chain(http.HandlerFunc(policy), requireAuth, admin))
	mux.Handle("PUT /api/v1/business-datasets/{key}/row-column-policy", middleware.Chain(http.HandlerFunc(policy), requireAuth, admin))
	mux.Handle("POST /api/v1/business-identities/{user}/datasets/{key}/row-column-plan", middleware.Chain(http.HandlerFunc(plan), requireAuth, admin))
}
