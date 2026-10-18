package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/httpx"
)

type Handler struct {
	users                 UserStore
	secret                []byte
	accessTTL, refreshTTL time.Duration
	log                   *slog.Logger
	sessions              *SessionStore
	loginLimiter          *loginLimiter
}

func NewHandler(cfg *config.Config, users UserStore, log *slog.Logger, sessions *SessionStore) *Handler {
	if sessions == nil {
		sessions = NewSessionStore(nil)
	}
	return &Handler{users: users, secret: cfg.SecretKey, accessTTL: cfg.AccessTTL, refreshTTL: cfg.RefreshTTL, log: log, sessions: sessions, loginLimiter: newLoginLimiter(cfg, sessions.client)}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}
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

func tokenData(pair *TokenPair, user *User) tokenResponse {
	return tokenResponse{TokenType: "Bearer", AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, ExpiresAt: pair.ExpiresAt.UTC(), User: UserResponse{ID: user.ID, Username: user.Username, Role: user.Role}}
}
func sessionFailure(w http.ResponseWriter, e error) {
	if errors.Is(e, ErrSession) {
		httpx.Fail(w, httpx.Unauthorized("会话已失效或刷新令牌已使用，请重新登录"))
	} else {
		httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "会话服务暂不可用"})
	}
}
func userFailure(w http.ResponseWriter, err error, message string) {
	if UserStoreUnavailable(err) {
		w.Header().Set("Retry-After", "2")
		httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "身份服务暂不可用，请稍后重试"})
		return
	}
	httpx.Fail(w, httpx.Unauthorized(message))
}
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || in.Password == "" {
		httpx.Fail(w, httpx.BadRequest("用户名和密码不能为空"))
		return
	}
	if len(in.Username) > 256 || len(in.Password) > 4096 {
		httpx.Fail(w, httpx.BadRequest("用户名或密码过长"))
		return
	}
	wait, err := h.loginLimiter.allow(r.Context(), in.Username, r)
	if err != nil {
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "login_protection_unavailable" })
		w.Header().Set("Retry-After", "2")
		httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "登录服务暂不可用，请稍后重试"})
		return
	}
	if wait > 0 {
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "login_throttled" })
		w.Header().Set("Retry-After", strconv.FormatInt(int64((wait+time.Second-1)/time.Second), 10))
		httpx.JSON(w, 429, httpx.Envelope{Code: 42900, Message: "登录尝试过于频繁，请稍后重试"})
		return
	}
	user, e := h.users.FindByUsername(r.Context(), in.Username)
	if e != nil {
		userFailure(w, e, "用户名或密码错误")
		return
	}
	valid, e := VerifyPassword(user.PasswordHash, in.Password)
	if e != nil || !valid {
		httpx.Fail(w, httpx.Unauthorized("用户名或密码错误"))
		return
	}
	pair, e := IssuePair(h.secret, user, h.accessTTL, h.refreshTTL)
	if e != nil {
		httpx.Fail(w, httpx.Internal("签发令牌失败", e))
		return
	}
	if e = h.sessions.Create(r.Context(), pair.SessionID, pair.RefreshID, user, h.refreshTTL); e != nil {
		sessionFailure(w, e)
		return
	}
	if e = h.users.TouchLogin(r.Context(), user.ID); e != nil {
		h.log.Warn("更新登录时间失败", "user_id", user.ID)
	}
	w.Header().Set("X-Audit-User-Id", strconv.FormatInt(user.ID, 10))
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ActorID = user.ID; d.ActorUsername = user.Username; d.ActorRole = user.Role })
	httpx.OK(w, tokenData(pair, user))
}
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var in refreshRequest
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	claims, e := ParseToken(h.secret, in.RefreshToken, TokenTypeRefresh)
	if e != nil {
		httpx.Fail(w, httpx.Unauthorized("刷新令牌无效或已过期"))
		return
	}
	user, e := h.users.FindByUsername(r.Context(), claims.Username)
	if e != nil {
		userFailure(w, e, "用户不可用")
		return
	}
	if e = h.sessions.Validate(r.Context(), claims, user); e != nil {
		sessionFailure(w, e)
		return
	}
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ActorID = user.ID; d.ActorUsername = user.Username; d.ActorRole = user.Role })
	pair, e := issuePairForSession(h.secret, user, h.accessTTL, h.refreshTTL, claims.SessionID)
	if e != nil {
		httpx.Fail(w, httpx.Internal("签发令牌失败", e))
		return
	}
	if e = h.sessions.Rotate(r.Context(), claims, pair.RefreshID, user, h.refreshTTL); e != nil {
		sessionFailure(w, e)
		return
	}
	httpx.OK(w, tokenData(pair, user))
}
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httpx.Fail(w, httpx.Unauthorized("未认证"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	user, e := h.users.FindByUsername(ctx, claims.Username)
	if e != nil {
		userFailure(w, e, "用户不可用")
		return
	}
	httpx.OK(w, UserResponse{ID: user.ID, Username: user.Username, Role: user.Role})
}
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		httpx.Fail(w, httpx.Unauthorized("未认证"))
		return
	}
	if e := h.sessions.Revoke(r.Context(), claims.SessionID); e != nil {
		sessionFailure(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "dbhub_access_token", Value: "", Path: "/", MaxAge: -1, HttpOnly: false, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	httpx.OK(w, map[string]bool{"revoked": true})
}
