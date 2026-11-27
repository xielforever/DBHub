package server

import (
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
)

func registerPlatformAccounts(mux httpx.RouteRegistrar, repo *db.UserRepository, identity middleware.Middleware) {
	platform := func(handler http.HandlerFunc) http.Handler {
		return identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if !repo.IsPlatformAdmin(r.Context()) {
				httpx.Fail(w, httpx.Forbidden("仅超级管理员可操作"))
				return
			}
			handler(w, r)
		}))
	}

	mux.Handle("GET /api/v1/platform/accounts", platform(func(w http.ResponseWriter, r *http.Request) {
		filter, err := platformDirectoryQuery(r)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		filter.Status = r.URL.Query().Get("status")
		filter.LoginMethod = r.URL.Query().Get("login_method")
		if filter.Status != "" && filter.Status != "pending" && filter.Status != "active" && filter.Status != "inactive" ||
			filter.LoginMethod != "" && filter.LoginMethod != "local" && filter.LoginMethod != "sso" {
			httpx.Fail(w, httpx.BadRequest("账号筛选条件无效"))
			return
		}
		items, total, err := repo.PlatformAccounts(r.Context(), filter)
		if err != nil {
			platformAccountFailure(w, err)
			return
		}
		httpx.OK(w, map[string]any{"items": items, "total": total})
	}))
	mux.Handle("POST /api/v1/platform/accounts", platform(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Username string `json:"username"`
			Email    string `json:"email"`
			Reason   string `json:"reason"`
		}
		if !decodePlatformAccountInput(w, r, &in) {
			return
		}
		email := strings.TrimSpace(in.Email)
		if email != "" {
			address, err := mail.ParseAddress(email)
			if err != nil || address.Address != email {
				httpx.Fail(w, httpx.BadRequest("邮箱格式无效"))
				return
			}
		}
		receipt, err := repo.CreatePlatformAccount(r.Context(), strings.TrimSpace(in.Username), email, in.Reason)
		if err != nil {
			platformAccountFailure(w, err)
			return
		}
		httpx.JSON(w, http.StatusCreated, httpx.Envelope{Code: httpx.CodeOK, Message: "ok", Data: receipt})
	}))
	mux.Handle("POST /api/v1/platform/accounts/{id}/credential", platform(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Purpose         string `json:"purpose"`
			ExpectedVersion int64  `json:"expected_version"`
			Reason          string `json:"reason"`
		}
		id, valid := platformAccountID(w, r)
		if !valid || !decodePlatformAccountInput(w, r, &in) {
			return
		}
		receipt, err := repo.IssuePlatformAccountCredential(r.Context(), id, in.ExpectedVersion, in.Purpose, in.Reason)
		if err != nil {
			platformAccountFailure(w, err)
			return
		}
		httpx.OK(w, receipt)
	}))
	mux.Handle("PATCH /api/v1/platform/accounts/{id}/status", platform(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Active          *bool  `json:"active"`
			ExpectedVersion int64  `json:"expected_version"`
			Reason          string `json:"reason"`
		}
		id, valid := platformAccountID(w, r)
		if !valid || !decodePlatformAccountInput(w, r, &in) {
			return
		}
		if in.Active == nil {
			httpx.Fail(w, httpx.BadRequest("须明确指定启用或停用"))
			return
		}
		account, err := repo.SetPlatformAccountStatus(r.Context(), id, in.ExpectedVersion, *in.Active, in.Reason)
		if err != nil {
			platformAccountFailure(w, err)
			return
		}
		httpx.OK(w, account)
	}))
	mux.Handle("POST /api/v1/platform/accounts/{id}/revoke-sessions", platform(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ExpectedVersion int64  `json:"expected_version"`
			Reason          string `json:"reason"`
		}
		id, valid := platformAccountID(w, r)
		if !valid || !decodePlatformAccountInput(w, r, &in) {
			return
		}
		account, err := repo.RevokePlatformAccountSessions(r.Context(), id, in.ExpectedVersion, in.Reason)
		if err != nil {
			platformAccountFailure(w, err)
			return
		}
		httpx.OK(w, account)
	}))
	mux.Handle("GET /api/v1/platform/role-templates", platform(func(w http.ResponseWriter, r *http.Request) {
		items, err := repo.PlatformRoleTemplates(r.Context())
		if err != nil {
			platformAccountFailure(w, err)
			return
		}
		httpx.OK(w, map[string]any{"items": items})
	}))
	mux.Handle("GET /api/v1/platform/workspaces", platform(func(w http.ResponseWriter, r *http.Request) {
		filter, err := platformDirectoryQuery(r)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		items, total, err := repo.PlatformWorkspaces(r.Context(), filter)
		if err != nil {
			platformAccountFailure(w, err)
			return
		}
		httpx.OK(w, map[string]any{"items": items, "total": total})
	}))
	mux.HandleFunc("POST /api/v1/auth/account-credential", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var in struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if !decodePlatformAccountInput(w, r, &in) {
			return
		}
		purpose, err := repo.RedeemPlatformAccountCredential(r.Context(), in.Token, in.Password)
		if errors.Is(err, db.ErrManagedInput) {
			httpx.Fail(w, httpx.BadRequest("密码至少 15 个字符且最多 4096 字节，不能全部为空白"))
			return
		}
		if err != nil {
			platformAccountFailure(w, err)
			return
		}
		httpx.OK(w, map[string]string{"purpose": purpose})
	})
}

