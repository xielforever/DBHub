// Package automation runs bounded, durable source jobs and platform maintenance.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/user/dbhub/internal/assetapi"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auditlog"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
)

type Service struct {
	Repo    *db.AutomationRepository
	Users   *db.UserRepository
	Conns   *db.ConnectionRepository
	Tunnels *db.TunnelRepository
	Assets  *assetapi.Handler
	Drivers *connectorruntime.Runtime
	Journal *auditlog.Journal
	Config  config.OperationsConfig
	Log     *slog.Logger
	cancel  context.CancelFunc
	done    chan struct{}
}

func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				if _, e := s.RunOne(ctx); e != nil && !errors.Is(e, db.ErrNotFound) && ctx.Err() == nil {
					s.Log.Warn("后台数据源任务暂未完成", "stage", "worker")
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if e := s.Tick(ctx); e != nil && ctx.Err() == nil {
				s.Log.Warn("后台维护暂不可用", "stage", "maintenance")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	go func() { workers.Wait(); close(s.done) }()
}
func (s *Service) Close(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) actorContext(ctx context.Context, actor, wid, version int64) (context.Context, error) {
	ctx = context.WithValue(ctx, auth.ClaimsContextKey, &auth.Claims{Subject: actor, Role: "readonly"})
	ctx, e := s.Users.BindWorkspace(ctx, wid)
	if e != nil {
		return ctx, e
	}
	if db.MemberEpoch(ctx) != version {
		return ctx, db.ErrConnectionChanged
	}
	user, e := s.Users.GetByID(ctx, actor)
	if e != nil {
		return ctx, e
	}
	claims, _ := auth.ClaimsFromContext(ctx)
	copy := *claims
	copy.Username = user.Username
	ctx = context.WithValue(ctx, auth.ClaimsContextKey, &copy)
	return ctx, nil
}

func invalidBinding(e error) bool {
	return errors.Is(e, db.ErrNotFound) || errors.Is(e, db.ErrWorkspaceRequired) || errors.Is(e, db.ErrConnectionChanged) || errors.Is(e, db.ErrSourceAccessDenied) || errors.Is(e, db.ErrCatalogDisabled) || errors.Is(e, auth.ErrUserNotFound)
}

// RunOne is also used by integration tests with real databases. The queue lease
// belongs to the database; a second process cannot exceed the shared two slots.
func (s *Service) RunOne(parent context.Context) (*db.SourceTask, error) {
	if s.Journal == nil || !s.Journal.Status().Ready {
		return nil, auditlog.ErrUnavailable
	}
	claimCtx, claimCancel := context.WithTimeout(parent, 5*time.Second)
	t, e := s.Repo.ClaimSourceTask(claimCtx)
	claimCancel()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()
	status, phase := "failed", "任务失败，请检查连接与读取权限"
	var result any = map[string]any{}
	var check *db.ConnectionCheck
	rec := &db.AuditLog{Action: "SOURCE_TASK_EXECUTE", ResourceType: "SOURCE_TASK", ResourceName: t.ID, UserID: &t.ActorID, ConnectionID: &t.ConnectionID, Context: auditctx.Data{WorkspaceID: t.WorkspaceID, ActorID: t.ActorID, JobID: t.ID, ExecutionRequestID: t.RequestID, Outcome: "in_progress"}}
	ticket, auditErr := s.Journal.Begin(rec)
	if auditErr != nil {
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer finishCancel()
		return s.Repo.FinishSourceTask(finishCtx, t, "failed", "审计存储不可用，未启动目标操作", result, nil)
	}
	started := time.Now()
	ctx, e = s.actorContext(ctx, t.ActorID, t.WorkspaceID, t.MemberVersion)
	var conn *db.Connection
	var password string
	var tunnel *db.SSHTunnel
	if e == nil {
		conn, password, e = s.Conns.GetSecrets(ctx, t.ConnectionID)
	}
	if e == nil && !conn.UpdatedAt.Equal(t.ConfigUpdatedAt) {
		e = db.ErrConnectionChanged
	}
	if e == nil && t.Kind == "collect" {
		claims, _ := auth.ClaimsFromContext(ctx)
		if claims.Role != "admin" && claims.Role != "developer" {
			e = db.ErrAutomationDenied
		}
	}
	if e == nil && conn.SSHTunnelID != nil {
		tunnel, e = s.Tunnels.GetSecrets(ctx, *conn.SSHTunnelID)
		if e == nil && tunnel.UserID != t.ActorID {
			e = db.ErrNotFound
		}
	}
	if e == nil {
		claims, _ := auth.ClaimsFromContext(ctx)
		rec.Context.ActorUsername = claims.Username
		rec.Context.ActorRole = claims.Role
		rec.Context.DatabaseAccount = conn.Username
		rec.Context.ConnectionID = conn.ID
		rec.Context.Driver = conn.Type
		if conn.DefinitionRevisionID != nil {
			rec.Context.DefinitionID = *conn.DefinitionRevisionID
			rec.Context.SourceFamilyID = conn.DefinitionFamilyID
		}
		e = ticket.Checkpoint(rec)
	}
	if e == nil {
		ctx = db.WithSourceTask(ctx, t)
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			ticker := time.NewTicker(400 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					pollCtx, pollCancel := context.WithTimeout(ctx, time.Second)
					active, err := s.Repo.SourceTaskRunning(pollCtx, t)
					pollCancel()
					if err != nil || !active {
						cancel()
						return
					}
				}
			}
		}()
		if t.Kind == "check" {
			checkStart := time.Now()
			testCtx, testCancel := context.WithTimeout(ctx, 20*time.Second)
			info, testErr := s.Drivers.Test(testCtx, conn, password, tunnel)
			testCancel()
			check = &db.ConnectionCheck{OK: testErr == nil, Type: conn.Type, StartedAt: checkStart.UTC(), CheckedAt: time.Now().UTC(), ElapsedMS: time.Since(checkStart).Milliseconds()}
			if testErr != nil {
				diagnostic := connectorruntime.DiagnoseConnectionError(testErr)
				check.Diagnostic = &diagnostic
				check.Error = "连接测试失败: " + diagnostic.Title
				status, phase = "failed", diagnostic.Title
			} else {
				check.Version = info.Version
				check.Compatibility, _ = json.Marshal(info.Compatibility)
				status, phase = "succeeded", "连接检查通过"
			}
			result = check
		} else {
			out, collectErr := s.Assets.Collect(ctx, conn.ID, func(p int, message string) {
				progressCtx, progressCancel := context.WithTimeout(ctx, time.Second)
				defer progressCancel()
				_ = s.Repo.SourceTaskProgress(progressCtx, t, p, message)
			})
			if collectErr != nil {
				e = collectErr
			} else {
				status = out.Status
				result = out
				switch status {
				case "succeeded":
					phase = "结构快照已完整发布"
				case "skipped":
					phase = "此连接不支持完整结构采集"
				case "unknown":
					phase = "发布结果待确认，请刷新目录核实"
				case "busy":
					status, phase = "failed", "该数据源正在采集，请稍后重试"
				default:
					status, phase = "failed", "采集未发布，请检查读取权限；旧快照保留"
				}
			}
		}
		cancel()
		<-watchDone
	} else if invalidBinding(e) || errors.Is(e, db.ErrAutomationDenied) {
		status, phase = "stale", "身份、授权或配置已变化，请重新提交"
	}
	if parent.Err() != nil {
		status, phase = "interrupted", "服务停止，需核实后手动重试"
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer finishCancel()
	finished, finishErr := s.Repo.FinishSourceTask(finishCtx, t, status, phase, result, check)
	rec.Status = 0
	rec.Context.Outcome = "unknown"
	if finishErr == nil {
		rec.Context.Outcome = finished.Status
		if finished.Status == "succeeded" {
			rec.Status = 1
		} else {
			rec.ErrorMessage = finished.Phase
		}
	}
	duration := int(time.Since(started).Milliseconds())
	rec.DurationMS = &duration
	if e = ticket.Finish(rec); e != nil {
		return finished, e
	}
	return finished, finishErr
}

