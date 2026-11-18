package server

import (
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"

	"net/http"
)

func registerIAMRecovery(mux httpx.RouteRegistrar, cfg *config.Config, users *db.UserRepository, sessions *auth.SessionStore, identity middleware.Middleware) {
	mux.Handle("GET /api/v1/iam-recovery", identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, e := users.IAMRecoveryList(r.Context())
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpx.OK(w, out)
	})))
	mux.Handle("POST /api/v1/iam-recovery/requests", identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMRecoveryCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMRecoveryRequest(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})))
	mux.Handle("POST /api/v1/iam-recovery/verifications", identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMRecoveryVerification
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMRecoveryVerify(r.Context(), in, cfg.SecretKey, sessions)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})))
}
