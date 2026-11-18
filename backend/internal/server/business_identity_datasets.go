package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

func registerBusinessMappings(mux httpx.RouteRegistrar, users *db.UserRepository, requireAuth middleware.Middleware) {
	dataset := func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if r.Method != http.MethodPut {
			out, err := users.BusinessDataset(r.Context(), key)
			writeBusinessMapping(w, out, err)
			return
		}
		var in db.BusinessDatasetInput
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.SaveBusinessDataset(r.Context(), key, in)
		writeBusinessMapping(w, out, err)
	}
	identity := func(w http.ResponseWriter, r *http.Request) {
		userID, err := positiveWorkspaceUserID(r.PathValue("user"))
		if err != nil {
			writeBusinessMapping(w, nil, err)
			return
		}
		if r.Method != http.MethodPut {
			out, err := users.BusinessIdentity(r.Context(), userID)
			writeBusinessMapping(w, out, err)
			return
		}
		var in db.BusinessIdentityInput
		if err = httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.SaveBusinessIdentity(r.Context(), userID, in)
		writeBusinessMapping(w, out, err)
	}
	organizationScope := func(w http.ResponseWriter, r *http.Request) {
		userID, err := positiveWorkspaceUserID(r.PathValue("user"))
		if err != nil {
			writeBusinessMapping(w, nil, err)
			return
		}
		key := r.PathValue("key")
		if r.Method != http.MethodPut {
			out, err := users.OrganizationScope(r.Context(), userID, key)
			writeBusinessMapping(w, out, err)
			return
		}
		var in db.OrganizationScopeInput
		if err = httpx.DecodeJSON(w, r, &in); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		out, err := users.SaveOrganizationScope(r.Context(), userID, key, in)
		writeBusinessMapping(w, out, err)
	}
	admin := middleware.RequireRoles("admin")
	mux.Handle("GET /api/v1/business-datasets/{key}", middleware.Chain(http.HandlerFunc(dataset), requireAuth, admin))
	mux.Handle("PUT /api/v1/business-datasets/{key}", middleware.Chain(http.HandlerFunc(dataset), requireAuth, admin))
	mux.Handle("GET /api/v1/business-identities/{user}", middleware.Chain(http.HandlerFunc(identity), requireAuth, admin))
	mux.Handle("PUT /api/v1/business-identities/{user}", middleware.Chain(http.HandlerFunc(identity), requireAuth, admin))
	mux.Handle("GET /api/v1/business-identities/{user}/datasets/{key}/organization-scope", middleware.Chain(http.HandlerFunc(organizationScope), requireAuth, admin))
	mux.Handle("PUT /api/v1/business-identities/{user}/datasets/{key}/organization-scope", middleware.Chain(http.HandlerFunc(organizationScope), requireAuth, admin))
}

func positiveWorkspaceUserID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, db.ErrBusinessMappingInput
	}
	return id, nil
}

func writeBusinessMapping(w http.ResponseWriter, out any, err error) {
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		httpx.OK(w, out)
	case errors.Is(err, db.ErrBusinessMappingInput):
		httpx.Fail(w, httpx.BadRequest(err.Error()))
	case errors.Is(err, db.ErrBusinessMappingDenied), errors.Is(err, db.ErrBusinessReadDenied):
		httpx.Fail(w, httpx.Forbidden(err.Error()))
	case errors.Is(err, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("当前空间没有对应的成员或登记记录"))
	case errors.Is(err, db.ErrBusinessMappingConflict):
		httpx.Fail(w, httpx.Conflict(err.Error()))
	default:
		// Constraint details can contain the submitted business key.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			err = fmt.Errorf("business mapping SQLSTATE %s", pgErr.Code)
		}
		httpx.Fail(w, httpx.Internal("业务映射操作失败", err))
	}
}
