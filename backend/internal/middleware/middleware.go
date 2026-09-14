// Package middleware 提供 HTTP 通用中间件：请求 ID、访问日志、panic 恢复、CORS、JWT 鉴权。
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/httpx"
)

type ctxKey string

// RequestIDKey 请求 ID 在 context 中的键。
const RequestIDKey ctxKey = "request_id"

// Middleware HTTP 中间件函数签名。
type Middleware func(http.Handler) http.Handler

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
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
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

// RequireAuth 校验 Bearer Access Token，并把 Claims 注入 context。
func RequireAuth(secret []byte) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" || !strings.HasPrefix(header, "Bearer ") {
				httpx.Fail(w, httpx.Unauthorized("缺少认证令牌"))
				return
			}
			tokenStr := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
			claims, err := auth.ParseToken(secret, tokenStr, auth.TokenTypeAccess)
			if err != nil {
				httpx.Fail(w, httpx.New(httpx.CodeUnauthorized, "认证令牌无效或已过期", err))
				return
			}
			ctx := context.WithValue(r.Context(), auth.ClaimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		if idx := strings.IndexByte(ip, ','); idx > 0 {
			return strings.TrimSpace(ip[:idx])
		}
		return strings.TrimSpace(ip)
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		return host[:idx]
	}
	return host
}

func containsOrigin(set map[string]struct{}, origin string) bool {
	_, ok := set[origin]
	return ok
}
