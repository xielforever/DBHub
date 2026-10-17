package queryapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
)

func TestCancelAuditFailureDoesNotCancel(t *testing.T) {
	ctx, snapshot := auditctx.Begin(context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{Subject: 1, Username: "admin", Role: "admin"}))
	ctx = auditctx.WithCheckpoint(ctx, func() error { return errors.New("audit storage unavailable") })
	cancelled := false
	h := &Handler{jobs: &jobs{items: []*Job{{ID: "owned-job", UserID: 1, ConnectionID: 2, cancel: func() { cancelled = true }}}}}
	r := httptest.NewRequest("POST", "/api/v1/query/jobs/owned-job/cancel", nil).WithContext(ctx)
	r.SetPathValue("id", "owned-job")
	w := httptest.NewRecorder()
	h.CancelJob(w, r)
	if w.Code != 503 || cancelled || snapshot.Read().Outcome != "rejected" {
		t.Fatal("audit failure falsely cancelled or claimed cancellation")
	}
}
