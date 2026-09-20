// 审计中间件：把关键写操作、SQL 执行与登录尝试落库到 sys_audit_logs。
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
)

// AuditWriter 捕获状态码，并在响应头落网前提取审计专用的用户 ID 标记。
type AuditWriter struct {
	http.ResponseWriter
	status  int
	auditID *int64
}

func (w *AuditWriter) WriteHeader(code int) {
	// 登录等在鉴权中间件之外执行的处理器，可通过 X-Audit-User-Id 响应头
	// 回传操作者 ID；该头仅用于审计关联，不返回给客户端
	if v := w.Header().Get("X-Audit-User-Id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			w.auditID = &id
		}
		w.Header().Del("X-Audit-User-Id")
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Audit 记录匹配的请求。body 不落库（防泄密），仅记录资源、动作、结果与耗时。
func Audit(repo *db.AuditRepository, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &AuditWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			action, resource, name := classify(r.Method, r.URL.Path)
			if action == "" {
				return // 只读或无关路径不审计
			}

			rec := &db.AuditLog{
				Action:       action,
				ResourceType: resource,
				ResourceName: name,
				IPAddress:    clientIP(r),
				UserAgent:    truncate(r.UserAgent(), 500),
				Status:       boolToStatus(sw.status < 400),
				DurationMS:   intPtr(int(time.Since(start).Milliseconds())),
			}
			if rid, ok := r.Context().Value(RequestIDKey).(string); ok {
				rec.TraceID = rid
			}
			if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
				uid := claims.Subject
				rec.UserID = &uid
			} else if sw.auditID != nil {
				rec.UserID = sw.auditID
			}
			if sw.status >= 400 {
				rec.ErrorMessage = http.StatusText(sw.status)
			}
			// 审计写入不应阻塞/影响主流程太久
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := repo.Create(ctx, rec); err != nil {
				log.Warn("审计日志写入失败", "path", r.URL.Path, "error", err)
			}
		})
	}
}

// classify 按方法与路径推断 (action, resourceType, resourceName)。
// 无需审计返回空 action。
func classify(method, path string) (action, resource, name string) {
	if !strings.HasPrefix(path, "/api/v1/") {
		return "", "", ""
	}
	body := strings.TrimPrefix(path, "/api/v1/")
	parts := strings.Split(body, "/")
	// 形如 connections/{id}/test、connections/{id}
	resource = strings.ToUpper(firstOr(parts, 0))
	id := firstOr(parts, 1)
	last := firstOr(parts, len(parts)-1)

	switch {
	case body == "auth/login":
		return "LOGIN", "AUTH", ""
	case body == "auth/change-password":
		return "CHANGE_PASSWORD", "AUTH", ""
	case strings.HasPrefix(body, "connections"):
		resource = "CONNECTION"
	case strings.HasPrefix(body, "ssh-tunnels"):
		resource = "SSH_TUNNEL"
	case strings.HasPrefix(body, "users") || strings.HasPrefix(body, "roles"):
		resource = "USER"
	case strings.HasPrefix(body, "query/execute"):
		return "QUERY", "SQL", ""
	case strings.HasPrefix(body, "query/history"):
		if method == http.MethodGet {
			return "", "", ""
		}
		resource = "QUERY_HISTORY"
	default:
		// GET 不审计
		if method == http.MethodGet {
			return "", "", ""
		}
		resource = strings.ToUpper(resource)
	}

	switch method {
	case http.MethodPost:
		if last == "test" {
			return "TEST", resource, id
		}
		return "CREATE", resource, ""
	case http.MethodPut, http.MethodPatch:
		return "UPDATE", resource, id
	case http.MethodDelete:
		return "DELETE", resource, id
	default:
		return "", "", ""
	}
}

func firstOr(parts []string, i int) string {
	if i >= 0 && i < len(parts) {
		return parts[i]
	}
	return ""
}

func boolToStatus(ok bool) int16 {
	if ok {
		return 1
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func intPtr(n int) *int { return &n }
