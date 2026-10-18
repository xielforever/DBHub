// Package middleware 提供 HTTP 通用中间件：请求 ID、访问日志、panic 恢复、CORS、JWT 鉴权。
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/httpx"
)

type ctxKey string

// RequestIDKey 请求 ID 在 context 中的键。
const RequestIDKey ctxKey = "request_id"

// Middleware HTTP 中间件函数签名。
type Middleware func(http.Handler) http.Handler

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// Chain 按声明顺序包裹中间件（最先声明的位于最外层）。
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// RequestID 为每个请求注入唯一 X-Request-Id。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			b := make([]byte, 8)
			if _, err := rand.Read(b); err != nil {
				rid = time.Now().Format("20060102150405")
			} else {
				rid = hex.EncodeToString(b)
			}
		}
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), RequestIDKey, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestLogger 结构化访问日志，记录方法、路径、状态码、耗时与请求 ID。
func RequestLogger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			rid, _ := r.Context().Value(RequestIDKey).(string)
			log.Info("http 请求",
				"request_id", rid,
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_ip", clientIP(r),
			)
		})
	}
}

// Recoverer 捕获 handler panic，返回 500 并打印堆栈，避免进程崩溃。
func Recoverer(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					auditctx.Update(r.Context(), func(d *auditctx.Data) { d.Outcome = "unknown" })
					rid, _ := r.Context().Value(RequestIDKey).(string)
					log.Error("panic 恢复",
						"request_id", rid,
						"error", fmt.Sprint(rec),
						"stack", string(debug.Stack()),
					)
					httpx.JSON(w, http.StatusInternalServerError, httpx.Envelope{
						Code: httpx.CodeInternal, Message: "服务器内部错误，请稍后重试",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS 处理跨域；allowedOrigins 为 ["*"] 时反射请求 Origin（开发环境）。
func CORS(allowedOrigins []string) Middleware {
	allowAll := false
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
		}
		allowed[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (allowAll || containsOrigin(allowed, origin)) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id, X-Access-Token, X-Workspace-ID, X-DBHub-CSRF")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuth 校验 Access Token，并把 Claims 注入 context。
//
// 令牌支持三种传递通道，按优先级依次尝试，以兼容会剥离 Authorization 头的
// 预览/反向代理网关：
//  1. Authorization: Bearer <token>（标准通道）
//  2. X-Access-Token: <token>（自定义头兜底）
//  3. Cookie: dbhub_access_token=<token>（浏览器同源自动携带，网关一般不剥离）
func RequireAuth(secret []byte, users auth.UserStore, sessions *auth.SessionStore) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractAccessToken(r)
			if tokenStr == "" {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "missing_token" })
				_, hasCookie := r.Cookie("dbhub_access_token")
				slog.Warn("受保护接口缺少认证令牌",
					"method", r.Method, "path", r.URL.Path,
					"has_authorization_header", r.Header.Get("Authorization") != "",
					"has_x_access_token_header", r.Header.Get("X-Access-Token") != "",
					"has_token_cookie", hasCookie,
					"x_forwarded_for", r.Header.Get("X-Forwarded-For"))
				httpx.Fail(w, httpx.Unauthorized("缺少认证令牌"))
				return
			}
			claims, err := auth.ParseToken(secret, tokenStr, auth.TokenTypeAccess)
			if err != nil {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "invalid_token" })
				httpx.Fail(w, httpx.New(httpx.CodeUnauthorized, "认证令牌无效或已过期", err))
				return
			}
			user, e := users.FindByUsername(r.Context(), claims.Username)
			if auth.UserStoreUnavailable(e) {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "identity_service_unavailable" })
				w.Header().Set("Retry-After", "2")
				httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "身份服务暂不可用，请稍后重试"})
				return
			}
			if e != nil || user.ID != claims.Subject {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "user_unavailable" })
				httpx.Fail(w, httpx.Unauthorized("用户不可用"))
				return
			}
			auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ActorID = user.ID; d.ActorUsername = user.Username; d.ActorRole = user.Role })
			if e = sessions.Validate(r.Context(), claims, user); e != nil {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "session_unavailable" })
				if errors.Is(e, auth.ErrSession) {
					auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "invalid_session" })
					httpx.Fail(w, httpx.Unauthorized("会话已失效"))
				} else {
					httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "会话服务暂不可用"})
				}
				return
			}
			// SameSite cookies can still accompany a simple request from an
			// untrusted sibling origin. Require a non-simple header whenever a
			// mutation relies on cookie authentication. Browser cross-origin
			// callers then need the configured CORS preflight to succeed.
			cookieAuth := !strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") && strings.TrimSpace(r.Header.Get("X-Access-Token")) == ""
			if cookieAuth && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && r.Header.Get("X-DBHub-CSRF") != "1" {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "csrf_header_missing" })
				httpx.Fail(w, httpx.Forbidden("Cookie 认证的修改请求必须携带 X-DBHub-CSRF: 1"))
				return
			}
			claims.Role = user.Role
			if err := auditctx.Checkpoint(r.Context()); err != nil {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.Outcome = "rejected" })
				httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "审计存储暂不可用，请稍后重试"})
				return
			}
			ctx := context.WithValue(r.Context(), auth.ClaimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractAccessToken 按 Authorization → X-Access-Token → Cookie 的顺序取令牌。
func extractAccessToken(r *http.Request) string {
	if header := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	if t := strings.TrimSpace(r.Header.Get("X-Access-Token")); t != "" {
		return t
	}
	if c, err := r.Cookie("dbhub_access_token"); err == nil {
		return strings.TrimSpace(c.Value)
	}
	return ""
}

// RequireRoles 在 RequireAuth 之后使用，限制仅指定角色可访问。
func RequireRoles(allowed ...string) Middleware {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		allowedSet[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				httpx.Fail(w, httpx.Unauthorized("未认证"))
				return
			}
			if _, ok := allowedSet[claims.Role]; !ok {
				auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "insufficient_role" })
				httpx.Fail(w, httpx.Forbidden("当前角色无权执行此操作"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func clientIP(r *http.Request) string {
	if value, ok := r.Context().Value(ctxKey("client_ip")).(string); ok {
		return value
	}
	return auth.ClientIP(r, nil)
}

func TrustedClientIP(prefixes []netip.Prefix) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := auth.ClientIP(r, prefixes)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey("client_ip"), ip)))
		})
	}
}

func containsOrigin(set map[string]struct{}, origin string) bool {
	_, ok := set[origin]
	return ok
}
