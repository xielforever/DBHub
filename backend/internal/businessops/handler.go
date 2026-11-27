package businessops

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/middleware"
	"github.com/user/dbhub/internal/reportapi"
)

type Handler struct {
	Repo    *db.BusinessOperationRepository
	Gateway Gateway
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func fail(w http.ResponseWriter, e error) {
	if errors.Is(e, db.ErrManagedDenied) {
		httpx.Fail(w, httpx.Forbidden("当前身份无权处理此业务操作，或模板与专用账号已停用"))
		return
	}
	if errors.Is(e, db.ErrNotFound) {
		httpx.Fail(w, httpx.NotFound("业务操作不存在或当前无权访问"))
		return
	}
	if errors.Is(e, db.ErrManagedConflict) {
		httpx.Fail(w, httpx.Conflict("业务操作状态、记录版本或授权已变化，请刷新核对"))
		return
	}
	if errors.Is(e, db.ErrManagedInput) {
		httpx.Fail(w, httpx.BadRequest(strings.TrimPrefix(e.Error(), db.ErrManagedInput.Error()+": ")))
		return
	}
	if errors.Is(e, db.ErrOperationUncertain) {
		httpx.Fail(w, httpx.Conflict(e.Error()))
		return
	}
	var target *TargetError
	if errors.As(e, &target) {
		httpx.Fail(w, httpx.Conflict("目标对象、权限或业务条件未通过核验，请核对分配范围及当前记录后重新预览"))
		return
	}
	if errors.Is(e, db.ErrAutomationLimit) {
		httpx.JSON(w, 429, httpx.Envelope{Code: 42900, Message: e.Error()})
		return
	}
	reportapi.ManagedError(w, e)
}
func id(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || value < 1 {
		httpx.Fail(w, httpx.BadRequest("对象编号无效"))
		return 0, false
	}
	return value, true
}
func (h *Handler) Register(mux httpx.RouteRegistrar, auth middleware.Middleware) {
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/business-operations/templates": h.Templates, "POST /api/v1/business-operations/templates": h.SaveTemplate, "PUT /api/v1/business-operations/templates/{id}": h.SaveTemplate,
		"GET /api/v1/business-operations/accounts": h.Accounts, "POST /api/v1/business-operations/accounts": h.SaveAccount, "PUT /api/v1/business-operations/accounts/{id}": h.SaveAccount,
		"POST /api/v1/business-operations/previews": h.Preview, "POST /api/v1/business-operations/requests": h.Submit, "GET /api/v1/business-operations/requests": h.Requests, "GET /api/v1/business-operations/requests/{id}": h.Request,
		"POST /api/v1/business-operations/requests/{id}/validate": h.Validate, "POST /api/v1/business-operations/requests/{id}/decision": h.Decide, "POST /api/v1/business-operations/requests/{id}/execute": h.Execute, "POST /api/v1/business-operations/requests/{id}/reconcile": h.Reconcile,
	}
	for route, fn := range routes {
		mux.Handle(route, middleware.Chain(fn, auth))
	}
}
func (h *Handler) Templates(w http.ResponseWriter, r *http.Request) {
	manage := r.URL.Query().Get("manage") == "true"
	items, e := h.Repo.Templates(r.Context(), manage)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items})
}
func (h *Handler) Accounts(w http.ResponseWriter, r *http.Request) {
	items, e := h.Repo.Accounts(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items})
}
func (h *Handler) SaveAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RequestID string              `json:"request_id"`
		Account   db.OperationAccount `json:"account"`
		Password  string              `json:"password"`
	}
	if e := httpx.DecodeJSON(w, r, &input); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !uuidPattern.MatchString(input.RequestID) || !uuidPattern.MatchString(input.Account.InstallationID) {
		httpx.Fail(w, httpx.BadRequest("请求编号和安装编号须为UUID"))
		return
	}
	a := input.Account
	if r.PathValue("id") != "" {
		v, ok := id(w, r)
		if !ok {
			return
		}
		if a.ID != v || a.Version < 1 {
			httpx.Fail(w, httpx.BadRequest("账号版本或编号无效"))
			return
		}
	}
	c, e := h.Repo.Source(r.Context(), a.DefinitionID, a.Username)
	if e != nil {
		fail(w, e)
		return
	}
	if a.Enabled {
		password := input.Password
		if password == "" && a.ID > 0 {
			password, e = h.Repo.AccountPassword(r.Context(), a.ID)
			if e != nil {
				fail(w, e)
				return
			}
		}
		if e = h.Gateway.Health(r.Context(), c, password, a.InstallationID); e != nil {
			fail(w, e)
			return
		}
	}
	out, e := h.Repo.SaveAccount(r.Context(), input.RequestID, a, input.Password)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) SaveTemplate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RequestID string               `json:"request_id"`
		Template  db.OperationTemplate `json:"template"`
	}
	if e := httpx.DecodeJSON(w, r, &input); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !uuidPattern.MatchString(input.RequestID) {
		httpx.Fail(w, httpx.BadRequest("请求编号须为UUID"))
		return
	}
	if r.PathValue("id") != "" {
		v, ok := id(w, r)
		if !ok {
			return
		}
		if input.Template.ID != v || input.Template.Version < 1 {
			httpx.Fail(w, httpx.BadRequest("模板编号或版本无效"))
			return
		}
	}
	out, e := h.Repo.SaveTemplate(r.Context(), input.RequestID, input.Template)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TemplateID int64  `json:"template_id"`
		Version    int64  `json:"version"`
		TargetKey  string `json:"target_key"`
		Amount     int    `json:"amount"`
	}
	if e := httpx.DecodeJSON(w, r, &input); e != nil {
		httpx.Fail(w, e)
		return
	}
	target, e := h.Repo.PreviewTarget(r.Context(), input.TemplateID, input.Version)
	if e != nil {
		fail(w, e)
		return
	}
	if e = db.ValidateOperationParameters(target.Template.Kind, input.TargetKey, input.Amount); e != nil {
		fail(w, e)
		return
	}
	p := db.OperationPreview{InstallationID: target.Account.InstallationID, WorkspaceID: db.WorkspaceID(r.Context()), ActorID: db.ActorID(r.Context()), Kind: target.Template.Kind, TargetKey: input.TargetKey, Amount: input.Amount, PreviewAt: time.Now().Unix()}
	p, e = h.Gateway.Preview(r.Context(), target, p)
	if e != nil {
		fail(w, e)
		return
	}
	out, e := h.Repo.StorePreview(r.Context(), target, p)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RequestID string `json:"request_id"`
		PreviewID string `json:"preview_id"`
		Reason    string `json:"reason"`
	}
	if e := httpx.DecodeJSON(w, r, &input); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !uuidPattern.MatchString(input.RequestID) || !uuidPattern.MatchString(input.PreviewID) {
		httpx.Fail(w, httpx.BadRequest("请求和预览编号须为UUID"))
		return
	}
	out, e := h.Repo.Submit(r.Context(), input.RequestID, input.PreviewID, input.Reason)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Requests(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		httpx.Fail(w, httpx.BadRequest("页码无效"))
		return
	}
	items, total, e := h.Repo.Requests(r.Context(), r.URL.Query().Get("all") == "true", 20, (page-1)*20)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total})
}
func (h *Handler) Request(w http.ResponseWriter, r *http.Request) {
	value, ok := id(w, r)
	if !ok {
		return
	}
	out, e := h.Repo.Request(r.Context(), value)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Validate(w http.ResponseWriter, r *http.Request) {
	value, ok := id(w, r)
	if !ok {
		return
	}
	var input struct {
		Version int64 `json:"version"`
	}
	if e := httpx.DecodeJSON(w, r, &input); e != nil {
		httpx.Fail(w, e)
		return
	}
	x, target, e := h.Repo.ReviewTarget(r.Context(), value, input.Version)
	if e != nil {
		fail(w, e)
		return
	}
	p, e := h.Gateway.Preview(r.Context(), target, x.Preview)
	if e != nil {
		fail(w, e)
		return
	}
	validation, e := h.Repo.StoreValidation(r.Context(), value, input.Version, p)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"validation_id": validation, "expires_in_seconds": 60, "preview": p})
}
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	value, ok := id(w, r)
	if !ok {
		return
	}
	var input struct {
		RequestID    string `json:"request_id"`
		Version      int64  `json:"version"`
		Decision     string `json:"decision"`
		ValidationID string `json:"validation_id"`
		Comment      string `json:"comment"`
		Confirmed    bool   `json:"confirmed"`
	}
	if e := httpx.DecodeJSON(w, r, &input); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !uuidPattern.MatchString(input.RequestID) || (input.Decision == "approved" && !uuidPattern.MatchString(input.ValidationID)) {
		httpx.Fail(w, httpx.BadRequest("请求或验证编号须为UUID"))
		return
	}
	out, e := h.Repo.Decide(r.Context(), input.RequestID, value, input.Version, input.Decision, input.ValidationID, input.Comment, input.Confirmed)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	value, ok := id(w, r)
	if !ok {
		return
	}
	var input struct {
		Version   int64 `json:"version"`
		Confirmed bool  `json:"confirmed"`
	}
	if e := httpx.DecodeJSON(w, r, &input); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !input.Confirmed {
		httpx.Fail(w, httpx.BadRequest("请明确确认本次执行"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	x, e := h.Repo.Claim(ctx, value, input.Version)
	if e != nil {
		fail(w, e)
		return
	}
	if x.Status == "executed" {
		httpx.OK(w, x)
		return
	}
	guard, e := h.Repo.ExecutionGuard(ctx, value, false)
	if e != nil {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = h.Repo.RecordUncertain(clean, value, true)
		fail(w, e)
		return
	}
	receipt, e := h.Gateway.Apply(ctx, guard.Target, guard.Request)
	if e == nil {
		e = guard.Finish(ctx, receipt)
	}
	guard.Close()
	clean, cleanCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanCancel()
	if e != nil {
		_ = h.Repo.RecordUncertain(clean, value, DefiniteFailure(e))
	}
	current, loadErr := h.Repo.Request(clean, value)
	if loadErr != nil {
		fail(w, db.ErrOperationUncertain)
		return
	}
	if current.Status == "stale" {
		fail(w, db.OperationFailure(e))
		return
	}
	httpx.OK(w, current)
}
func (h *Handler) Reconcile(w http.ResponseWriter, r *http.Request) {
	value, ok := id(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	x, e := h.Repo.Request(ctx, value)
	if e != nil {
		fail(w, e)
		return
	}
	if x.UserID != db.ActorID(ctx) {
		fail(w, db.ErrManagedDenied)
		return
	}
	if x.Status == "executed" {
		httpx.OK(w, x)
		return
	}
	guard, e := h.Repo.ExecutionGuard(ctx, value, true)
	if e != nil {
		fail(w, e)
		return
	}
	receipt, e := h.Gateway.Lookup(ctx, guard.Target, guard.Request)
	if e == nil && receipt != nil {
		e = guard.Finish(ctx, *receipt)
	}
	guard.Close()
	if e != nil {
		fail(w, e)
		return
	}
	if receipt == nil {
		_ = h.Repo.MissingReceipt(ctx, value)
	}
	current, e := h.Repo.Request(ctx, value)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, current)
}
