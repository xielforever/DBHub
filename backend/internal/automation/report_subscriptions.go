package automation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
)

func (s *Service) RunSubscription(parent context.Context) (*db.ReportSubscriptionRun, error) {
	if s.Subscriptions == nil || s.Queries == nil {
		return nil, db.ErrNotFound
	}
	if s.Journal == nil || !s.Journal.Status().Ready {
		return nil, auditlog.ErrUnavailable
	}
	claimCtx, claimCancel := context.WithTimeout(parent, 5*time.Second)
	run, err := s.Subscriptions.Claim(claimCtx)
	claimCancel()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 70*time.Second)
	defer cancel()
	rec := &db.AuditLog{Action: "REPORT_SUBSCRIPTION_EXECUTE", ResourceType: "REPORT_SUBSCRIPTION", ResourceName: strconv.FormatInt(run.SubscriptionID, 10), UserID: &run.UserID, Context: auditctx.Data{WorkspaceID: run.WorkspaceID, ActorID: run.UserID, JobID: run.ID, ExecutionRequestID: run.RequestID, Outcome: "in_progress"}}
	ticket, err := s.Journal.Begin(rec)
	if err != nil {
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer finishCancel()
		_ = s.Subscriptions.FailRun(finishCtx, run, false)
		return run, err
	}
	started := time.Now()
	ctx, snapshot := auditctx.Begin(ctx)
	auditctx.Update(ctx, func(d *auditctx.Data) { *d = rec.Context })
	ctx = auditctx.WithCheckpoint(ctx, func() error {
		rec.Context = snapshot.Read()
		if rec.Context.ConnectionID > 0 {
			id := rec.Context.ConnectionID
			rec.ConnectionID = &id
		}
		return ticket.Checkpoint(rec)
	})
	ctx, err = s.actorContext(ctx, run.UserID, run.WorkspaceID, run.MemberVersion)
	var release func()
	if err == nil {
		ctx, release, err = s.iamTaskLease(ctx, "report.subscribe")
		if release != nil {
			defer release()
		}
	}
	if err == nil {
		claims, _ := auth.ClaimsFromContext(ctx)
		auditctx.Update(ctx, func(d *auditctx.Data) { d.ActorUsername = claims.Username; d.ActorRole = claims.Role })
		err = s.Subscriptions.CheckRun(ctx, run)
	}
	var execution *db.ManagedExecution
	if err == nil {
		execution, err = s.Conns.ManagedReports().ResolvePublished(ctx, run.ReportID, run.PublicationNo)
	}
	if err == nil {
		err = s.Conns.ManagedReports().PrepareBusinessReport(ctx, execution)
	}
	if err == nil {
		var params map[string]any
		params, err = db.ResolveReportParameters(execution.Definition.Parameters, run.Definition.Params, time.Now())
		if err == nil {
			err = s.Subscriptions.FreezeParameters(ctx, run, params)
		}
	}
	var result *db.ReportSubscriptionSnapshot
	if err == nil {
		rows, e := s.Queries.ExecuteSubscription(ctx, s.Conns.ManagedReports(), run.ReportID, run.PublicationNo, run.RequestID, run.Definition.Params)
		err = e
		if err == nil {
			err = s.Subscriptions.CheckRun(ctx, run)
		}
		if err == nil {
			err = s.Conns.ManagedReports().CheckExecution(ctx, execution)
		}
		if err == nil {
			result = &db.ReportSubscriptionSnapshot{WorkspaceID: run.WorkspaceID, RunID: run.ID, ReportID: run.ReportID, PublicationNo: run.PublicationNo, RequesterID: run.UserID, Name: execution.Definition.Name, CreatedAt: time.Now().UTC(), Params: db.ReportParameterDisplayValues(execution.Definition.Parameters, run.Definition.Params), ManifestHash: execution.Hash, ChartType: execution.Definition.ChartType, ChartConfig: execution.Definition.ChartConfig, Contract: execution.Definition.DataContract, Result: *rows, BusinessPolicyStamp: execution.BusinessPolicyStamp()}
		}
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		err = s.Users.IAMCurrent(ctx)
	}
	if err == nil {
		err = s.Subscriptions.FinishRun(ctx, run, result)
		// Reconcile a possibly committed metadata transaction with the same
		// immutable snapshot. Never run the business query again for this retry.
		if err != nil && ctx.Err() == nil && !errors.Is(err, db.ErrAutomationConflict) && !errors.Is(err, db.ErrAutomationInput) {
			err = s.Subscriptions.FinishRun(ctx, run, result)
		}
	}
	outcome, status := "succeeded", 1
	if err != nil {
		outcome, status = "failed", 2
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		pause := invalidBinding(err) || errors.Is(err, db.ErrManagedDenied) || errors.Is(err, db.ErrManagedConflict)
		finishErr := s.Subscriptions.FailRun(finishCtx, run, pause)
		finishCancel()
		if finishErr != nil {
			outcome, status = "unknown", 0
		}
	}
	rec.Context = snapshot.Read()
	rec.Context.Outcome = outcome
	rec.Status = int16(status)
	duration := int(time.Since(started).Milliseconds())
	rec.DurationMS = &duration
	if outcome != "succeeded" {
		rec.ErrorMessage = "订阅执行未完成，未交付未确认结果。"
	}
	if finishErr := ticket.Finish(rec); finishErr != nil {
		return run, finishErr
	}
	return run, err
}