func (s *Service) Tick(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	if e := s.Repo.WorkerHeartbeat(ctx); e != nil {
		return e
	}
	list, e := s.Repo.DueSourceSchedules(ctx)
	if e != nil {
		return e
	}
	for _, schedule := range list {
		actorCtx, bindErr := s.actorContext(ctx, schedule.ActorID, schedule.WorkspaceID, schedule.MemberVersion)
		var conn *db.Connection
		if bindErr == nil {
			conn, _, bindErr = s.Conns.GetSecrets(actorCtx, schedule.ConnectionID)
		}
		if bindErr == nil && !conn.UpdatedAt.Equal(schedule.ConfigUpdatedAt) {
			bindErr = db.ErrConnectionChanged
		}
		if bindErr == nil && schedule.Kind == "collect" {
			claims, _ := auth.ClaimsFromContext(actorCtx)
			if (claims.Role != "admin" && claims.Role != "developer") || !s.Drivers.Capabilities(conn).Collect {
				bindErr = db.ErrAutomationDenied
			}
		}
		if bindErr != nil {
			if invalidBinding(bindErr) || errors.Is(bindErr, db.ErrAutomationDenied) {
				if e = s.Repo.PauseSourceSchedule(ctx, schedule); e != nil {
					return e
				}
				continue
			}
			return bindErr
		}
		if e = s.Repo.TickSourceSchedule(actorCtx, schedule, conn); e != nil {
			return e
		}
	}
	_, retentionErr := s.Repo.PurgeRetention(ctx, s.Config)
	if e = s.Repo.SetOperationalAlert(ctx, "retention_failed", retentionErr != nil, "历史留存维护失败", "请检查元数据库与留存配置，已有记录不会因失败而部分清理。"); e != nil {
		return e
	}
	if retentionErr != nil {
		return retentionErr
	}
	state := s.Journal.Status()
	oldPending := state.Oldest != nil && time.Since(*state.Oldest) > 5*time.Minute
	if e = s.Repo.SetOperationalAlert(ctx, "audit_delivery", !state.Ready || oldPending || state.Pending*100 >= state.Capacity*80, "审计待写队列异常", "请检查审计持久卷、队列积压与元数据库连接。"); e != nil {
		return e
	}
	if s.Config.BackupExpectedHours > 0 {
		backup, err := s.Repo.BackupState(ctx)
		if err != nil {
			return err
		}
		failed := backup != nil && backup.Status == "failed"
		stale := backup == nil || backup.LastSuccessAt == nil || time.Since(*backup.LastSuccessAt) > time.Duration(s.Config.BackupExpectedHours*2)*time.Hour
		if e = s.Repo.SetOperationalAlert(ctx, "backup_failed", failed, "元数据库自动备份失败", "已有成功备份保留，请检查备份服务和私有备份存储。"); e != nil {
			return e
		}
		if e = s.Repo.SetOperationalAlert(ctx, "backup_stale", stale, "元数据库备份超期", "请检查备份服务，最近成功时间超过配置的容许间隔。"); e != nil {
			return e
		}
	}
	return nil
}