func platformDirectoryQuery(r *http.Request) (db.PlatformDirectoryFilter, error) {
	parameters := r.URL.Query()
	f := db.PlatformDirectoryFilter{Keyword: strings.TrimSpace(parameters.Get("keyword")), Limit: 20}
	parse := func(key string, fallback, max int) (int, bool) {
		raw := parameters.Get(key)
		if raw == "" {
			return fallback, true
		}
		n, err := strconv.Atoi(raw)
		return n, err == nil && n >= 1 && n <= max
	}
	page, ok := parse("page", 1, 1_000_000)
	size, sizeOK := parse("page_size", f.Limit, 100)
	if !ok || !sizeOK || !utf8.ValidString(f.Keyword) || utf8.RuneCountInString(f.Keyword) > 100 {
		return f, httpx.BadRequest("搜索或分页参数无效")
	}
	f.Limit = size
	f.Offset = (page - 1) * size
	return f, nil
}

func platformAccountID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(w, httpx.NotFound("账号不存在"))
		return 0, false
	}
	return id, true
}

func decodePlatformAccountInput(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := httpx.DecodeJSON(w, r, out); err != nil {
		// Do not echo unknown field names or other request fragments on credential paths.
		httpx.Fail(w, httpx.BadRequest("请求参数格式错误"))
		return false
	}
	return true
}

func platformAccountFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrManagedDenied):
		httpx.Fail(w, httpx.Forbidden("当前账号无权执行此操作，超级管理员须通过个人安全设置管理本人凭据"))
	case errors.Is(err, db.ErrPlatformCredentialInvalid):
		httpx.Fail(w, httpx.BadRequest(db.ErrPlatformCredentialInvalid.Error()))
	case errors.Is(err, db.ErrManagedInput):
		httpx.Fail(w, httpx.BadRequest("账号、版本或操作原因无效"))
	case errors.Is(err, db.ErrManagedConflict):
		httpx.Fail(w, httpx.Conflict("账号已存在或当前状态、版本不支持此操作，请刷新后核对"))
	case errors.Is(err, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("账号不存在"))
	case errors.Is(err, db.ErrPlatformAccountAudit):
		httpx.JSON(w, http.StatusServiceUnavailable, httpx.Envelope{Code: 50300, Message: "审计服务暂不可用，账号未修改"})
	default:
		httpx.Fail(w, httpx.Internal("平台账号操作失败", err))
	}
}
