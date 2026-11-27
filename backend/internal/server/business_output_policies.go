package server

import (
	"net/http"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

func registerBusinessOutputPolicies(mux httpx.RouteRegistrar, users *db.UserRepository, requireAuth middleware.Middleware) {
	policy := func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if r.Method != http.MethodPut {
			out, err := users.BusinessOutputPolicy(r.Context(), key)
			writeBusinessMapping(w, out, err)
			return
		}
		var in db.BusinessOutputPolicyInput
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.SaveBusinessOutputPolicy(r.Context(), key, in)
		writeBusinessMapping(w, out, err)
	}
	plan := func(w http.ResponseWriter, r *http.Request) {
		userID, err := positiveWorkspaceUserID(r.PathValue("user"))
		if err != nil {
			writeBusinessMapping(w, nil, err)
			return
		}
		var in db.BusinessOutputPlanInput
		if err = httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.CompileBusinessOutputPlan(r.Context(), userID, r.PathValue("key"), in)
		writeBusinessMapping(w, out, err)
	}
	reveal := func(w http.ResponseWriter, r *http.Request) {
		var in db.BusinessRevealInput
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.RequestBusinessReveal(r.Context(), r.PathValue("key"), in)
		writeBusinessMapping(w, out, err)
	}
	approve := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TTLSeconds int `json:"ttl_seconds"`
		}
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.ApproveBusinessReveal(r.Context(), r.PathValue("request"), in.TTLSeconds)
		writeBusinessMapping(w, out, err)
	}
	revoke := func(w http.ResponseWriter, r *http.Request) {
		if _, err := httpx.DecodeOptionalJSON(w, r, &struct{}{}); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.RevokeBusinessReveal(r.Context(), r.PathValue("request"))
		writeBusinessMapping(w, out, err)
	}
	admin := middleware.RequireRoles("admin")
	mux.Handle("GET /api/v1/business-datasets/{key}/output-policy", middleware.Chain(http.HandlerFunc(policy), requireAuth, admin))
	mux.Handle("PUT /api/v1/business-datasets/{key}/output-policy", middleware.Chain(http.HandlerFunc(policy), requireAuth, admin))
	mux.Handle("POST /api/v1/business-identities/{user}/datasets/{key}/output-plan", middleware.Chain(http.HandlerFunc(plan), requireAuth, admin))
	mux.Handle("POST /api/v1/business-datasets/{key}/reveal-requests", middleware.Chain(http.HandlerFunc(reveal), requireAuth, admin))
	mux.Handle("POST /api/v1/business-reveal-requests/{request}/approve", middleware.Chain(http.HandlerFunc(approve), requireAuth, admin))
	mux.Handle("POST /api/v1/business-reveal-requests/{request}/revoke", middleware.Chain(http.HandlerFunc(revoke), requireAuth, admin))
}