// The deployment owns the fixed receiver URL and signing key. Only event IDs
// and authenticated app paths are delivered; no rows, values, params or tokens.
func (s *Service) RunWebhook(parent context.Context) error {
	if s.Subscriptions == nil || s.Webhook.URL == "" {
		return db.ErrNotFound
	}
	if s.Journal == nil || !s.Journal.Status().Ready {
		return auditlog.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	item, err := s.Subscriptions.ClaimWebhook(ctx)
	if err != nil {
		return err
	}
	rec := &db.AuditLog{Action: "REPORT_WEBHOOK_SEND", ResourceType: "WEBHOOK_DELIVERY", ResourceName: item.ID, UserID: &item.UserID, Context: auditctx.Data{WorkspaceID: item.WorkspaceID, ActorID: item.UserID, JobID: item.RunID, Outcome: "in_progress"}}
	ticket, err := s.Journal.Begin(rec)
	if err != nil {
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer finishCancel()
		_ = s.Subscriptions.FinishWebhook(finishCtx, item, false, false, 0)
		return err
	}
	ctx, err = s.actorContext(ctx, item.UserID, item.WorkspaceID, item.MemberVersion)
	var release func()
	if err == nil {
		ctx, release, err = s.iamTaskLease(ctx, "report.view")
		if release != nil {
			defer release()
		}
	}
	if err == nil {
		err = s.Subscriptions.CheckWebhook(ctx, item)
	}
	var execution *db.ManagedExecution
	if err == nil {
		execution, err = s.Conns.ManagedReports().ResolvePublished(ctx, item.ReportID, item.PublicationNo)
	}
	if err == nil {
		err = s.Conns.ManagedReports().PrepareBusinessReport(ctx, execution)
	}
	if err == nil && execution.BusinessPolicyStamp() != "" && item.EventType == "report_ready" {
		var deliveryID int64
		deliveryID, err = strconv.ParseInt(item.ResourceID, 10, 64)
		if err == nil {
			_, err = s.Subscriptions.Delivery(ctx, deliveryID)
		}
	}
	permanent := invalidBinding(err) || errors.Is(err, db.ErrManagedDenied) || errors.Is(err, db.ErrManagedConflict) || errors.Is(err, db.ErrAutomationConflict)
	if item.ResourcePath() == "" {
		permanent = true
		err = db.ErrAutomationInput
	}
	httpStatus := 0
	success := false
	if err == nil {
		claims, _ := auth.ClaimsFromContext(ctx)
		rec.Context.ActorUsername = claims.Username
		rec.Context.ActorRole = claims.Role
		err = ticket.Checkpoint(rec)
		if err == nil {
			payload := struct {
				EventID      string    `json:"event_id"`
				EventType    string    `json:"event_type"`
				WorkspaceID  int64     `json:"workspace_id"`
				ResourcePath string    `json:"resource_path"`
				CreatedAt    time.Time `json:"created_at"`
			}{item.ID, item.EventType, item.WorkspaceID, item.ResourcePath(), item.CreatedAt.UTC()}
			body, _ := json.Marshal(payload)
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(s.Webhook.Secret))
			mac.Write([]byte(timestamp + "."))
			mac.Write(body)
			request, e := http.NewRequestWithContext(ctx, http.MethodPost, s.Webhook.URL, bytes.NewReader(body))
			err = e
			if err == nil {
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-DBHub-Event-Id", item.ID)
				request.Header.Set("X-DBHub-Timestamp", timestamp)
				request.Header.Set("X-DBHub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("投递不接受重定向") }}
				response, e := client.Do(request)
				err = e
				if err == nil {
					httpStatus = response.StatusCode
					_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
					response.Body.Close()
					success = httpStatus >= 200 && httpStatus < 300
				}
			}
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer finishCancel()
	finishErr := s.Subscriptions.FinishWebhook(finishCtx, item, success, permanent, httpStatus)
	rec.Status = 2
	rec.Context.Outcome = "failed"
	if success {
		rec.Status = 1
		rec.Context.Outcome = "succeeded"
	}
	if finishErr != nil {
		rec.Status = 0
		rec.Context.Outcome = "unknown"
	}
	if e := ticket.Finish(rec); e != nil {
		return e
	}
	return finishErr
}
