package queryapi

import (
	"context"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/reportapi"
	"net/http"
)

func (h *Handler) ExecuteDashboard(repo *db.ManagedDashboardRepository, managed *db.ManagedReportRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		refresh, tile := r.PathValue("refresh"), r.PathValue("tile")
		if !requestIDPattern.MatchString(refresh) || len(tile) == 0 || len(tile) > 64 {
			httpx.Fail(w, httpx.BadRequest("刷新或卡片编号无效"))
			return
		}
		var empty struct{}
		if _, e := httpx.DecodeOptionalJSON(w, r, &empty); e != nil {
			httpx.Fail(w, e)
			return
		}
		x, e := repo.ResolveTile(r.Context(), refresh, tile)
		if e != nil {
			reportapi.ManagedError(w, e)
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), x.ExpiresAt)
		defer cancel()
		r = r.WithContext(ctx)
		auditDashboardExecution(ctx, x)
		run := &managedRun{repo: managed, execution: x.Report, requestID: x.ExecutionID, dashboard: repo, dashboardExecution: x}
		h.execute(w, r.WithContext(context.WithValue(ctx, managedRunKey{}, run)), true, nil)
	}
}

func (h *Handler) DashboardReceipt(repo *db.ManagedDashboardRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		refresh, tile := r.PathValue("refresh"), r.PathValue("tile")
		if !requestIDPattern.MatchString(refresh) || tile == "" || len(tile) > 64 {
			httpx.Fail(w, httpx.BadRequest("刷新或卡片编号无效"))
			return
		}
		x, e := repo.ResolveTile(r.Context(), refresh, tile)
		if e != nil {
			reportapi.ManagedError(w, e)
			return
		}
		r.SetPathValue("id", x.ExecutionID)
		h.Execution(w, r)
	}
}

func auditDashboardExecution(ctx context.Context, x *db.DashboardExecution) {
	auditctx.Update(ctx, func(d *auditctx.Data) {
		d.DashboardID = x.DashboardID
		d.DashboardMode = x.Mode
		if x.Mode == "draft" {
			d.DashboardDraftVersion = x.DashboardVersion
		} else {
			d.DashboardVersion = x.DashboardVersion
		}
		d.DashboardPublicationNo = x.DashboardPublicationNo
		d.RefreshID = x.RefreshID
		d.TileID = x.TileKey
		d.ContractHash = x.ContractHash
	})
}
