package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// AuditWriter captures implicit writes as well as explicit status codes. It
// never buffers result data, SQL, tokens or download payloads into the journal.
type AuditWriter struct {
	http.ResponseWriter
	status            int
	wrote, suppressed bool
	writeFailed       bool
	auditID           *int64
	beforeDeny        func(int) bool
}

func (w *AuditWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *AuditWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	if v := w.Header().Get("X-Audit-User-Id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			w.auditID = &id
		}
		w.Header().Del("X-Audit-User-Id")
	}
	w.wrote = true
	if w.beforeDeny != nil && !w.beforeDeny(code) {
		w.status, w.suppressed = 503, true
		w.Header().Del("Content-Length")
		httpx.JSON(w.ResponseWriter, 503, httpx.Envelope{Code: 50300, Message: "审计存储暂不可用，请稍后重试"})
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *AuditWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(200)
	}
	if w.suppressed {
		return len(p), nil
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.writeFailed = true
	}
	return n, err
}
func (w *AuditWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(200)
	}
	if !w.suppressed {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
}

// Audit surrounds authentication and recovery for every API route. Mutations
// and sensitive reads reserve a durable intent before entering any handler.
// Ordinary reads create an event only if authorization denies the request.
func Audit(journal *auditlog.Journal, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if journal == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/") || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			ctx, snapshot := auditctx.Begin(r.Context())
			r = r.WithContext(ctx)
			action, resource, name := classify(r.Method, r.URL.Path)
			rec := &db.AuditLog{Action: truncate(action, 50), ResourceType: truncate(resource, 50), ResourceName: truncate(name, 255), IPAddress: clientIP(r), UserAgent: truncate(r.UserAgent(), 500)}
			if net.ParseIP(rec.IPAddress) == nil {
				rec.IPAddress = ""
			}
			if rid, ok := ctx.Value(RequestIDKey).(string); ok {
				rec.TraceID = truncate(rid, 64)
			}
			var ticket *auditlog.Ticket
			begin := func() bool {
				if ticket != nil {
					return true
				}
				rec.Context = snapshot.Read()
				rec.Context.Outcome = "in_progress"
				var err error
				ticket, err = journal.Begin(rec)
				if err != nil {
					log.Error("请求未执行：审计队列不可用", "request_id", rec.TraceID)
					return false
				}
				return true
			}
			if action != "" && !begin() {
				httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "审计存储暂不可用，请稍后重试"})
				return
			}
			ctx = auditctx.WithCheckpoint(ctx, func() error {
				if ticket == nil {
					return nil
				}
				value := *rec
				value.Context = snapshot.Read()
				if value.Context.ActorID > 0 {
					id := value.Context.ActorID
					value.UserID = &id
				}
				if value.Context.ConnectionID > 0 {
					id := value.Context.ConnectionID
					value.ConnectionID = &id
				}
				return ticket.Checkpoint(&value)
			})
			r = r.WithContext(ctx)
			sw := &AuditWriter{ResponseWriter: w, status: 200}
			sw.beforeDeny = func(code int) bool {
				if ticket != nil {
					return true
				}
				if code != 401 && code != 403 && snapshot.Read().AuthFailure == "" {
					return true
				}
				if rec.Action == "" {
					rec.Action, rec.ResourceType, rec.ResourceName = "AUTH_DENIED", "AUTH", ""
				}
				return begin()
			}
			next.ServeHTTP(sw, r)
			if ticket == nil {
				return
			}
			rec.Context = snapshot.Read()
			rec.Context.HTTPStatus = sw.status
			rec.Context.Route = r.Pattern
			rec.Status = boolToStatus(sw.status < 400)
			rec.DurationMS = intPtr(int(time.Since(start).Milliseconds()))
			if rec.Context.ActorID > 0 {
				id := rec.Context.ActorID
				rec.UserID = &id
			} else if sw.auditID != nil {
				rec.UserID = sw.auditID
			}
			if rec.Context.ConnectionID > 0 {
				id := rec.Context.ConnectionID
				rec.ConnectionID = &id
			}
			if rec.Context.Outcome == "" {
				switch {
				case rec.Context.AuthFailure != "":
					rec.Context.Outcome = "rejected"
				case sw.status < 400:
					rec.Context.Outcome = "completed"
				case sw.status >= 500:
					rec.Context.Outcome = "unknown"
				default:
					rec.Context.Outcome = "rejected"
				}
			}
			if sw.status >= 400 {
				rec.ErrorMessage = http.StatusText(sw.status)
			}
			if sw.writeFailed {
				rec.Status = 0
				rec.Context.ResponseFailed = true
				if rec.Context.Outcome == "download_response" {
					rec.Context.Outcome = "unknown"
				}
				rec.ErrorMessage = "响应传输未确认"
			}
			if rec.Context.Outcome == "unknown" {
				rec.Status = 0
			}
			if err := ticket.Finish(rec); err != nil {
				log.Error("审计结果持久化失败，已保留执行前记录并停止新的审计操作", "event_id", rec.EventID)
			}
		})
	}
}

