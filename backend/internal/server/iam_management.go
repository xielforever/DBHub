package server

import (
	"encoding/json"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"

	"net/http"
)

func registerIAMManagement(mux httpx.RouteRegistrar, users *db.UserRepository, scoped middleware.Middleware, deps *Deps) {
	registerContentCustody(mux, users, scoped)
	wrap := func(path string, h http.HandlerFunc) { mux.Handle(path, scoped(h)) }
	wrap("GET /api/v1/iam/effective", func(w http.ResponseWriter, r *http.Request) {
		out, e := users.IAMEffective(r.Context())
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpx.OK(w, out)
	})
	wrap("GET /api/v1/iam/catalog", func(w http.ResponseWriter, r *http.Request) {
		out, e := users.IAMEffective(r.Context())
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	for _, section := range []string{"roles", "assignments", "delegations", "restrictions", "proposals", "own-proposals", "groups", "members", "resources", "policy", "custody", "migration", "bundles", "assignment-targets", "assignable-roles"} {
		wrap("GET /api/v1/iam/"+section, func(w http.ResponseWriter, r *http.Request) {
			out, e := users.IAMRead(r.Context(), section)
			if e != nil {
				iamHTTPError(w, e)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			httpx.OK(w, out)
		})
	}
	wrap("GET /api/v1/iam/menus", func(w http.ResponseWriter, r *http.Request) {
		out, e := users.IAMEffective(r.Context())
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	wrap("PUT /api/v1/iam/menus", func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMMenuCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMSaveMenus(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	wrap("PUT /api/v1/iam/home", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			MenuID string `json:"menu_id"`
		}
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		if e := users.IAMHome(r.Context(), in.MenuID); e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, nil)
	})
	wrap("POST /api/v1/iam/proposals/preview", func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMProposalCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMPreview(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	wrap("POST /api/v1/iam/proposals", func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMProposalCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMPropose(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	wrap("POST /api/v1/iam/proposals/review", func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMReviewCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMReview(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	wrap("POST /api/v1/iam/restrictions", func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMRestrictionCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMRestrict(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		var receipt struct {
			Fence string `json:"execution_fence"`
		}
		_ = json.Unmarshal(out, &receipt)
		if receipt.Fence == "pending" {
			httpx.JSON(w, http.StatusAccepted, httpx.Envelope{Code: 0, Message: "访问限制已保存，执行边界待确认", Data: out})
			return
		}
		httpx.OK(w, out)
	})
	wrap("GET /api/v1/iam/routes", func(w http.ResponseWriter, r *http.Request) { httpx.OK(w, map[string]any{"items": deps.IAMRoutes}) })
	wrap("GET /api/v1/iam/migration/bridge", func(w http.ResponseWriter, r *http.Request) {
		out, e := users.IAMBridgeStatus(r.Context())
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	wrap("POST /api/v1/iam/migration/shadow", func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMShadowCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMShadow(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	wrap("POST /api/v1/iam/bundles", func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMBundleCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMPrepareBundle(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
	wrap("POST /api/v1/iam/bundles/rollback", func(w http.ResponseWriter, r *http.Request) {
		var in db.IAMRollbackCommand
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		out, e := users.IAMRollback(r.Context(), in)
		if e != nil {
			iamHTTPError(w, e)
			return
		}
		httpx.OK(w, out)
	})
}
