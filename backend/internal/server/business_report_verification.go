package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

func businessReportVerification(users *db.UserRepository, reports *db.ManagedReportRepository, drivers *target.Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := positiveWorkspaceUserID(r.PathValue("id"))
		var in struct {
			db.BusinessOutputPlanInput
			DatasetKey    string `json:"dataset_key"`
			PublicationNo int64  `json:"publication_no"`
		}
		if err != nil || httpx.DecodeJSON(w, r, &in) != nil || in.PublicationNo <= 0 {
			writeBusinessMapping(w, nil, db.ErrBusinessMappingInput)
			return
		}
		ctx := r.Context()
		x, err := reports.ResolvePublished(ctx, id, in.PublicationNo)
		if err != nil {
			if errors.Is(err, db.ErrManagedConflict) {
				writeBusinessMapping(w, nil, db.ErrBusinessMappingConflict)
			} else {
				writeBusinessMapping(w, nil, db.ErrBusinessReadDenied)
			}
			return
		}
		plan, err := users.CompileBusinessReportVerification(ctx, in.DatasetKey, in.BusinessOutputPlanInput)
		if err != nil {
			writeBusinessMapping(w, nil, err)
			return
		}
		ref := plan.ReportTarget
		approvedSQL := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(x.Definition.SQL), ";"))
		if ref.ReportID != id || ref.PublicationNo != in.PublicationNo || ref.AccountID != x.AccountID ||
			ref.AccountVersion != x.AccountVersion || ref.DefinitionID != x.Definition.DefinitionID ||
			x.Connection == nil || x.Connection.Type != "postgres" || ref.ApprovedSQL != approvedSQL ||
			len(x.Definition.Parameters) != 0 || x.Manifest.Contract != nil ||
			x.Manifest.MaxRows <= 0 || x.Manifest.TimeoutMS <= 0 {
			writeBusinessMapping(w, nil, db.ErrBusinessReadDenied)
			return
		}
		auditctx.Update(ctx, func(d *auditctx.Data) {
			d.ReportID, d.ReportVersion, d.ReportAccountID = id, x.Version, x.AccountID
			d.ReportPublicationNo, d.ReportReviewID, d.ReportManifestHash = x.PublicationNo, x.ReviewID, x.Hash
			d.ReportMode = "isolated_policy_verification"
		})
		if err = auditctx.Checkpoint(ctx); err != nil {
			httpx.Fail(w, httpx.New(50300, "审计存储暂不可用", nil))
			return
		}
		check := func() error {
			if err := reports.CheckExecution(ctx, x); err != nil {
				return db.ErrBusinessMappingConflict
			}
			latest, err := users.CompileBusinessReportVerification(ctx, in.DatasetKey, in.BusinessOutputPlanInput)
			if err != nil || !reflect.DeepEqual(latest, plan) {
				return db.ErrBusinessMappingConflict
			}
			return nil
		}
		release, err := reports.AcquireExecutionLease(ctx, x)
		if err != nil {
			httpx.Fail(w, httpx.New(50300, "受管查询暂不可用", nil))
			return
		}
		defer release()
		timeout := time.Duration(min(x.Manifest.TimeoutMS, 10000)) * time.Millisecond
		run, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		handle, err := drivers.OpenRelational(run, x.Connection, x.Password, nil)
		if err != nil {
			httpx.Fail(w, httpx.New(50300, "已批准目标暂不可用", nil))
			return
		}
		defer handle.Close()
		if err = handle.Begin(run, true); err != nil {
			writeBusinessMapping(w, nil, db.ErrBusinessReadDenied)
			return
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer stop()
			_ = handle.Rollback(cleanup)
		}()
		if err = target.VerifyBusinessReportView(run, handle, ref, x.Connection.Username); err != nil {
			writeBusinessMapping(w, nil, err)
			return
		}
		if err = check(); err != nil {
			writeBusinessMapping(w, nil, err)
			return
		}
		result, err := target.ExecuteWithOptions(run, handle, plan.Statement, true, target.ExecuteOptions{
			Args: plan.Args, MaxRows: min(x.Manifest.MaxRows, 1000), Timeout: timeout, LosslessNumbers: true,
			BeforeExecute: check,
		})
		if err != nil {
			if errors.Is(err, db.ErrBusinessMappingConflict) {
				writeBusinessMapping(w, nil, err)
			} else {
				httpx.Fail(w, httpx.New(50300, "受管查询未能完成", nil))
			}
			return
		}
		if err = check(); err != nil {
			writeBusinessMapping(w, nil, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpx.OK(w, map[string]any{"columns": result.Columns, "rows": result.Rows, "masked_columns": plan.MaskedColumns})
	}
}
