package queryapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/user/dbhub/internal/httpx"
)

type Job struct {
	ID           string     `json:"id"`
	UserID       int64      `json:"user_id"`
	ConnectionID int64      `json:"connection_id"`
	SQL          string     `json:"sql"`
	Status       string     `json:"status"`
	Created      time.Time  `json:"created_at"`
	Started      *time.Time `json:"started_at,omitempty"`
	Finished     *time.Time `json:"finished_at,omitempty"`
	Error        string     `json:"error,omitempty"`
	cancel       context.CancelFunc
}
type jobs struct {
	mu        sync.Mutex
	items     []*Job
	global    chan struct{}
	users     map[int64]*userJobs
	limits    jobLimits
	closed    bool
	telemetry queueTelemetry
}

type userJobs struct {
	slots chan struct{}
	refs  int
}
type jobLimits struct {
	global, user, pending, userPending int
	wait                               time.Duration
}

func newJobs() *jobs { return newJobsWithLimits(jobLimits{8, 2, 100, 10, 30 * time.Second}) }
func newJobsWithLimits(limits jobLimits) *jobs {
	return &jobs{global: make(chan struct{}, limits.global), users: map[int64]*userJobs{}, limits: limits}
}
func (m *jobs) admit(ctx context.Context, user, connection int64, sql string) (context.Context, func(error), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	job := &Job{ID: randomID(), UserID: user, ConnectionID: connection, SQL: sql, Status: "pending", Created: time.Now(), cancel: cancel}
	m.mu.Lock()
	pending := 0
	userPending := 0
	for _, j := range m.items {
		if j.Status == "pending" || j.Status == "running" {
			pending++
			if j.UserID == user {
				userPending++
			}
		}
	}
	if m.closed || pending >= m.limits.pending || userPending >= m.limits.userPending {
		if m.closed {
			m.telemetry.shutdown++
		} else {
			m.telemetry.overload++
		}
		m.mu.Unlock()
		cancel()
		return nil, nil, fmt.Errorf("查询队列已满，请稍后重试")
	}
	if m.users[user] == nil {
		m.users[user] = &userJobs{slots: make(chan struct{}, m.limits.user)}
	}
	userState := m.users[user]
	userState.refs++
	sem := userState.slots
	m.items = append(m.items, job)
	m.telemetry.admitted++
	m.telemetry.waiting++
	if m.telemetry.waiting > m.telemetry.peakWaiting {
		m.telemetry.peakWaiting = m.telemetry.waiting
	}
	m.mu.Unlock()
	waitTimer := time.NewTimer(m.limits.wait)
	defer waitTimer.Stop()
	acquiredUser, acquiredGlobal := false, false
	var finished sync.Once
	finish := func(err error) {
		finished.Do(func() {
			wasCancelled := ctx.Err() != nil
			cancel()
			m.mu.Lock()
			defer m.mu.Unlock()
			if job.Started == nil {
				m.telemetry.waiting--
				m.telemetry.wait.observe(time.Since(job.Created))
				if errors.Is(err, context.DeadlineExceeded) {
					m.telemetry.timeouts++
				}
				if errors.Is(err, context.Canceled) {
					m.telemetry.canceled++
				}
			} else {
				m.telemetry.running--
			}
			// 槽位状态与归还在同一个锁内更新，避免新任务启动时出现虚假的并发峰值。
			if acquiredGlobal {
				<-m.global
			}
			if acquiredUser {
				<-sem
			}
			userState.refs--
			if userState.refs == 0 {
				delete(m.users, user)
			}
			now := time.Now()
			job.Finished = &now
			job.Status = "success"
			if err != nil {
				job.Status = "failed"
				job.Error = "查询执行失败"
				if wasCancelled {
					job.Status = "cancelled"
					job.Error = "查询已取消或超时"
				}
			}
			if len(m.items) > 300 {
				kept := []*Job{}
				finishedKept := 0
				for i := len(m.items) - 1; i >= 0; i-- {
					j := m.items[i]
					if j.Finished == nil || finishedKept < 200 {
						kept = append(kept, j)
						if j.Finished != nil {
							finishedKept++
						}
					}
				}
				for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
					kept[i], kept[j] = kept[j], kept[i]
				}
				m.items = kept
			}

		})
	}
	select {
	case sem <- struct{}{}:
		acquiredUser = true
	case <-waitTimer.C:
		finish(context.DeadlineExceeded)
		return nil, nil, context.DeadlineExceeded
	case <-ctx.Done():
		finish(ctx.Err())
		return nil, nil, ctx.Err()
	}
	select {
	case m.global <- struct{}{}:
		acquiredGlobal = true
	case <-waitTimer.C:
		finish(context.DeadlineExceeded)
		return nil, nil, context.DeadlineExceeded
	case <-ctx.Done():
		finish(ctx.Err())
		return nil, nil, ctx.Err()
	}
	// 取消和可用槽位可能同时就绪，取得槽位后必须再次检查。
	if err := ctx.Err(); err != nil {
		finish(err)
		return nil, nil, err
	}
	m.mu.Lock()
	now := time.Now()
	job.Started = &now
	job.Status = "running"
	m.telemetry.waiting--
	m.telemetry.running++
	m.telemetry.wait.observe(time.Since(job.Created))
	if m.telemetry.running > m.telemetry.peakRunning {
		m.telemetry.peakRunning = m.telemetry.running
	}
	m.mu.Unlock()
	return ctx, finish, nil
}
func (m *jobs) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for _, j := range m.items {
		if j.Finished == nil {
			j.cancel()
		}
	}
}
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	h.jobs.mu.Lock()
	defer h.jobs.mu.Unlock()
	out := []Job{}
	for i := len(h.jobs.items) - 1; i >= 0 && len(out) < 100; i-- {
		j := h.jobs.items[i]
		if j.UserID == c.Subject || isAdmin(c) {
			out = append(out, *j)
		}
	}
	httpx.OK(w, map[string]any{"items": out, "max_concurrent": 2, "max_global_concurrent": 8})
}
func (h *Handler) CancelJob(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	h.jobs.mu.Lock()
	defer h.jobs.mu.Unlock()
	for _, j := range h.jobs.items {
		if j.ID == r.PathValue("id") && (j.UserID == c.Subject || isAdmin(c)) {
			if j.Finished == nil {
				j.cancel()
			}
			httpx.OK(w, map[string]any{"id": j.ID, "cancelled": true})
			return
		}
	}
	httpx.Fail(w, httpx.NotFound("查询任务不存在"))
}