func classify(method, path string) (action, resource, name string) {
	if !strings.HasPrefix(path, "/api/v1/") {
		return "", "", ""
	}
	body := strings.TrimPrefix(path, "/api/v1/")
	parts := strings.Split(body, "/")
	resource, name = strings.ToUpper(firstOr(parts, 0)), firstOr(parts, 1)
	last := firstOr(parts, len(parts)-1)
	switch {
	case parts[0] == "managed-reports" && method == http.MethodGet:
		return "REPORT_VERSION_VIEW", "REPORT", name
	case parts[0] == "published-reports" && last == "execute" && method == http.MethodPost:
		return "REPORT_PUBLISHED_EXECUTE", "REPORT", name
	case parts[0] == "report-reviews" && method == http.MethodPost:
		if last == "validate" {
			return "REPORT_REVIEW_VALIDATE", "REPORT", name
		}
		return "REPORT_REVIEW_DECISION", "REPORT", name
	case parts[0] == "managed-reports" && method != http.MethodGet && method != http.MethodHead:
		if last == "submit" {
			return "REPORT_SUBMIT", "REPORT", name
		}
		if last == "releases" {
			return "REPORT_RELEASE", "REPORT", name
		}
		if last == "transfer" {
			return "REPORT_TRANSFER", "REPORT", name
		}
		return "REPORT_DRAFT_SAVE", "REPORT", name
	case parts[0] == "report-accounts" && method != http.MethodGet && method != http.MethodHead:
		return "REPORT_ACCOUNT_MANAGE", "REPORT_ACCOUNT", name
	case body == "reports/preview" && method == http.MethodPost:
		return "REPORT_PREVIEW", "REPORT", ""
	case len(parts) == 3 && parts[0] == "reports" && last == "execute" && method == http.MethodPost:
		return "REPORT_EXECUTE", "REPORT", parts[1]
	case body == "auth/login":
		return "LOGIN", "AUTH", ""
	case body == "auth/refresh":
		return "REFRESH", "AUTH", ""
	case body == "auth/logout":
		return "LOGOUT", "AUTH", ""
	case body == "auth/change-password":
		return "CHANGE_PASSWORD", "AUTH", ""
	case body == "query/transaction/begin":
		return "TRANSACTION_BEGIN", "TRANSACTION", ""
	case body == "query/transaction/commit":
		return "TRANSACTION_COMMIT", "TRANSACTION", ""
	case body == "query/transaction/rollback":
		return "TRANSACTION_ROLLBACK", "TRANSACTION", ""
	case len(parts) == 4 && parts[0] == "query" && parts[1] == "jobs" && last == "cancel":
		return "QUERY_CANCEL", "QUERY_JOB", parts[2]
	case len(parts) == 4 && parts[0] == "query" && parts[1] == "exports" && last == "download" && (method == http.MethodGet || method == http.MethodHead):
		return "EXPORT_DOWNLOAD", "EXPORT", parts[2]
	}
	if method == http.MethodGet || method == http.MethodHead {
		return "", "", ""
	}
	// Presence is a heartbeat, not a business mutation.
	if body == "query/presence" {
		return "", "", ""
	}
	switch {
	case parts[0] == "connections":
		resource = "CONNECTION"
	case parts[0] == "reports":
		resource = "REPORT"
	case parts[0] == "ssh-tunnels":
		resource = "SSH_TUNNEL"
	case parts[0] == "users" || parts[0] == "roles":
		resource = "USER"
	case body == "query/execute":
		return "QUERY", "SQL", ""
	case strings.HasPrefix(body, "query/history"):
		resource = "QUERY_HISTORY"
	case strings.HasPrefix(body, "query/exports"):
		if last == "review" {
			return "EXPORT_REVIEW", "EXPORT", firstOr(parts, 2)
		}
		return "EXPORT_CREATE", "EXPORT", ""
	}
	switch method {
	case http.MethodPost:
		if last == "test" {
			return "TEST", resource, name
		}
		return "CREATE", resource, name
	case http.MethodPut, http.MethodPatch:
		return "UPDATE", resource, name
	case http.MethodDelete:
		return "DELETE", resource, name
	}
	return "", "", ""
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
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func intPtr(n int) *int { return &n }
