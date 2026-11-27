package server

import (
	"net/http"
	"strconv"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

func registerSimpleIAM(mux httpx.RouteRegistrar, users *db.UserRepository, identity middleware.Middleware) {
	mux.Handle("GET /api/v1/role-assignments/workspaces/{workspace}/members", identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		space, err := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		if err != nil || space <= 0 {
			httpx.Fail(w, httpx.NotFound("工作空间不存在"))
			return
		}
		out, err := users.IAMDirectMembers(r.Context(), space)
		if err != nil {
			iamHTTPError(w, err)
			return
		}
		httpx.OK(w, out)
	})))
	mux.Handle("PUT /api/v1/role-assignments/workspaces/{workspace}/members/{member}", identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		space, spaceErr := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		member, memberErr := strconv.ParseInt(r.PathValue("member"), 10, 64)
		if spaceErr != nil || memberErr != nil || space <= 0 || member <= 0 {
			httpx.Fail(w, httpx.NotFound("工作空间或成员不存在"))
			return
		}
		var in db.IAMDirectRoleCommand
		if httpx.DecodeJSON(w, r, &in) != nil {
			httpx.Fail(w, httpx.BadRequest("成员角色参数无效"))
			return
		}
		out, err := users.IAMSetDirectRoles(r.Context(), space, member, in)
		if err != nil {
			iamHTTPError(w, err)
			return
		}
		httpx.OK(w, out)
	})))
	mux.Handle("GET /api/v1/role-assignments/workspaces/{workspace}/member-candidates", identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		space, err := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		if err != nil || space <= 0 {
			httpx.Fail(w, httpx.NotFound("工作空间不存在"))
			return
		}
		out, err := users.IAMMemberCandidates(r.Context(), space, r.URL.Query().Get("keyword"))
		if err != nil {
			iamHTTPError(w, err)
			return
		}
		httpx.OK(w, out)
	})))
	mux.Handle("POST /api/v1/role-assignments/workspaces/{workspace}/members", identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		space, err := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		if err != nil || space <= 0 {
			httpx.Fail(w, httpx.NotFound("工作空间不存在"))
			return
		}
		var in db.IAMJoinMemberCommand
		if httpx.DecodeJSON(w, r, &in) != nil {
			httpx.Fail(w, httpx.BadRequest("成员加入参数无效"))
			return
		}
		out, err := users.IAMJoinMember(r.Context(), space, in)
		if err != nil {
			iamHTTPError(w, err)
			return
		}
		httpx.OK(w, out)
	})))
	mux.Handle("PATCH /api/v1/role-assignments/workspaces/{workspace}/members/{member}/membership", identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		space, spaceErr := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		member, memberErr := strconv.ParseInt(r.PathValue("member"), 10, 64)
		if spaceErr != nil || memberErr != nil || space <= 0 || member <= 0 {
			httpx.Fail(w, httpx.NotFound("工作空间或成员不存在"))
			return
		}
		var in db.IAMMembershipCommand
		if httpx.DecodeJSON(w, r, &in) != nil {
			httpx.Fail(w, httpx.BadRequest("成员资格参数无效"))
			return
		}
		out, err := users.IAMChangeMembership(r.Context(), space, member, in)
		if err != nil {
			iamHTTPError(w, err)
			return
		}
		httpx.OK(w, out)
	})))
}
