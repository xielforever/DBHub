package server

import (
	"net/http"
	"strconv"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

func registerIAMStatus(mux httpx.RouteRegistrar, users *db.UserRepository, identity middleware.Middleware) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var wid int64
		var e error
		if raw := r.PathValue("workspace"); raw != "" {
			wid, e = strconv.ParseInt(raw, 10, 64)
			if e != nil || wid <= 0 {
				httpx.Fail(w, httpx.BadRequest("原工作空间编号无效"))
				return
			}
		}
		result, e := users.IAMOwnRestrictionStatus(r.Context(), wid, r.PathValue("request"))
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpx.OK(w, result)
	}
	mux.Handle("GET /api/v1/iam-command-status", identity(http.HandlerFunc(handler)))
	mux.Handle("GET /api/v1/iam-command-status/{workspace}/{request}", identity(http.HandlerFunc(handler)))
}
