package middleware

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type auditSink struct {
	mu   sync.Mutex
	down bool
	rows []db.AuditLog
}

func (s *auditSink) Create(_ context.Context, a *db.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		return errors.New("unavailable")
	}
	s.rows = append(s.rows, *a)
	return nil
}
func middlewareJournal(t *testing.T, capacity int, sink *auditSink) (*auditlog.Journal, *slog.Logger) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	j, err := auditlog.Open(t.TempDir()+"/audit", capacity, sink, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close(context.Background()) })
	return j, log
}

func TestAuditAdmissionAndDenial(t *testing.T) {
	sink := &auditSink{down: true}
	j, log := middlewareJournal(t, 1, sink)
	calls := 0
	h := Audit(j, log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; httpx.OK(w, nil) }))
	for i, want := range []int{200, 503} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/connections", nil))
		if w.Code != want || calls != 1 {
			t.Fatalf("request %d: status %d calls %d", i, w.Code, calls)
		}
	}
	// Read-only requests remain available for diagnosis with a full queue.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/connections", nil))
	if w.Code != 200 || calls != 2 {
		t.Fatal("ordinary read blocked")
	}
	denied := Audit(j, log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { httpx.Fail(w, httpx.Forbidden("denied")) }))
	w = httptest.NewRecorder()
	denied.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/users", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "denied") {
		t.Fatal("denial returned without durable audit admission")
	}
}

func TestAuditReadPermissionFailureAndMetadataBounds(t *testing.T) {
	sink := &auditSink{}
	j, log := middlewareJournal(t, 10, sink)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = "GET /api/v1/users"
		auditctx.Update(r.Context(), func(d *auditctx.Data) {
			d.ActorID = 2
			d.ActorUsername = "developer"
			d.ActorRole = "developer"
			d.AuthFailure = "insufficient_role"
		})
		httpx.Fail(w, httpx.Forbidden("denied"))
	}), RequestID, Audit(j, log))
	r := httptest.NewRequest("GET", "/api/v1/users?private=do-not-record", nil)
	r.Header.Set("X-Request-Id", strings.Repeat("字", 50))
	r.Header.Set("X-Forwarded-For", "invalid-ip")
	r.Header.Set("Authorization", "Bearer do-not-record-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.rows) != 1 {
		t.Fatal("GET permission denial not recorded")
	}
	a := sink.rows[0]
	if a.Action != "AUTH_DENIED" || a.Context.ActorUsername != "developer" || a.Context.AuthFailure != "insufficient_role" || a.Context.HTTPStatus != 403 || a.Context.Route != "GET /api/v1/users" || a.IPAddress != "" || len(a.TraceID) > 64 {
		t.Fatalf("bad denial metadata: %+v", a)
	}
}

func TestAuditPanicAndImplicitHeaders(t *testing.T) {
	sink := &auditSink{}
	j, log := middlewareJournal(t, 10, sink)
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test") }), Audit(j, log), Recoverer(log))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/connections", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	h = Audit(j, log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Audit-User-Id", "2")
		_, _ = w.Write([]byte("ok"))
	}))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/auth/login", nil))
	if w.Header().Get("X-Audit-User-Id") != "" {
		t.Fatal("internal audit header escaped")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.rows) != 2 || sink.rows[0].Context.Outcome != "unknown" || sink.rows[0].Status != 0 || sink.rows[1].UserID == nil || *sink.rows[1].UserID != 2 {
		t.Fatal("panic or implicit-write audit missing")
	}
}

type failingResponse struct{ header http.Header }

func (w *failingResponse) Header() http.Header       { return w.header }
func (w *failingResponse) WriteHeader(int)           {}
func (w *failingResponse) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestAuditResponseFailurePreservesExecutionOutcome(t *testing.T) {
	sink := &auditSink{}
	j, log := middlewareJournal(t, 10, sink)
	h := Audit(j, log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.Outcome = "succeeded" })
		httpx.OK(w, nil)
	}))
	h.ServeHTTP(&failingResponse{header: http.Header{}}, httptest.NewRequest("POST", "/api/v1/query/execute", nil))
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.rows) != 1 || !sink.rows[0].Context.ResponseFailed || sink.rows[0].Context.Outcome != "succeeded" {
		t.Fatal("network failure overwrote known database result")
	}
}
