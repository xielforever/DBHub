// Package auditlog keeps audit events on durable storage before admitting work.
// Each event reserves space for both the intent and result. A torn result is
// recovered as unknown; an event is removed only after an idempotent DB insert.
package auditlog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/user/dbhub/internal/db"
)

const frameSize = 16 * 1024
const fileSize = 2 * frameSize

var ErrUnavailable = errors.New("audit journal unavailable")

type Sink interface {
	Create(context.Context, *db.AuditLog) error
}

type Status struct {
	Ready            bool       `json:"ready"`
	Pending          int        `json:"pending"`
	Capacity         int        `json:"capacity"`
	Oldest           *time.Time `json:"oldest,omitempty"`
	LastDelivered    *time.Time `json:"last_delivered,omitempty"`
	DeliveryFailures uint64     `json:"delivery_failures"`
	StorageFailures  uint64     `json:"storage_failures"`
	LastError        string     `json:"last_error,omitempty"`
}

type Journal struct {
	mu                    sync.Mutex
	dir                   string
	lock                  *os.File
	sink                  Sink
	log                   *slog.Logger
	capacity              int
	items                 map[string]*Ticket
	closed, storageFailed bool
	status                Status
	cancel                context.CancelFunc
	done                  chan struct{}
}

type Ticket struct {
	mu        sync.Mutex
	journal   *Journal
	record    db.AuditLog
	created   time.Time
	ready     bool
	delivered bool
	next      time.Time
	failures  int
}

// Open requires a private directory on persistent local storage, owned by one
// replica. flock prevents concurrent replay by two processes using the same path.
func Open(dir string, capacity int, sink Sink, log *slog.Logger) (*Journal, error) {
	if dir == "" || capacity < 1 || sink == nil {
		return nil, ErrUnavailable
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("audit directory must be a private directory (0700)")
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("audit directory is already in use")
	}
	j := &Journal{dir: dir, lock: lock, sink: sink, log: log, capacity: capacity, items: map[string]*Ticket{}, done: make(chan struct{})}
	if err = j.recover(); err != nil {
		lock.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	go j.run(ctx)
	return j, nil
}

func encodeFrame(a *db.AuditLog) ([]byte, error) {
	// Params/body are deliberately excluded, even if a caller sets them.
	value := *a
	value.Params = nil
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > frameSize-36 {
		return nil, errors.New("audit event exceeds reserved frame")
	}
	frame := make([]byte, frameSize)
	binary.BigEndian.PutUint32(frame, uint32(len(raw)))
	hash := sha256.Sum256(raw)
	copy(frame[4:36], hash[:])
	copy(frame[36:], raw)
	return frame, nil
}

func decodeFrame(frame []byte) (db.AuditLog, error) {
	var a db.AuditLog
	if len(frame) != frameSize {
		return a, io.ErrUnexpectedEOF
	}
	n := int(binary.BigEndian.Uint32(frame))
	if n < 1 || n > frameSize-36 {
		return a, errors.New("invalid audit frame")
	}
	hash := sha256.Sum256(frame[36 : 36+n])
	if hex.EncodeToString(hash[:]) != hex.EncodeToString(frame[4:36]) {
		return a, errors.New("invalid audit checksum")
	}
	err := json.Unmarshal(frame[36:36+n], &a)
	return a, err
}

func (j *Journal) syncDir() error {
	d, err := os.Open(j.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (j *Journal) recover() error {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tmp") {
			if validID(strings.TrimSuffix(strings.TrimPrefix(name, "."), ".tmp")) {
				// Work was never admitted before the intent's rename and dir fsync.
				if err := os.Remove(filepath.Join(j.dir, name)); err != nil {
					return err
				}
			}
			continue
		}
		if !strings.HasSuffix(name, ".audit") {
			continue
		}
		id := strings.TrimSuffix(name, ".audit")
		if !validID(id) || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("invalid audit journal entry")
		}
		raw, err := os.ReadFile(filepath.Join(j.dir, name))
		if err != nil || len(raw) != fileSize {
			return fmt.Errorf("invalid audit journal file %s", name)
		}
		intent, err := decodeFrame(raw[:frameSize])
		if err != nil || intent.EventID != id || intent.CreatedAt.IsZero() {
			return fmt.Errorf("invalid audit intent %s", name)
		}
		result, err := decodeFrame(raw[frameSize:])
		if err != nil {
			result = intent
			result.Status = 0
			result.Context.Outcome = "unknown"
			result.ErrorMessage = "请求中断，操作结果待确认"
		} else if result.EventID != id || !result.CreatedAt.Equal(intent.CreatedAt) {
			return fmt.Errorf("audit identity mismatch %s", name)
		}
		t := &Ticket{journal: j, record: result, created: result.CreatedAt, ready: true}
		// Persist the conservative recovery decision before any delivery.
		if err := t.writeResult(); err != nil {
			return err
		}
		j.items[id] = t
	}
	return nil
}

