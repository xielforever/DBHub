package reportapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type ManagedHandler struct {
	repo     *db.ManagedReportRepository
	personal *Handler
}

func NewManagedHandler(repo *db.ManagedReportRepository, personal *Handler) *ManagedHandler {
	return &ManagedHandler{repo, personal}
}

// ManagedError deliberately keeps datasource/credential failures generic for a
// published viewer. Validation previews are available only to current admins.
func ManagedError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("报表不存在、版本已变化或无权访问"))
	case errors.Is(e, db.ErrManagedDenied):
		httpx.Fail(w, httpx.Forbidden("没有此报表操作权限，或相关账号与数据源已停用"))
	case errors.Is(e, db.ErrManagedConflict):
		httpx.Fail(w, httpx.Conflict(e.Error()))
	case errors.Is(e, db.ErrManagedInput):
		httpx.Fail(w, httpx.BadRequest(e.Error()))
	default:
		var app *httpx.AppError
		if errors.As(e, &app) {
			httpx.Fail(w, e)
		} else {
			httpx.Fail(w, httpx.Internal("正式报表操作失败", e))
		}
	}
}
func requestKey(key string) bool {
	return creationKeyPattern.MatchString(key) && key != "00000000-0000-0000-0000-000000000000"
}
func managedPage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	page, size := 1, 20
	var e error
	q := r.URL.Query()
	if q.Has("page") {
		page, e = strconv.Atoi(q.Get("page"))
		if e != nil || page < 1 || page > 1000000 {
			httpx.Fail(w, httpx.BadRequest("page 非法"))
			return 0, 0, false
		}
	}
	if q.Has("page_size") {
		size, e = strconv.Atoi(q.Get("page_size"))
		if e != nil || size < 1 || size > 100 {
			httpx.Fail(w, httpx.BadRequest("page_size 非法"))
			return 0, 0, false
		}
	}
	return page, size, true
}
func commandResponse(w http.ResponseWriter, result db.ReportCommandResult, e error) {
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, result)
}
func bindManaged(ctx context.Context, id, version int64) {
	auditctx.Update(ctx, func(d *auditctx.Data) { d.ReportID = id; d.ReportVersion = version; d.ReportMode = "managed" })
}

