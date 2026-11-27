package reportapi

import (
	"context"
	"net/http"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

func (h *ManagedHandler) Availability(w http.ResponseWriter, r *http.Request) {
	availabilityCommand(w, r, false, h.repo.ChangeAvailability)
}

func (h *ManagedDashboardHandler) Availability(w http.ResponseWriter, r *http.Request) {
	availabilityCommand(w, r, true, h.repo.ChangeAvailability)
}

func availabilityCommand(w http.ResponseWriter, r *http.Request, dashboard bool, apply func(context.Context, int64, db.ContentAvailabilityCommand) (db.ReportCommandResult, error)) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var input db.ContentAvailabilityCommand
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requestKey(input.RequestID) {
		httpx.Fail(w, httpx.BadRequest("下线与恢复操作须携带有效请求键"))
		return
	}
	if !dashboard {
		bindManaged(r.Context(), id, 0)
	} else {
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.DashboardID = id })
	}
	out, err := apply(r.Context(), id, input)
	auditctx.Update(r.Context(), func(d *auditctx.Data) {
		if dashboard {
			d.DashboardPublicationNo = out.Version
		} else {
			d.ReportPublicationNo = out.Version
			d.ReportReleaseKind = input.Action
		}
	})
	commandResponse(w, out, err)
}