// Begin fsyncs the intent and reserves the result's disk space before work.
func (j *Journal) Begin(a *db.AuditLog) (*Ticket, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.storageFailed || len(j.items) >= j.capacity {
		return nil, ErrUnavailable
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	a.EventID = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	a.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := j.writeIntent(a); err != nil {
		j.storageFailureLocked()
		return nil, ErrUnavailable
	}
	t := &Ticket{journal: j, record: *a, created: a.CreatedAt}
	j.items[a.EventID] = t
	return t, nil
}

func (j *Journal) writeIntent(a *db.AuditLog) error {
	frame, err := encodeFrame(a)
	if err != nil {
		return err
	}
	raw := make([]byte, fileSize)
	copy(raw, frame)
	tmp := filepath.Join(j.dir, "."+a.EventID+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, err = f.Write(raw) // Real allocation, not a sparse Truncate.
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(j.dir, a.EventID+".audit"))
	}
	if err == nil {
		err = j.syncDir()
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// Checkpoint atomically replaces the intent with a verified identity/target
// snapshot. Either the old or new intent survives a crash during replacement.
func (t *Ticket) Checkpoint(a *db.AuditLog) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ready {
		return errors.New("audit event already finished")
	}
	value := *a
	value.EventID, value.CreatedAt = t.record.EventID, t.record.CreatedAt
	value.Context.Outcome = "in_progress"
	if err := t.journal.writeIntent(&value); err != nil {
		t.journal.mu.Lock()
		t.journal.storageFailureLocked()
		t.journal.mu.Unlock()
		return ErrUnavailable
	}
	t.record = value
	return nil
}

func validID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(raw) == 16
}

func (t *Ticket) writeResult() error {
	frame, err := encodeFrame(&t.record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(t.journal.dir, t.record.EventID+".audit"), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.WriteAt(frame, frameSize); err != nil {
		return err
	}
	return f.Sync()
}

// Finish durably saves the result; DB failure leaves the file available for retry.
func (t *Ticket) Finish(a *db.AuditLog) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ready {
		return errors.New("audit event already finished")
	}
	id, created := t.record.EventID, t.record.CreatedAt
	t.record = *a
	t.record.EventID, t.record.CreatedAt = id, created
	if err := t.writeResult(); err != nil {
		t.journal.mu.Lock()
		t.journal.storageFailureLocked()
		t.journal.mu.Unlock()
		return ErrUnavailable // The durable intent remains recoverable as unknown.
	}
	t.ready = true
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = t.deliver(ctx)
	return nil
}

func (j *Journal) storageFailureLocked() {
	j.storageFailed = true
	j.status.StorageFailures++
	j.status.LastError = "storage_write_failed"
	j.log.Error("审计持久化失败，已停止接收新的审计操作；修复存储后重启服务")
}

// deliver is called with the ticket lock held; retries cannot race its result.
func (t *Ticket) deliver(ctx context.Context) error {
	j := t.journal
	err := j.sink.Create(ctx, &t.record)
	if err != nil {
		t.failures++
		delay := time.Second << min(t.failures-1, 5)
		t.next = time.Now().Add(delay)
		j.mu.Lock()
		j.status.DeliveryFailures++
		if !j.storageFailed {
			j.status.LastError = "database_write_failed"
		}
		j.mu.Unlock()
		if t.failures == 1 || t.failures%10 == 0 {
			j.log.Warn("审计落库失败，记录已持久化并等待重试", "event_id", t.record.EventID, "attempt", t.failures)
		}
		return err
	}
	if err = os.Remove(filepath.Join(j.dir, t.record.EventID+".audit")); err == nil {
		err = j.syncDir()
	}
	if err != nil {
		j.mu.Lock()
		j.storageFailureLocked()
		j.mu.Unlock()
		return err
	}
	t.delivered = true
	j.mu.Lock()
	delete(j.items, t.record.EventID)
	now := time.Now().UTC()
	j.status.LastDelivered = &now
	if len(j.items) == 0 && !j.storageFailed {
		j.status.LastError = ""
	}
	j.mu.Unlock()
	return nil
}

func (j *Journal) flush(ctx context.Context, force bool) error {
	j.mu.Lock()
	list := make([]*Ticket, 0, len(j.items))
	for _, t := range j.items {
		list = append(list, t)
	}
	j.mu.Unlock()
	var result error
	for _, t := range list {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !t.mu.TryLock() {
			continue
		}
		if t.ready && !t.delivered && (force || !time.Now().Before(t.next)) {
			attempt, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			if err := t.deliver(attempt); err != nil {
				result = err
			}
			cancel()
		}
		t.mu.Unlock()
	}
	return result
}

// Flush is also used for bounded graceful shutdown and recovery checks.
func (j *Journal) Flush(ctx context.Context) error { return j.flush(ctx, true) }

func (j *Journal) run(ctx context.Context) {
	defer close(j.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = j.flush(ctx, false)
		}
	}
}

func (j *Journal) Status() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.status
	s.Ready = !j.closed && !j.storageFailed && len(j.items) < j.capacity
	s.Pending, s.Capacity = len(j.items), j.capacity
	for _, t := range j.items {
		created := t.created
		if s.Oldest == nil || created.Before(*s.Oldest) {
			s.Oldest = &created
		}
	}
	return s
}

func (j *Journal) Close(ctx context.Context) error {
	j.mu.Lock()
	if j.closed {
		j.mu.Unlock()
		return nil
	}
	j.closed = true
	j.mu.Unlock()
	j.cancel()
	<-j.done
	err := j.Flush(ctx)
	_ = j.lock.Close()
	return err
}
