package reportapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/sqlparse"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Handler struct {
	reports *db.ReportRepository
	conns   *db.ConnectionRepository
	log     *slog.Logger
}

func NewHandler(reports *db.ReportRepository, conns *db.ConnectionRepository, log *slog.Logger) *Handler {
	return &Handler{reports, conns, log}
}
func claimsOf(r *http.Request) *auth.Claims { c, _ := auth.ClaimsFromContext(r.Context()); return c }

type createRequest struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ConnectionID int64           `json:"connection_id"`
	Database     string          `json:"database"`
	SQL          string          `json:"sql"`
	ChartType    string          `json:"chart_type"`
	ChartConfig  json.RawMessage `json:"chart_config"`
	Visibility   string          `json:"visibility"`
	RequestID    string          `json:"request_id"`
}

var creationKeyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validateFields(rep *db.Report) error {
	rep.Name = strings.TrimSpace(rep.Name)
	rep.Description = strings.TrimSpace(rep.Description)
	rep.SQLText = strings.TrimSpace(rep.SQLText)
	rep.DatabaseName = strings.TrimSpace(rep.DatabaseName)
	for _, s := range []string{rep.Name, rep.Description, rep.SQLText, rep.DatabaseName} {
		if strings.ContainsRune(s, 0) {
			return httpx.BadRequest("报表字段不能包含空字符")
		}
	}
	if rep.Name == "" || utf8.RuneCountInString(rep.Name) > 128 {
		return httpx.BadRequest("报表名称必填，且不能超过128字")
	}
	if utf8.RuneCountInString(rep.Description) > 512 || utf8.RuneCountInString(rep.DatabaseName) > 128 {
		return httpx.BadRequest("描述不能超过512字，数据库名不能超过128字")
	}
	if rep.SQLText == "" || len(rep.SQLText) > 128*1024 {
		return httpx.BadRequest("SQL 必填，且不能超过128KB")
	}
	if rep.Visibility != "private" && rep.Visibility != "shared" {
		return httpx.BadRequest("可见性非法")
	}
	switch rep.ChartType {
	case "table", "bar", "line", "pie", "metric":
	default:
		return httpx.BadRequest("图表类型非法")
	}
	if rep.ChartConfig == nil {
		rep.ChartConfig = json.RawMessage(`{}`)
	}
	var cfg struct {
		Dimension string            `json:"dimension,omitempty"`
		Metrics   []string          `json:"metrics,omitempty"`
		Display   *db.ReportDisplay `json:"display,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(rep.ChartConfig))
	dec.DisallowUnknownFields()
	if len(rep.ChartConfig) > 16384 || dec.Decode(&cfg) != nil || string(rep.ChartConfig) == "null" {
		return httpx.BadRequest("图表配置只接受维度、指标和展示设置；请在 SQL 中完成聚合、排序与筛选")
	}
	if e := db.ValidateReportDisplay(cfg.Display); e != nil {
		return httpx.BadRequest(e.Error())
	}
	if len(cfg.Metrics) > 6 || utf8.RuneCountInString(cfg.Dimension) > 128 {
		return httpx.BadRequest("最多选择6个指标，字段名不能超过128字")
	}
	seen := map[string]bool{}
	for _, m := range cfg.Metrics {
		if strings.TrimSpace(m) == "" || utf8.RuneCountInString(m) > 128 || seen[m] {
			return httpx.BadRequest("指标字段不能为空、重复或超过128字")
		}
		seen[m] = true
	}
	if rep.ChartType != "table" && len(cfg.Metrics) == 0 {
		return httpx.BadRequest("请选择数值指标")
	}
	if (rep.ChartType == "bar" || rep.ChartType == "line" || rep.ChartType == "pie") && strings.TrimSpace(cfg.Dimension) == "" {
		return httpx.BadRequest("请选择维度字段")
	}
	if (rep.ChartType == "pie" || rep.ChartType == "metric") && len(cfg.Metrics) != 1 {
		return httpx.BadRequest("饼图和指标卡只接受一个指标")
	}
	rep.ChartConfig, _ = json.Marshal(cfg)
	return nil
}
func (h *Handler) validateConnection(r *http.Request, rep *db.Report) error {
	conn, err := h.conns.Get(r.Context(), rep.ConnectionID)
	if err != nil {
		return httpx.BadRequest("请选择本人的有效个人连接")
	}
	if err = h.conns.ValidateCurrent(r.Context(), conn); err != nil {
		return httpx.Forbidden("个人连接或数据源授权不可用，请重新配置")
	}
	caps := conn.SupportedCapabilities()
	if !caps.SQL || !caps.EnforcedReadOnly || conn.DefinitionRevisionID == nil {
		return httpx.BadRequest("该连接暂不支持报表所需的只读 SQL")
	}
	if err = sqlparse.ValidateReport(rep.SQLText, conn.SQLDialect()); err != nil {
		return httpx.BadRequest(err.Error())
	}
	rep.DefinitionRevisionID = *conn.DefinitionRevisionID
	return nil
}
func reportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("报表不存在或无权访问"))
	case errors.Is(err, db.ErrReportConflict):
		httpx.Fail(w, httpx.Conflict("报表已被修改，或创建编号已用于其他内容；请重新加载后核对"))
	default:
		httpx.Fail(w, httpx.Internal("报表操作失败", err))
	}
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var in createRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.ChartType == "" {
		in.ChartType = "table"
	}
	if in.Visibility == "" {
		in.Visibility = "private"
	}
	in.RequestID = strings.ToLower(in.RequestID)
	if !creationKeyPattern.MatchString(in.RequestID) || in.RequestID == "00000000-0000-0000-0000-000000000000" {
		httpx.Fail(w, httpx.BadRequest("创建编号必须为非空 UUID"))
		return
	}
	rep := &db.Report{Name: in.Name, Description: in.Description, ConnectionID: in.ConnectionID, DatabaseName: in.Database, SQLText: in.SQL, ChartType: in.ChartType, ChartConfig: in.ChartConfig, Visibility: in.Visibility, OwnerUserID: claimsOf(r).Subject, CreationKey: in.RequestID}
	if err := validateFields(rep); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.validateConnection(r, rep); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.reports.Create(r.Context(), rep); err != nil {
		reportError(w, err)
		return
	}
	saved, err := h.reports.Get(r.Context(), rep.ID)
	if err != nil {
		reportError(w, err)
		return
	}
	httpx.OK(w, saved)
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	page, size := 1, 20
	var err error
	if q.Has("page") {
		page, err = strconv.Atoi(q.Get("page"))
		if err != nil || page < 1 || page > 1000000 {
			httpx.Fail(w, httpx.BadRequest("page 非法"))
			return
		}
	}
	if q.Has("page_size") {
		size, err = strconv.Atoi(q.Get("page_size"))
		if err != nil || size < 1 || size > 100 {
			httpx.Fail(w, httpx.BadRequest("page_size 非法"))
			return
		}
	}
	f := db.ReportFilter{Keyword: strings.TrimSpace(q.Get("q")), Offset: (page - 1) * size, Limit: size}
	switch q.Get("scope") {
	case "mine":
		f.OwnerID = claimsOf(r).Subject
	case "starred":
		f.StarredBy = claimsOf(r).Subject
	case "shared":
		f.Visibility = "shared"
	case "", "all":
	default:
		httpx.Fail(w, httpx.BadRequest("scope 非法"))
		return
	}
	if q.Has("connection_id") {
		f.ConnectionID, err = strconv.ParseInt(q.Get("connection_id"), 10, 64)
		if err != nil || f.ConnectionID < 1 {
			httpx.Fail(w, httpx.BadRequest("connection_id 非法"))
			return
		}
	}
	items, total, err := h.reports.List(r.Context(), f)
	if err != nil {
		reportError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func reportID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || id < 1 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return 0, false
	}
	return id, true
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	rep, e := h.reports.Get(r.Context(), id)
	if e != nil {
		reportError(w, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.OK(w, rep)
}

type updateRequest struct {
	Name         *string         `json:"name"`
	Description  *string         `json:"description"`
	ConnectionID *int64          `json:"connection_id"`
	Database     *string         `json:"database"`
	SQL          *string         `json:"sql"`
	ChartType    *string         `json:"chart_type"`
	ChartConfig  json.RawMessage `json:"chart_config"`
	Visibility   *string         `json:"visibility"`
	Version      int64           `json:"version"`
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	rep, err := h.reports.Get(r.Context(), id)
	if err != nil {
		reportError(w, err)
		return
	}
	if rep.OwnerUserID != claimsOf(r).Subject {
		httpx.Fail(w, httpx.Forbidden("仅所有者可编辑报表"))
		return
	}
	var in updateRequest
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Version < 1 || in.Version != rep.Version {
		httpx.Fail(w, httpx.Conflict("报表版本已变化，请重新加载后核对"))
		return
	}
	definitionChanged := in.ConnectionID != nil || in.Database != nil || in.SQL != nil || in.ChartType != nil || in.ChartConfig != nil
	if in.Name != nil {
		rep.Name = *in.Name
	}
	if in.Description != nil {
		rep.Description = *in.Description
	}
	if in.ConnectionID != nil {
		rep.ConnectionID = *in.ConnectionID
	}
	if in.Database != nil {
		rep.DatabaseName = *in.Database
	}
	if in.SQL != nil {
		rep.SQLText = *in.SQL
	}
	if in.ChartType != nil {
		rep.ChartType = *in.ChartType
	}
	if in.ChartConfig != nil {
		rep.ChartConfig = in.ChartConfig
	}
	if in.Visibility != nil {
		rep.Visibility = *in.Visibility
	}
	if err = validateFields(rep); err != nil {
		httpx.Fail(w, err)
		return
	}
	if definitionChanged {
		if err = h.validateConnection(r, rep); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	if err = h.reports.Update(r.Context(), rep); err != nil {
		reportError(w, err)
		return
	}
	saved, err := h.reports.Get(r.Context(), rep.ID)
	if err != nil {
		reportError(w, err)
		return
	}
	httpx.OK(w, saved)
}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	if err := h.reports.Delete(r.Context(), id); err != nil {
		reportError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

type starRequest struct {
	Star bool `json:"star"`
}

func (h *Handler) Star(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in starRequest
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if e := h.reports.SetStar(r.Context(), id, claimsOf(r).Subject, in.Star); e != nil {
		reportError(w, e)
		return
	}
	httpx.OK(w, map[string]any{"starred": in.Star})
}
