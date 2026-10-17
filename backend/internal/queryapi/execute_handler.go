package queryapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/sqlparse"
)

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Execute uses an independent connection unless the request explicitly names a transaction.
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	h.metrics.beginRequest()
	observed := &metricsResponseWriter{ResponseWriter: w}
	w = observed
	var outcome string
	var reason error
	defer func() {
		if outcome == "" {
			outcome = requestOutcome(observed.status)
		}
		h.metrics.finishRequest(outcome, time.Since(started), errors.Is(reason, context.DeadlineExceeded), errors.Is(reason, context.Canceled))
	}()
	w.Header().Set("Cache-Control", "no-store")
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
	in.RequestID = strings.ToLower(in.RequestID)
	if !requestIDPattern.MatchString(in.RequestID) || in.RequestID == "00000000-0000-0000-0000-000000000000" {
		httpx.Fail(w, httpx.BadRequest("request_id 必须为非空 UUID；同一次请求重试必须沿用原编号"))
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
	query, args, e := sqlparse.BindNamed(in.SQL, conn.SQLDialect(), in.Params)
	if e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	if _, e = sqlparse.Parse(query, conn.SQLDialect()); e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	raw, e := json.Marshal(struct {
		Request executeRequest
		Role    string
	}{in, c.Role})
	if e != nil {
		httpx.Fail(w, httpx.BadRequest("请求参数无法编码"))
		return
	}
	hash := sha256.Sum256(raw)
	requestHash := hex.EncodeToString(hash[:])
	claimed, e := h.controls.ClaimExecution(r.Context(), c.Subject, in.RequestID, requestHash, conn.ID, conn.Database, in.TransactionID != "")
	if e != nil {
		h.log.Error("登记执行请求失败，未发送用户 SQL", "error", e)
		reason = e
		h.metrics.registrationError()
		httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "执行登记暂不可用，未发送 SQL；请先核实请求状态。"})
		return
	}
	if !claimed {
		receipt, err := h.controls.Execution(r.Context(), c.Subject, in.RequestID)
		if err != nil {
			httpx.Fail(w, httpx.Internal("读取执行状态失败", err))
		} else if receipt.RequestHash != requestHash {
			outcome = "conflict"
			httpx.Fail(w, httpx.Conflict("同一 request_id 不能用于不同的 SQL、参数、执行选项或角色"))
		} else {
			outcome = "duplicate"
			httpx.JSON(w, 409, httpx.Envelope{Code: 40901, Message: "该请求已登记，不会重复执行，请查看执行回执。", Data: receipt})
		}
		return
	}
	receipt := &db.QueryExecution{RequestID: in.RequestID, ConnectionID: conn.ID, Database: conn.Database, Transactional: in.TransactionID != ""}
	respond := func(status int, message string, data any) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer cancel()
		if err := h.controls.FinishExecution(cleanup, c.Subject, receipt); err != nil {
			h.metrics.receiptWriteError()
			outcome = "unknown"
			h.log.Error("保存执行回执失败", "request_id", in.RequestID, "error", err)
			receipt.Status = "unknown"
			receipt.Message = "执行回执暂不可用，请核实目标数据库；不要直接重跑写操作。"
			httpx.JSON(w, 503, httpx.Envelope{Code: 50301, Message: receipt.Message, Data: receipt})
			return
		}
		outcome = receipt.Status
		if status == 200 {
			httpx.OK(w, data)
		} else {
			httpx.JSON(w, status, httpx.Envelope{Code: status * 100, Message: message, Data: receipt})
		}
	}
	ctx, finish, e := h.jobs.admit(r.Context(), c.Subject, conn.ID, in.SQL)
	if e != nil {
		reason = e
		receipt.Status = "rejected"
		receipt.Message = "查询排队已拒绝、超时或取消，未发送 SQL。"
		respond(429, receipt.Message, nil)
		return
	}
	var execErr error
	defer func() { finish(execErr) }()
	settings, settingErr := h.controls.Settings(ctx, c.Subject)
	if settingErr != nil {
		execErr = settingErr
		reason = settingErr
		receipt.Status, receipt.Message = "rejected", "读取查询限制失败，未发送 SQL。"
		respond(503, receipt.Message, nil)
		return
	}
	if in.TimeoutMS <= 0 || in.TimeoutMS > settings.TimeoutMS {
		in.TimeoutMS = settings.TimeoutMS
	}
	if in.MaxRows <= 0 || in.MaxRows > settings.MaxRows {
		in.MaxRows = settings.MaxRows
	}
	opt := target.ExecuteOptions{Timeout: time.Duration(in.TimeoutMS) * time.Millisecond, MaxRows: in.MaxRows, Args: args, ExecutionKey: fmt.Sprintf("query:%d:%s", c.Subject, in.RequestID), AllowUnsafeWrite: c.Role == "admin" && !settings.BlockUnsafeWrite}
	sent := false
	var executionStarted time.Time
	opt.BeforeExecute = func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := h.controls.StartExecution(ctx, c.Subject, in.RequestID); err != nil {
			return fmt.Errorf("无法登记执行状态，未发送 SQL")
		}
		sent = true
		executionStarted = time.Now()
		h.metrics.beginExecution()
		return nil
	}
	run := func(handle *target.RelDB) (result *target.ExecResult, runErr error) {
		defer func() {
			if !sent {
				return
			}
			executionOutcome := "unknown"
			if runErr == nil && result != nil {
				executionOutcome = "succeeded"
			} else if runErr != nil && (c.Role == "readonly" || definiteStatementError(runErr)) {
				executionOutcome = "failed"
			}
			h.metrics.finishExecution(executionOutcome, time.Since(executionStarted))
		}()
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
		handle, execErr = h.drivers.OpenRelational(ctx, conn, password, tunnel)
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
		reason = execErr
		receipt.Status, receipt.Message = "unknown", "执行结果待确认；取消、超时或连接中断不代表写入已回滚，请先核实目标数据库。"
		if !sent {
			receipt.Status, receipt.Message = "rejected", "执行前检查未通过，未发送 SQL。"
		} else if c.Role == "readonly" || definiteStatementError(execErr) {
			receipt.Status, receipt.Message = "failed", "数据库已返回执行错误。"
		}
		message := "执行失败: " + safeErr(execErr)
		if receipt.Status == "unknown" {
			message = receipt.Message
		}
		respond(400, message, nil)
		h.recordHistory(context.WithoutCancel(r.Context()), c, in, conn, 0, nil, execErr)
		return
	}
	receipt.Status, receipt.Kind = "succeeded", result.Kind
	receipt.AffectedRows, receipt.DurationMS = result.AffectedRows, result.DurationMS
	respond(200, "", result)
	var affected *int64
	if result.Kind == "write" {
		n := result.AffectedRows
		affected = &n
	}
	h.recordHistory(context.WithoutCancel(r.Context()), c, in, conn, result.DurationMS, affected, nil)
}

// 仅把 PostgreSQL 明确的语句拒绝视为确定失败；通信、取消及提交不明保守处理。
func definiteStatementError(err error) bool { return target.DefiniteStatementError(err) }

// Execution 仅允许请求所属用户读取回执，管理员也不能跨用户取回。
func (h *Handler) Execution(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := strings.ToLower(r.PathValue("id"))
	if !requestIDPattern.MatchString(id) {
		httpx.Fail(w, httpx.BadRequest("执行编号格式错误"))
		return
	}
	receipt, err := h.controls.Execution(r.Context(), claimsOf(r).Subject, id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			httpx.Fail(w, httpx.NotFound("执行回执不存在；尚不能据此判断请求是否执行。"))
		} else {
			httpx.Fail(w, httpx.Internal("读取执行回执失败", err))
		}
		return
	}
	httpx.OK(w, receipt)
}
func (h *Handler) Close() { h.jobs.Close(); h.txs.Close() }
