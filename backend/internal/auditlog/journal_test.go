package auditlog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/db"
)

type testSink struct {
	mu            sync.Mutex
	down, loseACK bool
	attempts      int
	rows          map[string]db.AuditLog
}

func (s *testSink) Create(_ context.Context, a *db.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.down {
		return errors.New("database unavailable")
	}
	if s.rows == nil {
		s.rows = map[string]db.AuditLog{}
	}
	s.rows[a.EventID] = *a
	if s.loseACK {
		s.loseACK = false
		return errors.New("ack lost")
	}
	return nil
}
func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func openTest(t *testing.T, dir string, capacity int, sink Sink) *Journal {
	t.Helper()
	j, err := Open(dir, capacity, sink, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = j.Close(ctx)
	})
	return j
}
func event() *db.AuditLog {
	return &db.AuditLog{Action: "QUERY", ResourceType: "SQL", Context: auditctx.Data{ActorID: 2, ActorUsername: "developer"}}
}
func finishTest(t *testing.T, j *Journal) *db.AuditLog {
	t.Helper()
	a := event()
	ticket, err := j.Begin(a)
	if err != nil {
		t.Fatal(err)
	}
	a.Context.Outcome = "succeeded"
	a.Status = 1
	if err = ticket.Finish(a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestJournalAutomaticRetry(t *testing.T) {
	sink := &testSink{down: true}
	dir := t.TempDir() + "/audit"
	j := openTest(t, dir, 10, sink)
	a := finishTest(t, j)
	if s := j.Status(); s.Pending != 1 || !s.Ready || s.DeliveryFailures == 0 {
		t.Fatalf("bad queued status: %+v", s)
	}
	info, err := os.Stat(filepath.Join(dir, a.EventID+".audit"))
	if err != nil || info.Size() != fileSize || info.Mode().Perm() != 0600 {
		t.Fatal("event space not privately reserved")
	}
	sink.mu.Lock()
	sink.down = false
	sink.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for j.Status().Pending != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if j.Status().Pending != 0 {
		t.Fatal("worker did not retry")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.rows) != 1 || sink.rows[a.EventID].Context.Outcome != "succeeded" {
		t.Fatal("result lost or duplicated")
	}
}

// A real child process is killed after its fsync boundary, with no Close/Flush.
func TestJournalProcessRecovery(t *testing.T) {
	for _, stage := range []string{"intent", "checkpoint", "finished", "torn"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir() + "/audit"
			cmd := exec.Command(os.Args[0], "-test.run=^TestJournalCrashHelper$")
			cmd.Env = append(os.Environ(), "DBHUB_AUDIT_CRASH_HELPER=1", "DBHUB_AUDIT_TEST_DIR="+dir, "DBHUB_AUDIT_TEST_STAGE="+stage)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ready := make(chan string, 1)
			go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
			select {
			case line := <-ready:
				if strings.TrimSpace(line) != "ready" {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal("helper failed before durable boundary")
				}
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal("helper timed out")
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			sink := &testSink{}
			j := openTest(t, dir, 10, sink)
			if err = j.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			sink.mu.Lock()
			defer sink.mu.Unlock()
			if len(sink.rows) != 1 {
				t.Fatalf("recovered %d events", len(sink.rows))
			}
			for _, row := range sink.rows {
				want := "unknown"
				if stage == "finished" {
					want = "succeeded"
				}
				if row.Context.Outcome != want {
					t.Fatalf("recovered %s, want %s", row.Context.Outcome, want)
				}
				if row.CreatedAt.IsZero() || row.EventID == "" {
					t.Fatal("unstable event identity")
				}
				if stage == "checkpoint" && (row.Context.DatabaseAccount != "audit_reader" || row.Context.DefinitionID != 22) {
					t.Fatal("pre-execution checkpoint lost")
				}
			}
		})
	}
}
func TestJournalCrashHelper(t *testing.T) {
	if os.Getenv("DBHUB_AUDIT_CRASH_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	j, err := Open(os.Getenv("DBHUB_AUDIT_TEST_DIR"), 10, &testSink{down: true}, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	a := event()
	ticket, err := j.Begin(a)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("DBHUB_AUDIT_TEST_STAGE") == "checkpoint" {
		a.Context.DatabaseAccount = "audit_reader"
		a.Context.DefinitionID = 22
		if err = ticket.Checkpoint(a); err != nil {
			t.Fatal(err)
		}
	} else if os.Getenv("DBHUB_AUDIT_TEST_STAGE") != "intent" {
		a.Context.Outcome = "succeeded"
		a.Status = 1
		if err = ticket.Finish(a); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("DBHUB_AUDIT_TEST_STAGE") == "torn" {
			f, err := os.OpenFile(filepath.Join(j.dir, a.EventID+".audit"), os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteAt([]byte("partial"), frameSize+40)
			if err == nil {
				err = f.Sync()
			}
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	fmt.Println("ready")
	select {}
}

func TestJournalLostAckAndConcurrentReplay(t *testing.T) {
	sink := &testSink{loseACK: true}
	j := openTest(t, t.TempDir()+"/audit", 10, sink)
	a := finishTest(t, j)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = j.Flush(context.Background()); _ = j.Status() }()
	}
	wg.Wait()
	if s := j.Status(); !s.Ready || s.Pending != 0 || s.StorageFailures != 0 {
		t.Fatalf("bad status after duplicate ACK: %+v", s)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.rows) != 1 || sink.rows[a.EventID].Context.Outcome != "succeeded" {
		t.Fatal("replay changed event identity")
	}
}

func TestJournalCapacityAndPrivacy(t *testing.T) {
	sink := &testSink{down: true}
	dir := t.TempDir() + "/audit"
	j := openTest(t, dir, 1, sink)
	a := event()
	a.Params = map[string]any{"password": "do-not-journal-this"}
	ticket, err := j.Begin(a)
	if err != nil {
		t.Fatal(err)
	}
	a.Context.Outcome = "succeeded"
	if err = ticket.Finish(a); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Begin(event()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("full queue admitted work")
	}
	raw, err := os.ReadFile(filepath.Join(dir, a.EventID+".audit"))
	if err != nil || strings.Contains(string(raw), "do-not-journal-this") {
		t.Fatal("params leaked into journal")
	}
	if j.Status().Ready {
		t.Fatal("full queue claims ready")
	}
	sink.mu.Lock()
	sink.down = false
	sink.mu.Unlock()
	if err = j.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !j.Status().Ready {
		t.Fatal("capacity did not recover")
	}
}

func TestJournalStorageFailureBlocksFurtherWork(t *testing.T) {
	dir := t.TempDir() + "/audit"
	j := openTest(t, dir, 10, &testSink{down: true})
	a := event()
	ticket, err := j.Begin(a)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(dir, dir+"-detached"); err != nil {
		t.Fatal(err)
	}
	a.Context.Outcome = "succeeded"
	if err = ticket.Finish(a); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing result storage not detected")
	}
	if _, err = j.Begin(event()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("admitted operation after storage failure")
	}
	if j.Status().Ready || j.Status().StorageFailures != 1 {
		t.Fatal("storage failure invisible")
	}
	if err = os.Rename(dir+"-detached", dir); err != nil {
		t.Fatal(err)
	}
	_ = j.Close(context.Background())
	sink := &testSink{}
	recovered := openTest(t, dir, 10, sink)
	if err = recovered.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.rows[a.EventID].Context.Outcome != "unknown" {
		t.Fatal("failed final write falsely reported success")
	}
}

func TestJournalIntentAndCheckpointStorageFailure(t *testing.T) {
	for _, stage := range []string{"intent", "checkpoint"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir() + "/audit"
			j := openTest(t, dir, 10, &testSink{down: true})
			a := event()
			var ticket *Ticket
			var err error
			if stage == "checkpoint" {
				ticket, err = j.Begin(a)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = os.Rename(dir, dir+"-detached"); err != nil {
				t.Fatal(err)
			}
			if stage == "checkpoint" {
				err = ticket.Checkpoint(a)
			} else {
				_, err = j.Begin(a)
			}
			if !errors.Is(err, ErrUnavailable) || j.Status().Ready {
				t.Fatal("unsafe work admitted after storage error")
			}
			if err = os.Rename(dir+"-detached", dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJournalStartupGuards(t *testing.T) {
	dir := t.TempDir() + "/audit"
	_ = openTest(t, dir, 10, &testSink{})
	if other, err := Open(dir, 10, &testSink{}, quietLog()); err == nil {
		_ = other.Close(context.Background())
		t.Fatal("shared journal accepted by second process")
	}
	public := t.TempDir() + "/public"
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	if j, err := Open(public, 10, &testSink{}, quietLog()); err == nil {
		_ = j.Close(context.Background())
		t.Fatal("insecure directory accepted")
	}
	corrupt := t.TempDir() + "/corrupt"
	if err := os.Mkdir(corrupt, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(corrupt, "11111111-1111-4111-8111-111111111111.audit")
	if err := os.WriteFile(path, []byte("broken intent"), 0600); err != nil {
		t.Fatal(err)
	}
	if j, err := Open(corrupt, 10, &testSink{}, quietLog()); err == nil {
		_ = j.Close(context.Background())
		t.Fatal("corrupt intent discarded")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("corrupt record deleted")
	}
}

func TestJournalConcurrentRequests(t *testing.T) {
	sink := &testSink{down: true}
	j := openTest(t, t.TempDir()+"/audit", 100, sink)
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := event()
			ticket, err := j.Begin(a)
			if err != nil {
				t.Error(err)
				return
			}
			_ = j.Status()
			a.Context.Outcome = "succeeded"
			if err = ticket.Finish(a); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	sink.mu.Lock()
	sink.down = false
	sink.mu.Unlock()
	if err := j.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.rows) != 40 {
		t.Fatalf("lost concurrent events: %d", len(sink.rows))
	}
}
