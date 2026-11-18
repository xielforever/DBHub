package queryapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/db"
)

// ExecuteFormalExport uses the same read-only execution and durable receipt
// path as the original request, with the job ID as the new execution ID.
func (h *Handler) ExecuteFormalExport(ctx context.Context, repo *db.ManagedReportRepository, execution *db.FormalExportExecution, requestID string) (*connector.ExecResult, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.Subject < 1 || db.WorkspaceID(ctx) < 1 || db.MemberEpoch(ctx) < 1 {
		return nil, db.ErrManagedDenied
	}
	run := &managedRun{repo: repo, execution: execution.Report, params: execution.Params, requestID: requestID,
		export: &reportExportRequest{Format: execution.Format, Reason: execution.Reason, generate: true, frozenRows: execution.MaxRows}}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, managedRunKey{}, run), http.MethodPost, "/internal/report-export", nil)
	if err != nil {
		return nil, err
	}
	w := &reportJobResponse{header: make(http.Header), maxBytes: 17 << 20}
	h.execute(w, req, true, nil)
	switch w.status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return nil, db.ErrManagedInput
	case http.StatusForbidden:
		return nil, db.ErrManagedDenied
	case http.StatusOK:
	default:
		return nil, db.ErrManagedConflict
	}
	var packet struct {
		Code int                  `json:"code"`
		Data connector.ExecResult `json:"data"`
	}
	decoder := json.NewDecoder(&w.body)
	decoder.UseNumber()
	if w.overflow || decoder.Decode(&packet) != nil || packet.Code != 0 {
		return nil, db.ErrManagedInput
	}
	return &packet.Data, nil
}
