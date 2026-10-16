package queryapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type admission struct {
	ctx    context.Context
	finish func(error)
	err    error
}

func enqueue(m *jobs, ctx context.Context, user int64) <-chan admission {
	result := make(chan admission, 1)
	go func() { c, f, e := m.admit(ctx, user, 1, "SELECT 1"); result <- admission{c, f, e} }()
	return result
}
func receive(t *testing.T, ch <-chan admission) admission {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("admission did not finish")
		return admission{}
	}
}
func waitJobs(t *testing.T, m *jobs, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		size := len(m.items)
		m.mu.Unlock()
		if size == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("jobs were not registered")
}
func assertReleased(t *testing.T, m *jobs) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.global) != 0 || len(m.users) != 0 {
		t.Fatalf("permits or user state leaked: %d, %d", len(m.global), len(m.users))
	}
	if m.telemetry.running != 0 || m.telemetry.waiting != 0 || m.telemetry.peakRunning > m.limits.global || m.telemetry.wait.count != m.telemetry.admitted {
		t.Fatal("queue telemetry does not match released permits")
	}
}

func TestJobsUserAndGlobalLimits(t *testing.T) {
	m := newJobs()
	defer m.Close()
	var initial []admission
	for user := int64(1); user <= 4; user++ {
		for i := 0; i < 2; i++ {
			a := receive(t, enqueue(m, context.Background(), user))
			if a.err != nil {
				t.Fatal(a.err)
			}
			initial = append(initial, a)
		}
	}
	sameUser := enqueue(m, context.Background(), 1)
	otherUser := enqueue(m, context.Background(), 5)
	waitJobs(t, m, 10)
	select {
	case <-sameUser:
		t.Fatal("per-user limit exceeded")
	default:
	}
	select {
	case <-otherUser:
		t.Fatal("global limit exceeded")
	default:
	}
	initial[2].finish(nil)
	c := receive(t, otherUser)
	if c.err != nil {
		t.Fatal(c.err)
	}
	select {
	case <-sameUser:
		t.Fatal("another user released this user's permit")
	default:
	}
	initial[0].finish(nil)
	d := receive(t, sameUser)
	if d.err != nil {
		t.Fatal(d.err)
	}
	c.finish(nil)
	d.finish(nil)
	for _, a := range initial {
		a.finish(nil)
	}
	assertReleased(t, m)
}

func TestJobsQueueBoundsAndCanceledWaiters(t *testing.T) {
	m := newJobsWithLimits(jobLimits{1, 1, 3, 2, time.Second})
	defer m.Close()
	a := receive(t, enqueue(m, context.Background(), 1))
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()
	b := enqueue(m, ctx1, 1)
	waitJobs(t, m, 2)
	if _, _, e := m.admit(context.Background(), 1, 1, "SELECT 1"); e == nil {
		t.Fatal("user pending bound was ignored")
	}
	c := enqueue(m, ctx2, 2)
	waitJobs(t, m, 3)
	if _, _, e := m.admit(context.Background(), 3, 1, "SELECT 1"); e == nil {
		t.Fatal("global pending bound was ignored")
	}
	cancel1()
	cancel2()
	if !errors.Is(receive(t, b).err, context.Canceled) || !errors.Is(receive(t, c).err, context.Canceled) {
		t.Fatal("waiting cancellation was lost")
	}
	a.finish(nil)
	assertReleased(t, m)
}

func TestJobsWaitTimeoutReleasesPartialAdmission(t *testing.T) {
	m := newJobsWithLimits(jobLimits{1, 1, 5, 3, 25 * time.Millisecond})
	defer m.Close()
	a := receive(t, enqueue(m, context.Background(), 1))
	for _, user := range []int64{1, 2} {
		if !errors.Is(receive(t, enqueue(m, context.Background(), user)).err, context.DeadlineExceeded) {
			t.Fatal("wait timeout missing")
		}
	}
	a.finish(nil)
	assertReleased(t, m)
	if m.metricsSnapshot().Timeouts != 2 {
		t.Fatal("queue timeouts were not observed")
	}
	b := receive(t, enqueue(m, context.Background(), 2))
	if b.err != nil {
		t.Fatal("timed-out waiter retained its permit")
	}
	b.finish(nil)
	assertReleased(t, m)
}

func TestJobsCanceledContextNeverGetsExecutionSlot(t *testing.T) {
	m := newJobs()
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		if _, _, e := m.admit(ctx, 1, 1, "SELECT 1"); !errors.Is(e, context.Canceled) {
			t.Fatal("canceled request admitted")
		}
	}
	assertReleased(t, m)
}

func TestJobsFinishIsIdempotentAndShutdownRejectsNewWork(t *testing.T) {
	m := newJobs()
	a := receive(t, enqueue(m, context.Background(), 1))
	m.Close()
	if a.ctx.Err() == nil {
		t.Fatal("shutdown did not cancel active query")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.finish(context.Canceled) }()
	}
	wg.Wait()
	assertReleased(t, m)
	if _, _, e := m.admit(context.Background(), 2, 1, "SELECT 1"); e == nil {
		t.Fatal("shutdown admitted new query")
	}
}
