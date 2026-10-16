package queryapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/sqlparse"
	"github.com/user/dbhub/internal/target"
)

// Execute uses an independent connection unless the request explicitly names a transaction.
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in executeRequest
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if in.ConnectionID < 1 || strings.TrimSpace(in.SQL) == "" {
		httpx.Fail(w, httpx.BadRequest("connection_id 和 sql 必填"))
		return
	}
	conn, password, tunnel, e := h.loadOwnedConnection(r, in.ConnectionID)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	if conn.Type == "redis" {
		httpx.Fail(w, httpx.BadRequest("Redis 不支持 SQL"))
		return
	}
	if in.Database != "" {
		conn.Database = in.Database
	}
	query, args, e := sqlparse.BindNamed(in.SQL, conn.Type, in.Params)
	if e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	ctx, finish, e := h.jobs.admit(r.Context(), c.Subject, conn.ID, in.SQL)
	if e != nil {
		httpx.Fail(w, httpx.New(42900, "查询队列繁忙或等待已取消", e))
		return
	}
	var execErr error
	defer func() { finish(execErr) }()
	settings, settingErr := h.controls.Settings(ctx, c.Subject)
	if settingErr != nil {
		execErr = settingErr
		httpx.Fail(w, httpx.Internal("读取查询限制失败", settingErr))
		return
	}
	if in.TimeoutMS <= 0 || in.TimeoutMS > settings.TimeoutMS {
		in.TimeoutMS = settings.TimeoutMS
	}
	if in.MaxRows <= 0 || in.MaxRows > settings.MaxRows {
		in.MaxRows = settings.MaxRows
	}
	opt := target.ExecuteOptions{Timeout: time.Duration(in.TimeoutMS) * time.Millisecond, MaxRows: in.MaxRows, Args: args, AllowUnsafeWrite: c.Role == "admin" && !settings.BlockUnsafeWrite}
	run := func(handle *target.RelDB) (*target.ExecResult, error) {
		rewritten, values, e := h.applyPolicies(ctx, handle, c.Subject, c.Role, query, args)
		if e != nil {
			return nil, e
		}
		opt.Args = values
		return target.ExecuteWithOptions(ctx, handle, rewritten, c.Role == "readonly", opt)
	}
	var result *target.ExecResult
	if in.TransactionID == "" {
		var handle *target.RelDB
		handle, execErr = target.OpenRelational(ctx, conn, password, tunnel)
		if execErr == nil {
			defer handle.Close()
			result, execErr = run(handle)
		}
	} else if tx := h.txs.get(c.Subject, conn.ID); tx == nil || tx.ID != in.TransactionID {
		execErr = fmt.Errorf("事务不存在或已过期")
	} else {
		tx.mu.Lock()
		if h.txs.get(c.Subject, conn.ID) != tx {
			execErr = fmt.Errorf("事务不存在或已过期")
		} else if in.Database != "" && tx.Database != in.Database {
			execErr = fmt.Errorf("事务内不能切换数据库")
		} else if time.Since(tx.LastUsed) > 5*time.Minute {
			execErr = fmt.Errorf("事务已过期")
		} else {
			result, execErr = run(tx.Handle)
			tx.Queries++
			tx.LastUsed = time.Now()
		}
		if execErr != nil {
			h.txs.remove(tx)
			tx.close()
		}
		tx.mu.Unlock()
	}
	if execErr != nil {
		h.recordHistory(context.WithoutCancel(r.Context()), c, in, conn, 0, nil, execErr)
		httpx.Fail(w, httpx.BadRequest("执行失败: "+safeErr(execErr)))
		return
	}
	var affected *int64
	if result.Kind == "write" {
		n := result.AffectedRows
		affected = &n
	}
	h.recordHistory(context.WithoutCancel(r.Context()), c, in, conn, result.DurationMS, affected, nil)
	httpx.OK(w, result)
}
func (h *Handler) Close() { h.jobs.Close(); h.txs.Close() }
