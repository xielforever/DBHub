package server

import (
	"errors"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
	"net/http"
)

func registerExternalIdentities(mux httpx.RouteRegistrar, cfg *config.Config, repo *db.UserRepository, identity middleware.Middleware) {
	allowed := func(w http.ResponseWriter, r *http.Request) bool {
		if !repo.IsPlatformAdmin(r.Context()) {
			httpx.Fail(w, httpx.Forbidden("仅平台管理员可管理全局单点登录身份关联"))
			return false
		}
		if cfg.OIDC.Issuer == "" || config.ValidateOIDC(cfg.OIDC, !cfg.IsProduction()) != nil {
			httpx.Fail(w, httpx.BadRequest("此环境尚未配置企业单点登录"))
			return false
		}
		return true
	}
	mux.Handle("GET /api/v1/platform/sso-bindings", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r) {
			return
		}
		items, err := repo.ExternalIdentities(r.Context(), cfg.OIDC.Issuer)
		if err != nil {
			httpx.Fail(w, httpx.Internal("读取身份关联失败", err))
			return
		}
		httpx.OK(w, map[string]any{"issuer": cfg.OIDC.Issuer, "items": items})
	}), identity))
	mux.Handle("POST /api/v1/platform/sso-bindings", middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r) {
			return
		}
		var input struct {
			Username        string `json:"username"`
			Subject         string `json:"subject"`
			ExpectedVersion int64  `json:"expected_version"`
			Enabled         *bool  `json:"enabled"`
		}
		if err := httpx.DecodeJSON(w, r, &input); err != nil {
			httpx.Fail(w, err)
			return
		}
		if input.Enabled == nil {
			httpx.Fail(w, httpx.BadRequest("必须明确指定关联状态"))
			return
		}
		if err := auditctx.Checkpoint(r.Context()); err != nil {
			httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "审计服务暂不可用，身份关联未修改"})
			return
		}
		err := repo.BindExternalIdentity(r.Context(), cfg.OIDC.Issuer, input.Subject, input.Username, input.ExpectedVersion, *input.Enabled)
		switch {
		case errors.Is(err, db.ErrManagedDenied):
			httpx.Fail(w, httpx.Forbidden("当前身份无权管理单点登录关联"))
		case errors.Is(err, db.ErrManagedInput):
			httpx.Fail(w, httpx.BadRequest("身份关联参数无效，请选择有效现有用户"))
		case errors.Is(err, db.ErrManagedConflict):
			httpx.Fail(w, httpx.Conflict("身份已关联或版本已变化，请刷新后操作"))
		case err != nil:
			httpx.Fail(w, httpx.Internal("保存身份关联失败", err))
		default:
			httpx.OK(w, map[string]bool{"saved": true})
		}
	}), identity))
}
