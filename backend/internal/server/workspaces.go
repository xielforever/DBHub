package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

var workspaceCommandKey = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type requestedWorkspace struct{}

func workspaceRouting(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/workspaces/"
		if strings.HasPrefix(r.URL.Path, prefix) {
			parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, prefix), "/", 2)
			if id, e := strconv.ParseInt(parts[0], 10, 64); e != nil || id <= 0 {
				httpx.Fail(w, httpx.NotFound("工作空间不存在"))
				return
			}
			if len(parts) == 2 && parts[1] != "members" && parts[1] != "policy" && !strings.HasPrefix(parts[1], "members/") {
				id, e := strconv.ParseInt(parts[0], 10, 64)
				if e != nil || id <= 0 {
					httpx.Fail(w, httpx.NotFound("工作空间不存在"))
					return
				}
				clone := r.Clone(context.WithValue(r.Context(), requestedWorkspace{}, id))
				u := *r.URL
				u.Path = "/api/v1/" + parts[1]
				clone.URL = &u
				r = clone
			}
		}
		next.ServeHTTP(w, r)
	})
}
func workspaceAuth(users *db.UserRepository) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, _ := r.Context().Value(requestedWorkspace{}).(int64)
			if value := r.Header.Get("X-Workspace-ID"); value != "" && id == 0 {
				var e error
				id, e = strconv.ParseInt(value, 10, 64)
				if e != nil || id <= 0 {
					httpx.Fail(w, httpx.NotFound("工作空间不存在"))
					return
				}
			}
			ctx, e := users.BindWorkspace(r.Context(), id)
			if e != nil {
				if errors.Is(e, db.ErrWorkspaceRequired) {
					httpx.JSON(w, 428, httpx.Envelope{Code: 42800, Message: e.Error()})
				} else {
					httpx.Fail(w, httpx.NotFound("工作空间不存在或成员资格已失效"))
				}
				return
			}
			auditctx.Update(ctx, func(d *auditctx.Data) {
				d.WorkspaceID = db.WorkspaceID(ctx)
				if c, ok := auth.ClaimsFromContext(ctx); ok && c != nil {
					d.ActorRole = c.Role
				}
			})
			if e := auditctx.Checkpoint(ctx); e != nil {
				httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "审计存储暂不可用，请稍后重试"})
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
func registerWorkspaces(mux *http.ServeMux, users *db.UserRepository, identity middleware.Middleware) {
	mux.Handle("GET /api/v1/platform/status", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.OK(w, map[string]bool{"is_platform_admin": users.IsPlatformAdmin(r.Context())})
	}), identity))
	mux.Handle("GET /api/v1/workspaces", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out, e := users.Workspaces(r.Context())
		if e != nil {
			httpx.JSON(w, 500, httpx.Envelope{Code: 50000, Message: "读取工作空间失败"})
			return
		}
		httpx.OK(w, out)
	}), identity))
	mux.Handle("POST /api/v1/workspaces", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name      string `json:"name"`
			RequestID string `json:"request_id"`
		}
		if httpx.DecodeJSON(w, r, &in) != nil {
			httpx.Fail(w, httpx.BadRequest("参数无效"))
			return
		}
		if !workspaceCommandKey.MatchString(in.RequestID) || in.RequestID == "00000000-0000-0000-0000-000000000000" {
			httpx.Fail(w, httpx.BadRequest("request_id 必须为非空UUID"))
			return
		}
		out, e := users.CreateWorkspace(r.Context(), in.Name, in.RequestID)
		if e != nil {
			switch {
			case errors.Is(e, db.ErrManagedConflict):
				httpx.Fail(w, httpx.Conflict(e.Error()))
			case errors.Is(e, db.ErrManagedInput):
				httpx.Fail(w, httpx.BadRequest(e.Error()))
			case errors.Is(e, db.ErrManagedDenied):
				httpx.Fail(w, httpx.Forbidden("仅平台管理员可创建工作空间"))
			default:
				httpx.Fail(w, httpx.Internal("创建工作空间失败", e))
			}
			return
		}
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.WorkspaceID = out.ID; d.ActorRole = "admin" })
		httpx.OK(w, out)
	}), identity))
	mux.Handle("PATCH /api/v1/workspaces/{workspace}/policy", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wid, _ := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		ctx, e := users.BindWorkspace(r.Context(), wid)
		var in struct {
			Allow *bool `json:"allow_unknown_freshness"`
		}
		if e == nil {
			e = httpx.DecodeJSON(w, r, &in)
		}
		if e == nil && in.Allow == nil {
			e = db.ErrManagedInput
		}
		if e == nil {
			e = users.SetWorkspacePolicy(ctx, *in.Allow)
		}
		if e != nil {
			httpx.Fail(w, httpx.Forbidden("仅有效管理员可修改完整的工作空间策略"))
			return
		}
		auditctx.Update(ctx, func(d *auditctx.Data) { d.WorkspaceID = wid })
		httpx.OK(w, nil)
	}), identity))
	mux.Handle("GET /api/v1/workspaces/{workspace}/members", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wid, _ := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		ctx, e := users.BindWorkspace(r.Context(), wid)
		if e != nil || !db.ActorAdmin(ctx) {
			httpx.Fail(w, httpx.Forbidden("仅工作空间管理员可查看成员管理"))
			return
		}
		out, total, e := users.ListUsers(ctx, "", 1000, 0)
		if e != nil {
			httpx.Fail(w, httpx.Internal("读取成员失败", e))
			return
		}
		httpx.OK(w, map[string]any{"items": out, "total": total})
	}), identity))
	mux.Handle("POST /api/v1/workspaces/{workspace}/members", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		ctx, e := users.BindWorkspace(r.Context(), id)
		var in struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		}
		if e == nil {
			e = httpx.DecodeJSON(w, r, &in)
		}
		if e == nil {
			auditctx.Update(ctx, func(d *auditctx.Data) { d.WorkspaceID = id })
			e = users.AddMember(ctx, in.Username, in.Role)
		}
		if e != nil {
			httpx.JSON(w, 409, httpx.Envelope{Code: 40900, Message: e.Error()})
			return
		}
		httpx.OK(w, nil)
	}), identity))
	mux.Handle("PATCH /api/v1/workspaces/{workspace}/members/{member}", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wid, _ := strconv.ParseInt(r.PathValue("workspace"), 10, 64)
		id, _ := strconv.ParseInt(r.PathValue("member"), 10, 64)
		ctx, e := users.BindWorkspace(r.Context(), wid)
		var in struct {
			Role    string `json:"role"`
			Active  *bool  `json:"active"`
			Version int64  `json:"expected_version"`
		}
		if e == nil {
			e = httpx.DecodeJSON(w, r, &in)
		}
		if e == nil && in.Version <= 0 {
			e = db.ErrManagedInput
		}
		if e == nil {
			auditctx.Update(ctx, func(d *auditctx.Data) { d.WorkspaceID = wid })
			e = users.UpdateMembership(ctx, id, in.Role, in.Active, in.Version)
		}
		if e != nil {
			httpx.JSON(w, 409, httpx.Envelope{Code: 40900, Message: e.Error()})
			return
		}
		httpx.OK(w, nil)
	}), identity))
}
