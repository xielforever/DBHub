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

func (s *Service) scheduleDashboardDeliveries(ctx context.Context) error {
	if s.Conns == nil {
		return nil
	}
	repo := s.Conns.ManagedDashboards()
	items, err := repo.DueDashboardDeliveries(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, item := range items {
		actor, e := s.actorContext(ctx, item.OwnerID, item.WorkspaceID, item.MemberVersion)
		if e == nil {
			var release func()
			actor, release, e = s.iamTaskLease(actor, "dashboard.view")
			if e == nil {
				e = repo.OpenDashboardDeliverySlot(actor, item)
			}
			if release != nil {
				release()
			}
		}
		if e == nil || invalidBinding(e) || errors.Is(e, db.ErrAutomationDenied) || errors.Is(e, db.ErrManagedDenied) {
			e = repo.MarkDashboardDeliverySlot(ctx, item)
		}
		result = errors.Join(result, e)
	}
	return result
}

func (s *Service) RunDashboardDelivery(parent context.Context) (job *db.DashboardDeliveryJob, err error) {
	if s.Conns == nil || s.Queries == nil {
		return nil, db.ErrNotFound
	}
	if s.Journal == nil || !s.Journal.Status().Ready {
		return nil, auditlog.ErrUnavailable
	}
	repo := s.Conns.ManagedDashboards()
	claim, cancel := context.WithTimeout(parent, 5*time.Second)
	job, err = repo.ClaimDashboardDelivery(claim)
	cancel()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			ctx, stop := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
			defer stop()
			skip := invalidBinding(err) || errors.Is(err, db.ErrManagedDenied) || errors.Is(err, db.ErrManagedConflict) || errors.Is(err, db.ErrAutomationDenied)
			err = errors.Join(err, repo.FailDashboardDelivery(ctx, job, skip, errors.Is(err, db.ErrDashboardRecipientReview)))
		}
	}()
	ctx, stop := context.WithTimeout(parent, 5*time.Minute)
	defer stop()
	rec := &db.AuditLog{Action: "DASHBOARD_DELIVERY", ResourceType: "DASHBOARD_DELIVERY", ResourceName: strconv.FormatInt(job.ID, 10), UserID: &job.UserID,
		Context: auditctx.Data{WorkspaceID: job.WorkspaceID, ActorID: job.UserID, Outcome: "in_progress"}}
	ticket, err := s.Journal.Begin(rec)
	if err != nil {
		return job, err
	}
	ctx, state := auditctx.Begin(ctx)
	auditctx.Update(ctx, func(d *auditctx.Data) { *d = rec.Context })
	ctx = auditctx.WithCheckpoint(ctx, func() error { rec.Context = state.Read(); return ticket.Checkpoint(rec) })
	defer func() {
		rec.Context = state.Read()
		rec.Context.Outcome, rec.Status = "succeeded", 1
		if err != nil {
			rec.Context.Outcome, rec.Status = "failed", 2
			rec.ErrorMessage = "本次仪表盘交付未完成。"
		}
		err = errors.Join(err, ticket.Finish(rec))
	}()
	ctx, err = s.actorContext(ctx, job.UserID, job.WorkspaceID, job.MemberVersion)
	if err != nil {
		return job, err
	}
	ctx, release, err := s.iamTaskLease(ctx, "dashboard.view")
	if err != nil {
		return job, err
	}
	if release != nil {
		defer release()
	}
	work, err := repo.LoadDashboardWork(ctx, job)
	if err != nil {
		return job, err
	}
	cards := []db.DashboardDeliveredCard{}
	for _, tile := range work.DeliveryTiles() {
		card := db.DashboardDeliveredCard{Key: tile.Key, Status: tile.Status}
		if tile.Status == "ready" {
			x, e := repo.DashboardWorkTile(ctx, job, work, tile.Key)
			if e == nil {
				result, quality, execErr := s.Queries.ExecuteDashboardDeliveryTile(ctx, repo, s.Conns.ManagedReports(), x)
				e = execErr
				if e == nil {
					card, e = work.CollectDashboardCard(ctx, x, result, quality)
				}
			}
			if e != nil {
				if tile.Required {
					return job, e
				}
				card = db.DashboardDeliveredCard{Key: tile.Key, Status: "unavailable"}
			}
		}
		cards = append(cards, card)
	}
	if err = s.Users.IAMCurrent(ctx); err != nil {
		return job, err
	}
	if err = auditctx.Checkpoint(ctx); err != nil {
		return job, err
	}
	return job, repo.FinishDashboardDelivery(ctx, job, work, cards)
}
