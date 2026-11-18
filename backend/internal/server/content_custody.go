package server

import (
	"net/http"
	"strconv"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

func registerContentCustody(mux httpx.RouteRegistrar, users *db.UserRepository, scoped middleware.Middleware) {
	read := func(nominations bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			out, err := users.IAMContentCustody(r.Context(), nominations)
			if err != nil {
				iamHTTPError(w, err)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			httpx.OK(w, out)
		}
	}
	mux.Handle("GET /api/v1/iam/custody-items", scoped(read(false)))
	mux.Handle("GET /api/v1/iam/custody-nominations", scoped(read(true)))
	mux.Handle("POST /api/v1/iam/custody-rejoins", scoped(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in db.ContentRejoinCommand
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			httpx.Fail(w, err)
			return
		}
		out, err := users.IAMContentRejoin(r.Context(), in)
		if err != nil {
			iamHTTPError(w, err)
			return
		}
		httpx.OK(w, out)
	})))
	mux.Handle("POST /api/v1/iam/custody-items/{item}/handover", scoped(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("item"), 10, 64)
		if err != nil || id <= 0 {
			httpx.Fail(w, httpx.NotFound("交接记录不存在"))
			return
		}
		var in db.ContentHandoverCommand
		if err = httpx.DecodeJSON(w, r, &in); err != nil {
			httpx.Fail(w, err)
			return
		}
		out, err := users.IAMContentHandover(r.Context(), id, in)
		if err != nil {
			iamHTTPError(w, err)
			return
		}
		httpx.OK(w, out)
	})))
}
