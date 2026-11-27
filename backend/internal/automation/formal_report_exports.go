package automation

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/db"
)

func (s *Service) RunFormalExport(parent context.Context) (job *db.FormalExportJob, err error) {
	if s.Conns == nil || s.Queries == nil {
		return nil, db.ErrNotFound
	}
	if s.Journal == nil || !s.Journal.Status().Ready {
		return nil, auditlog.ErrUnavailable
	}
	reports := s.Conns.ManagedReports()
	claimCtx, cancel := context.WithTimeout(parent, 5*time.Second)
	job, err = reports.ClaimFormalExport(claimCtx)
	cancel()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
			defer stop()
			err = errors.Join(err, reports.FailFormalExport(cleanup, job))
		}
	}()
	ctx, stop := context.WithTimeout(parent, 70*time.Second)
	defer stop()
	rec := &db.AuditLog{Action: "REPORT_EXPORT_GENERATE", ResourceType: "REPORT_EXPORT",
		ResourceName: strconv.FormatInt(job.ExportID, 10), UserID: &job.UserID,
		Context: auditctx.Data{WorkspaceID: job.WorkspaceID, ActorID: job.UserID,
			ExportID: job.ExportID, ExecutionRequestID: job.ID, Outcome: "in_progress"}}
	ticket, err := s.Journal.Begin(rec)
	if err != nil {
		return job, err
	}
	ctx, captured := auditctx.Begin(ctx)
	auditctx.Update(ctx, func(d *auditctx.Data) { *d = rec.Context })
	ctx = auditctx.WithCheckpoint(ctx, func() error {
		rec.Context = captured.Read()
		return ticket.Checkpoint(rec)
	})
	defer func() {
		rec.Context = captured.Read()
		rec.Context.Outcome, rec.Status = "succeeded", 1
		if err != nil {
			rec.Context.Outcome, rec.Status = "failed", 2
			rec.ErrorMessage = "正式文件未通过当前运行条件。"
		}
		err = errors.Join(err, ticket.Finish(rec))
	}()
	ctx, err = s.actorContext(ctx, job.UserID, job.WorkspaceID, job.MemberVersion)
	if err != nil {
		return job, err
	}
	ctx, release, err := s.iamTaskLease(ctx, "report.export")
	if err != nil {
		return job, err
	}
	if release != nil {
		defer release()
	}
	execution, err := reports.LoadFormalExport(ctx, job)
	if err != nil {
		return job, err
	}
	result, err := s.Queries.ExecuteFormalExport(ctx, reports, execution, job.ID)
	if err != nil {
		return job, err
	}
	if err = s.Users.IAMCurrent(ctx); err != nil {
		return job, err
	}
	if err = auditctx.Checkpoint(ctx); err != nil {
		return job, err
	}
	return job, reports.FinishFormalExport(ctx, job, execution, result)
}
