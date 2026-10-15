package queryapi

import (
	"context"
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
	mu     sync.Mutex
	items  []*Job
	global chan struct{}
	users  map[int64]chan struct{}
}

func newJobs() *jobs { return &jobs{global: make(chan struct{}, 8), users: map[int64]chan struct{}{}} }
func (m *jobs) admit(ctx context.Context, user, connection int64, sql string) (context.Context, func(error), error) {
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
	if pending >= 100 || userPending >= 10 {
		m.mu.Unlock()
		cancel()
		return nil, nil, fmt.Errorf("查询队列已满，请稍后重试")
	}
	if m.users[user] == nil {
		m.users[user] = make(chan struct{}, 2)
	}
	sem := m.users[user]
	m.items = append(m.items, job)
	m.mu.Unlock()
	waitTimer := time.NewTimer(30 * time.Second)
	defer waitTimer.Stop()
	acquiredUser, acquiredGlobal := false, false
	finish := func(err error) {
		wasCancelled := ctx.Err() != nil
		cancel()
		if acquiredGlobal {
			<-m.global
		}
		if acquiredUser {
			<-sem
		}
		m.mu.Lock()
		defer m.mu.Unlock()
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
	m.mu.Lock()
	now := time.Now()
	job.Started = &now
	job.Status = "running"
	m.mu.Unlock()
	return ctx, finish, nil
}
func (m *jobs) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
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