type managedDraftRequest struct {
	DataContract *db.DataContract `json:"data_contract"`
	RequestID    string           `json:"request_id"`
	Version      int64            `json:"version"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	ConnectionID int64            `json:"connection_id"`
	Database     string           `json:"database"`
	SQL          string           `json:"sql"`
	ChartType    string           `json:"chart_type"`
	ChartConfig  json.RawMessage  `json:"chart_config"`
}

func (h *ManagedHandler) definition(r *http.Request, in managedDraftRequest) (db.ReportDefinition, error) {
	rep := &db.Report{Name: in.Name, Description: in.Description, ConnectionID: in.ConnectionID, DatabaseName: in.Database, SQLText: in.SQL, ChartType: in.ChartType, ChartConfig: in.ChartConfig, Visibility: "private"}
	if rep.ChartType == "" {
		rep.ChartType = "table"
	}
	if e := validateFields(rep); e != nil {
		return db.ReportDefinition{}, e
	}
	if e := h.personal.validateConnection(r, rep); e != nil {
		return db.ReportDefinition{}, e
	}
	source, e := h.personal.conns.Definition(r.Context(), rep.DefinitionRevisionID)
	if e != nil {
		return db.ReportDefinition{}, e
	}
	if source.Visibility != "global" {
		return db.ReportDefinition{}, httpx.BadRequest("正式报表需要已发布的数据源，请先联系管理员发布")
	}
	if rep.DatabaseName == "" {
		rep.DatabaseName = source.Config.Database
	}
	if rep.DatabaseName != source.Config.Database {
		return db.ReportDefinition{}, httpx.BadRequest("正式报表的数据库必须与固定数据源修订一致")
	}
	if e := db.ValidateDataContract(in.DataContract, rep.ChartType); e != nil {
		return db.ReportDefinition{}, e
	}
	if e := db.ValidateContractMapping(in.DataContract, rep.ChartConfig); e != nil {
		return db.ReportDefinition{}, e
	}
	return db.ReportDefinition{DataContract: in.DataContract, Name: rep.Name, Description: rep.Description, DefinitionID: rep.DefinitionRevisionID, Database: rep.DatabaseName, SQL: rep.SQLText, ChartType: rep.ChartType, ChartConfig: rep.ChartConfig}, nil
}
func (h *ManagedHandler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	var in managedDraftRequest
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	in.RequestID = strings.ToLower(in.RequestID)
	if !requestKey(in.RequestID) {
		httpx.Fail(w, httpx.BadRequest("request_id 必须为非空 UUID"))
		return
	}
	var id int64
	if r.PathValue("id") != "" {
		var ok bool
		id, ok = reportID(w, r)
		if !ok {
			return
		}
		if in.Version < 1 {
			httpx.Fail(w, httpx.BadRequest("version 必填"))
			return
		}
	}
	definition, e := h.definition(r, in)
	if e != nil {
		ManagedError(w, e)
		return
	}
	bindManaged(r.Context(), id, in.Version)
	result, e := h.repo.SaveDraft(r.Context(), in.RequestID, id, in.Version, definition)
	if e == nil {
		bindManaged(r.Context(), result.ID, result.Version)
	}
	commandResponse(w, result, e)
}
func (h *ManagedHandler) List(w http.ResponseWriter, r *http.Request) {
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope != "" && scope != "mine" && scope != "all" && scope != "pending" {
		httpx.Fail(w, httpx.BadRequest("scope 非法"))
		return
	}
	if scope == "pending" && !db.ActorAdmin(r.Context()) {
		ManagedError(w, db.ErrManagedDenied)
		return
	}
	items, total, e := h.repo.List(r.Context(), scope == "all" || scope == "pending", scope == "pending", strings.TrimSpace(r.URL.Query().Get("q")), size, (page-1)*size)
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *ManagedHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	item, e := h.repo.Get(r.Context(), id)
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, item)
}
func (h *ManagedHandler) Versions(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	items, total, e := h.repo.Versions(r.Context(), id, size, (page-1)*size)
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *ManagedHandler) Submit(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
		Version   int64  `json:"version"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) || in.Version < 1 {
		httpx.Fail(w, httpx.BadRequest("request_id 和当前草稿 version 必填"))
		return
	}
	draft, e := h.repo.Get(r.Context(), id)
	if e != nil {
		ManagedError(w, e)
		return
	}
	d := draft.Draft
	// Revalidate the submitting developer's current personal connection. No
	// service identity can be used to develop or sneak new SQL into a submission.
	_, e = h.definition(r, managedDraftRequest{Name: d.Name, Description: d.Description, ConnectionID: draft.ConnectionID, Database: d.Database, SQL: d.SQL, ChartType: d.ChartType, ChartConfig: d.ChartConfig, DataContract: d.DataContract})
	if e != nil {
		ManagedError(w, e)
		return
	}
	bindManaged(r.Context(), id, in.Version)
	result, e := h.repo.Submit(r.Context(), in.RequestID, id, in.Version)
	commandResponse(w, result, e)
}
func (h *ManagedHandler) Decide(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID         string `json:"request_id"`
		Decision          string `json:"decision"`
		ValidationID      string `json:"validation_id"`
		ContractConfirmed bool   `json:"contract_confirmed"`
		Comment           string `json:"comment"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if !requestKey(in.RequestID) || utf8.RuneCountInString(in.Comment) > 1000 || strings.ContainsRune(in.Comment, 0) || (in.Decision != "approved" && in.Decision != "rejected" && in.Decision != "withdrawn") || in.Decision == "approved" && !requestKey(in.ValidationID) || in.Decision == "rejected" && in.Comment == "" {
		httpx.Fail(w, httpx.BadRequest("审批参数无效；批准须先验证，退回须填写原因"))
		return
	}
	auditctx.Update(r.Context(), func(d *auditctx.Data) {
		d.ReportMode = "managed"
		d.ReportReviewID = id
		d.ReportDecision = in.Decision
	})
	result, e := h.repo.Decide(r.Context(), in.RequestID, id, in.Decision, in.ValidationID, in.Comment, in.ContractConfirmed)
	commandResponse(w, result, e)
}
func (h *ManagedHandler) Release(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID          string `json:"request_id"`
		Expected           int64  `json:"expected_publication_no"`
		ExpectedDashboards int64  `json:"expected_affected_dashboards"`
		ReviewID           int64  `json:"review_id"`
		Kind               string `json:"kind"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) || in.Expected < 0 || (in.Kind != "publish" && in.Kind != "rollback" && in.Kind != "unpublish") || in.Kind != "unpublish" && in.ReviewID < 1 || in.Kind == "unpublish" && in.ReviewID != 0 {
		httpx.Fail(w, httpx.BadRequest("发布参数无效"))
		return
	}
	bindManaged(r.Context(), id, 0)
	auditctx.Update(r.Context(), func(d *auditctx.Data) {
		d.ReportReviewID = in.ReviewID
		d.ReportPublicationNo = in.Expected + 1
		d.ReportReleaseKind = in.Kind
	})
	result, e := h.repo.Release(r.Context(), in.RequestID, id, in.Expected, in.ReviewID, in.Kind, in.ExpectedDashboards)
	commandResponse(w, result, e)
}
func (h *ManagedHandler) Releases(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	items, total, e := h.repo.Releases(r.Context(), id, size, (page-1)*size)
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *ManagedHandler) Transfer(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID string `json:"request_id"`
		Version   int64  `json:"version"`
		OwnerID   int64  `json:"owner_id"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) || in.Version < 1 || in.OwnerID < 1 {
		httpx.Fail(w, httpx.BadRequest("移交参数无效"))
		return
	}
	bindManaged(r.Context(), id, in.Version)
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ReportOwnerID = in.OwnerID })
	result, e := h.repo.Transfer(r.Context(), in.RequestID, id, in.Version, in.OwnerID)
	commandResponse(w, result, e)
}
func (h *ManagedHandler) Published(w http.ResponseWriter, r *http.Request) {
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	items, total, e := h.repo.Published(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")), size, (page-1)*size)
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *ManagedHandler) PublishedGet(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	item, e := h.repo.PublishedGet(r.Context(), id)
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, item)
}
func (h *ManagedHandler) Accounts(w http.ResponseWriter, r *http.Request) {
	items, e := h.repo.Accounts(r.Context())
	if e != nil {
		ManagedError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, map[string]any{"items": items})
}
func (h *ManagedHandler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID    string `json:"request_id"`
		Name         string `json:"name"`
		DefinitionID int64  `json:"definition_revision_id"`
		Username     string `json:"username"`
		Password     string `json:"password"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Username = strings.TrimSpace(in.Username)
	if !requestKey(in.RequestID) || in.Name == "" || utf8.RuneCountInString(in.Name) > 100 || in.Username == "" || utf8.RuneCountInString(in.Username) > 128 || in.Password == "" || len(in.Password) > 4096 || in.DefinitionID < 1 || strings.ContainsRune(in.Name+in.Username+in.Password, 0) {
		httpx.Fail(w, httpx.BadRequest("账号名称、数据源修订、数据库用户名和密码必填且需在长度范围内"))
		return
	}
	source, e := h.personal.conns.Definition(r.Context(), in.DefinitionID)
	if e != nil {
		ManagedError(w, e)
		return
	}
	if source.Visibility != "global" || !source.Enabled {
		httpx.Fail(w, httpx.BadRequest("请选择启用的已发布数据源"))
		return
	}
	conn := source.Config.Connection()
	if conn.TemplateRevisionID != nil {
		conn.Template, e = h.personal.conns.Catalog().Template(r.Context(), *conn.TemplateRevisionID)
		if e != nil {
			ManagedError(w, e)
			return
		}
		if conn.Template.Visibility != "global" || conn.Template.Driver.Visibility != "global" {
			httpx.Fail(w, httpx.BadRequest("报表账号仅支持已发布的驱动规则"))
			return
		}
	}
	caps := conn.SupportedCapabilities()
	if !caps.SQL || !caps.EnforcedReadOnly {
		httpx.Fail(w, httpx.BadRequest("此数据源不支持强制只读报表"))
		return
	}
	result, e := h.repo.CreateAccount(r.Context(), in.RequestID, in.Name, in.DefinitionID, in.Username, in.Password)
	if e == nil {
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ReportAccountID = result.ID; d.DefinitionID = in.DefinitionID })
	}
	commandResponse(w, result, e)
}
func (h *ManagedHandler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID       string `json:"request_id"`
		Version         int64  `json:"version"`
		Enabled         *bool  `json:"enabled"`
		Password        string `json:"password"`
		ExpectedReports *int64 `json:"expected_reports"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if !requestKey(in.RequestID) || in.Version < 1 || len(in.Password) > 4096 || strings.ContainsRune(in.Password, 0) || in.Enabled == nil && in.Password == "" {
		httpx.Fail(w, httpx.BadRequest("账号修改参数无效"))
		return
	}
	auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ReportAccountID = id })
	result, e := h.repo.UpdateAccount(r.Context(), in.RequestID, id, in.Version, in.Enabled, in.Password, in.ExpectedReports)
	commandResponse(w, result, e)
}

func (h *ManagedHandler) DashboardImpact(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	review, e := strconv.ParseInt(r.URL.Query().Get("review_id"), 10, 64)
	if e != nil || review < 1 {
		httpx.Fail(w, httpx.BadRequest("请选择已批准版本"))
		return
	}
	out, e := h.repo.DashboardImpact(r.Context(), id, review)
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, out)
}

func (h *ManagedHandler) DashboardDependencies(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	page, size, ok := managedPage(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("scope")
	if kind == "" {
		kind = "current"
	}
	out, total, e := h.repo.DashboardDependencies(r.Context(), id, kind, size, (page-1)*size)
	if e != nil {
		ManagedError(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": out, "total": total, "page": page, "page_size": size})
}
