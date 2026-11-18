package queryapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/db"
)

type reportJobResponse struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	maxBytes int
	overflow bool
}

func (w *reportJobResponse) Header() http.Header { return w.header }
func (w *reportJobResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *reportJobResponse) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(value) > w.maxBytes {
		w.overflow = true
		return 0, errors.New("后台报表结果超过大小限制")
	}
	return w.body.Write(value)
}

// ExecuteSubscription is an internal adapter over the same immutable-report
// handler used by the browser. The scheduler must first rebind the saved actor
// and workspace/member epoch; no JWT or administrative credential is minted.
func (h *Handler) ExecuteSubscription(ctx context.Context, repo *db.ManagedReportRepository, reportID, publication int64, requestID string, params map[string]any) (*connector.ExecResult, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.Subject < 1 || db.WorkspaceID(ctx) < 1 || db.MemberEpoch(ctx) < 1 {
		return nil, db.ErrManagedDenied
	}
	raw, err := json.Marshal(map[string]any{"publication_no": publication, "request_id": requestID, "params": params})
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/internal/report-subscription", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	r.SetPathValue("id", strconv.FormatInt(reportID, 10))
	w := &reportJobResponse{header: make(http.Header), maxBytes: 4 << 20}
	h.ExecutePublished(repo)(w, r)
	return readReportJobResult(w)
}

// ExecuteTeamSubscription uses the recipient-bound execution prepared by the
// team worker. The query pipeline rechecks its guard before and after execution.
func (h *Handler) ExecuteTeamSubscription(ctx context.Context, repo *db.ManagedReportRepository, execution *db.TeamDeliveryExecution, requestID string) (*connector.ExecResult, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.Subject < 1 || db.WorkspaceID(ctx) < 1 || db.MemberEpoch(ctx) < 1 {
		return nil, db.ErrManagedDenied
	}
	run := &managedRun{repo: repo, execution: execution.Report, params: execution.Params, requestID: requestID}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, managedRunKey{}, run), http.MethodPost, "/internal/team-subscription", nil)
	if err != nil {
		return nil, err
	}
	w := &reportJobResponse{header: make(http.Header), maxBytes: 4 << 20}
	h.execute(w, req, true, nil)
	return readReportJobResult(w)
}

func readReportJobResult(w *reportJobResponse) (*connector.ExecResult, error) {
	if w.overflow {
		return nil, errors.New("订阅结果超过4MiB，请缩小报表范围")
	}
	var packet struct {
		Code    int                  `json:"code"`
		Message string               `json:"message"`
		Data    connector.ExecResult `json:"data"`
	}
	decoder := json.NewDecoder(&w.body)
	decoder.UseNumber()
	if decoder.Decode(&packet) != nil {
		return nil, errors.New("订阅执行结果不可读取")
	}
	if w.status != 200 || packet.Code != 0 {
		return nil, errors.New("订阅执行未通过当前授权、报表版本、参数或数据校验，请查看正式报表")
	}
	return &packet.Data, nil
}
