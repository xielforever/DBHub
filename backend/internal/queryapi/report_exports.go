package queryapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/reportapi"
)

type reportExportRequest struct {
	Format     string `json:"format"`
	Reason     string `json:"reason"`
	generate   bool
	frozenRows int
}

func (h *Handler) ExportPublished(repo *db.ManagedReportRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := positiveID(r.PathValue("id"))
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		var in struct {
			RequestID     string         `json:"request_id"`
			PublicationNo int64          `json:"publication_no"`
			Params        map[string]any `json:"params"`
			Format        string         `json:"format"`
			Reason        string         `json:"reason"`
		}
		if err = httpx.DecodeJSON(w, r, &in); err != nil {
			httpx.Fail(w, err)
			return
		}
		in.Format = strings.ToLower(in.Format)
		in.Reason = strings.TrimSpace(in.Reason)
		if !requestIDPattern.MatchString(in.RequestID) || in.RequestID == "00000000-0000-0000-0000-000000000000" || in.PublicationNo < 1 || (in.Format != "csv" && in.Format != "json" && in.Format != "xlsx") || in.Reason == "" || len([]rune(in.Reason)) > 500 {
			httpx.Fail(w, httpx.BadRequest("导出编号、版本、格式或用途无效"))
			return
		}
		x, err := repo.ResolvePublished(r.Context(), id, in.PublicationNo)
		if err != nil {
			reportapi.ManagedError(w, err)
			return
		}
		if x.Definition.ExportPolicy == nil || !x.Definition.ExportPolicy.Enabled {
			httpx.Fail(w, httpx.Forbidden("此报表版本尚未批准导出，请联系开发者送审导出策略"))
			return
		}
		params, err := db.ResolveReportParameters(x.Definition.Parameters, in.Params, time.Now())
		if err != nil {
			httpx.Fail(w, httpx.BadRequest(err.Error()))
			return
		}
		policy := x.Definition.ExportPolicy
		if policy.PageSizeParameter != "" {
			// Read one extra row so a query LIMIT cannot conceal truncation.
			params[policy.PageSizeParameter] = int64(policy.MaxRows) + 1
		}
		if policy.OffsetParameter != "" {
			params[policy.OffsetParameter] = int64(0)
		}
		run := &managedRun{repo: repo, execution: x, requestID: in.RequestID, params: params, export: &reportExportRequest{Format: in.Format, Reason: in.Reason}}
		h.execute(w, r.WithContext(context.WithValue(r.Context(), managedRunKey{}, run)), true, nil)
	}
}
