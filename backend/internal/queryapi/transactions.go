package queryapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/target"
)

type transaction struct {
	mu                   sync.Mutex
	ID                   string
	UserID, ConnectionID int64
	Database             string
	Started, LastUsed    time.Time
	Queries              int
	Handle               *target.RelDB
	cancel               context.CancelFunc
}
type transactions struct {
	mu    sync.Mutex
	items map[string]*transaction
	stop  chan struct{}
	once  sync.Once
}

func newTransactions() *transactions {
	m := &transactions{items: map[string]*transaction{}, stop: make(chan struct{})}
	go m.reap()
	return m
}
func txKey(user, connection int64) string { return fmt.Sprintf("%d:%d", user, connection) }
func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (m *transactions) reap() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.mu.Lock()
			list := []*transaction{}
			for _, tx := range m.items {
				list = append(list, tx)
			}
			m.mu.Unlock()
			for _, tx := range list {
				if tx.mu.TryLock() {
					if time.Since(tx.LastUsed) > 5*time.Minute {
						m.remove(tx)
						tx.close()
					}
					tx.mu.Unlock()
				}
			}
		}
	}
}
func (m *transactions) remove(tx *transaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := txKey(tx.UserID, tx.ConnectionID)
	if m.items[key] == tx {
		delete(m.items, key)
	}
}
func (m *transactions) get(user, connection int64) *transaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items[txKey(user, connection)]
}
func (m *transactions) Close() {
	m.once.Do(func() {
		close(m.stop)
		m.mu.Lock()
		list := []*transaction{}
		for _, tx := range m.items {
			list = append(list, tx)
		}
		m.items = map[string]*transaction{}
		m.mu.Unlock()
		for _, tx := range list {
			tx.mu.Lock()
			tx.close()
			tx.mu.Unlock()
		}
	})
}

type transactionInput struct {
	ConnectionID int64  `json:"connection_id"`
	Database     string `json:"database"`
	ID           string `json:"transaction_id"`
}

func (h *Handler) BeginTransaction(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in transactionInput
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if in.ConnectionID < 1 {
		httpx.Fail(w, httpx.BadRequest("connection_id 无效"))
		return
	}
	if h.txs.get(c.Subject, in.ConnectionID) != nil {
		httpx.Fail(w, httpx.Conflict("该连接已有活动事务"))
		return
	}
	handle, _, _, e := h.openRelational(r, in.ConnectionID, in.Database)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	persistent, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	if e = handle.Begin(persistent, c.Role == "readonly"); e != nil {
		cancel()
		handle.Close()
		httpx.Fail(w, httpx.BadRequest("开启事务失败"))
		return
	}
	tx := &transaction{ID: randomID(), UserID: c.Subject, ConnectionID: in.ConnectionID, Database: handle.Conn.Database, Started: time.Now(), LastUsed: time.Now(), Handle: handle, cancel: cancel}
	h.txs.mu.Lock()
	count := 0
	for _, other := range h.txs.items {
		if other.UserID == c.Subject {
			count++
		}
	}
	if len(h.txs.items) >= 100 || count >= 4 || h.txs.items[txKey(c.Subject, in.ConnectionID)] != nil {
		h.txs.mu.Unlock()
		cancel()
		handle.Close()
		httpx.Fail(w, httpx.Conflict("活动事务数量达到上限"))
		return
	}
	h.txs.items[txKey(c.Subject, in.ConnectionID)] = tx
	h.txs.mu.Unlock()
	httpx.OK(w, map[string]any{"transaction_id": tx.ID, "status": "active", "connection_id": in.ConnectionID})
}
func (h *Handler) TransactionStatus(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	connection, _ := strconv.ParseInt(r.URL.Query().Get("connection_id"), 10, 64)
	tx := h.txs.get(c.Subject, connection)
	if tx == nil {
		httpx.OK(w, map[string]any{"active": false})
		return
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if time.Since(tx.LastUsed) > 5*time.Minute {
		h.txs.remove(tx)
		tx.close()
		httpx.OK(w, map[string]any{"active": false})
		return
	}
	httpx.OK(w, map[string]any{"active": true, "transaction_id": tx.ID, "started_at": tx.Started, "queries": tx.Queries})
}
func (h *Handler) FinishTransaction(commit bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := claimsOf(r)
		var in transactionInput
		if e := httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		tx := h.txs.get(c.Subject, in.ConnectionID)
		if tx == nil || in.ID == "" || tx.ID != in.ID {
			httpx.Fail(w, httpx.NotFound("事务不存在或不属于当前用户"))
			return
		}
		tx.mu.Lock()
		defer tx.mu.Unlock()
		h.txs.remove(tx)
		defer tx.close()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		var e error
		status := "rolled_back"
		field := "rolled_back"
		if commit {
			e = tx.Handle.Commit(ctx)
			status = "committed"
			field = "committed"
		} else {
			e = tx.Handle.Rollback(ctx)
		}
		if e != nil {
			httpx.Fail(w, httpx.BadRequest("事务结束失败，连接已关闭"))
			return
		}
		httpx.OK(w, map[string]any{"transaction_id": tx.ID, "status": status, field: true})
	}
}

func (tx *transaction) close() {
	tx.Handle.Close()
	if tx.cancel != nil {
		tx.cancel()
	}
}
