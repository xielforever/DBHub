package automation

import (
	"context"
	"errors"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/db"
)

func (s *Service) scheduleTeamPeriods(ctx context.Context) error {
	items, err := s.Subscriptions.DueTeamSchedules(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, item := range items {
		actor, err := s.actorContext(ctx, item.OwnerID, item.WorkspaceID, item.MemberVersion)
		if err == nil {
			var release func()
			actor, release, err = s.iamTaskLease(actor, "report.subscribe")
			if err == nil {
				err = s.Subscriptions.OpenScheduledTeamPeriod(actor, item)
			}
			if release != nil {
				release()
			}
		}
		if err == nil || invalidBinding(err) || errors.Is(err, db.ErrAutomationDenied) || errors.Is(err, db.ErrManagedDenied) || errors.Is(err, db.ErrManagedConflict) {
			err = s.Subscriptions.MarkTeamScheduleChecked(ctx, item)
		}
		result = errors.Join(result, err)
	}
	return result
}

// RunTeamDelivery always rebinds the frozen recipient, never the plan owner.
func (s *Service) RunTeamDelivery(parent context.Context) (job *db.TeamDeliveryJob, err error) {
	if s.Subscriptions == nil || s.Queries == nil || s.Conns == nil {
		return nil, db.ErrNotFound
	}
	if s.Journal == nil || !s.Journal.Status().Ready {
		return nil, auditlog.ErrUnavailable
	}
	claimCtx, cancel := context.WithTimeout(parent, 5*time.Second)
	job, err = s.Subscriptions.ClaimTeamDelivery(claimCtx)
	cancel()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
			defer stop()
			skip := invalidBinding(err) || errors.Is(err, db.ErrManagedDenied) || errors.Is(err, db.ErrManagedConflict)
			err = errors.Join(err, s.Subscriptions.FailTeamDelivery(cleanup, job, skip, errors.Is(err, db.ErrTeamRecipientReview)))
		}
	}()
	ctx, stop := context.WithTimeout(parent, 70*time.Second)
	defer stop()
	rec := &db.AuditLog{Action: "REPORT_TEAM_DELIVER", ResourceType: "REPORT_TEAM_SUBSCRIPTION",
		ResourceName: job.ID, UserID: &job.UserID, Context: auditctx.Data{
			WorkspaceID: job.WorkspaceID, ActorID: job.UserID, ExecutionRequestID: job.ID, Outcome: "in_progress"}}
	ticket, err := s.Journal.Begin(rec)
	if err != nil {
		return job, err
	}
	ctx, snapshot := auditctx.Begin(ctx)
	auditctx.Update(ctx, func(d *auditctx.Data) { *d = rec.Context })
	ctx = auditctx.WithCheckpoint(ctx, func() error {
		rec.Context = snapshot.Read()
		return ticket.Checkpoint(rec)
	})
	defer func() {
		rec.Context = snapshot.Read()
		rec.Context.Outcome, rec.Status = "succeeded", 1
		if err != nil {
			rec.Context.Outcome, rec.Status = "failed", 2
			rec.ErrorMessage = "本次团队期次未交付。"
		}
		err = errors.Join(err, ticket.Finish(rec))
	}()
	ctx, err = s.actorContext(ctx, job.UserID, job.WorkspaceID, job.MemberVersion)
	if err != nil {
		return job, err
	}
	ctx, release, err := s.iamTaskLease(ctx, "report.view")
	if err != nil {
		return job, err
	}
	if release != nil {
		defer release()
	}
	exec, err := s.Subscriptions.LoadTeamDelivery(ctx, job)
	if err != nil {
		return job, err
	}
	rows, err := s.Queries.ExecuteTeamSubscription(ctx, s.Conns.ManagedReports(), exec, job.ID)
	if err != nil {
		// A query guard may have rejected a change while the request was open.
		_, currentErr := s.Subscriptions.LoadTeamDelivery(ctx, job)
		return job, errors.Join(err, currentErr)
	}
	if err = s.Users.IAMCurrent(ctx); err != nil {
		return job, err
	}
	if err = auditctx.Checkpoint(ctx); err != nil {
		return job, err
	}
	return job, s.Subscriptions.FinishTeamDelivery(ctx, job, exec, rows)
}
