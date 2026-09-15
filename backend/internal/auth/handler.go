package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/httpx"
)

// Handler 认证模块 HTTP 处理器。
type Handler struct {
	users      UserStore
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	log        *slog.Logger
}

// NewHandler 创建认证处理器。
func NewHandler(cfg *config.Config, users UserStore, log *slog.Logger) *Handler {
	return &Handler{
		users:      users,
		secret:     cfg.SecretKey,
		accessTTL:  cfg.AccessTTL,
		refreshTTL: cfg.RefreshTTL,
		log:        log,
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// UserResponse 返回给前端的用户信息（绝不包含密码相关字段）。
type UserResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type tokenResponse struct {
	TokenType    string       `json:"token_type"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresAt    time.Time    `json:"expires_at"`
	User         UserResponse `json:"user"`
}

// Login POST /api/v1/auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		httpx.Fail(w, httpx.BadRequest("用户名和密码不能为空"))
		return
	}

	user, err := h.users.FindByUsername(r.Context(), req.Username)
	if err != nil {
		// 用户不存在与密码错误使用相同提示，防止账号枚举
		h.log.Warn("登录失败：用户不存在", "username", req.Username)
		httpx.Fail(w, httpx.Unauthorized("用户名或密码错误"))
		return
	}

	ok, err := VerifyPassword(user.PasswordHash, req.Password)
	if err != nil || !ok {
		h.log.Warn("登录失败：密码校验未通过", "username", req.Username)
		httpx.Fail(w, httpx.Unauthorized("用户名或密码错误"))
		return
	}

	pair, err := IssuePair(h.secret, user, h.accessTTL, h.refreshTTL)
	if err != nil {
		httpx.Fail(w, httpx.Internal("签发令牌失败", err))
		return
	}
	if err := h.users.TouchLogin(r.Context(), user.ID); err != nil {
		h.log.Warn("更新登录时间失败", "user_id", user.ID, "error", err)
	}

	h.log.Info("用户登录成功", "user_id", user.ID, "username", user.Username)
	// 供审计中间件关联操作者（WriteHeader 时被提取并从响应头移除）
	w.Header().Set("X-Audit-User-Id", strconv.FormatInt(user.ID, 10))
	httpx.OK(w, tokenResponse{
		TokenType:    "Bearer",
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresAt:    pair.ExpiresAt.UTC(),
		User:         UserResponse{ID: user.ID, Username: user.Username, Role: user.Role},
	})
}

// Refresh POST /api/v1/auth/refresh
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		httpx.Fail(w, httpx.BadRequest("refresh_token 不能为空"))
		return
	}

	claims, err := ParseToken(h.secret, req.RefreshToken, TokenTypeRefresh)
	if err != nil {
		httpx.Fail(w, httpx.New(httpx.CodeUnauthorized, "刷新令牌无效或已过期", err))
		return
	}

	// 重新加载用户，确保令牌签发后账号未被禁用
	user, err := h.users.FindByUsername(r.Context(), claims.Username)
	if err != nil {
		httpx.Fail(w, httpx.Unauthorized("用户不可用"))
		return
	}

	pair, err := IssuePair(h.secret, user, h.accessTTL, h.refreshTTL)
	if err != nil {
		httpx.Fail(w, httpx.Internal("签发令牌失败", err))
		return
	}
	httpx.OK(w, tokenResponse{
		TokenType:    "Bearer",
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresAt:    pair.ExpiresAt.UTC(),
		User:         UserResponse{ID: user.ID, Username: user.Username, Role: user.Role},
	})
}

// Me GET /api/v1/auth/me （需 Access Token）
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httpx.Fail(w, httpx.Unauthorized("未认证"))
		return
	}

	// 以数据库/存储中的最新角色与状态为准
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	user, err := h.users.FindByUsername(ctx, claims.Username)
	if err != nil {
		httpx.Fail(w, httpx.Unauthorized("用户不可用"))
		return
	}
	httpx.OK(w, UserResponse{ID: user.ID, Username: user.Username, Role: user.Role})
}
