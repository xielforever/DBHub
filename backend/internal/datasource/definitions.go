package datasource

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

func scopeFor(r *http.Request, value string) (string, error) {
	if value == "" {
		value = "private"
	}
	if value != "private" && value != "global" {
		return "", httpx.BadRequest("可见范围必须为 private 或 global")
	}
	if value == "global" && !isAdmin(claimsOf(r)) {
		return "", httpx.Forbidden("仅管理员可以全局发布")
	}
	return value, nil
}
func (h *Handler) Definitions(w http.ResponseWriter, r *http.Request) {
	filter, e := directoryPage(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	items, total, e := h.conns.SearchDefinitions(r.Context(), filter)
	if e != nil {
		httpx.Fail(w, httpx.Internal("加载数据源目录失败", e))
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total})
}

func (h *Handler) DefinitionRevisions(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	page, size := 1, 20
	for key, dest := range map[string]*int{"page": &page, "page_size": &size} {
		if value := r.URL.Query().Get(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || (key == "page_size" && n > 100) || (key == "page" && n > 1000000) {
				httpx.Fail(w, httpx.BadRequest("修订分页参数无效"))
				return
			}
			*dest = n
		}
	}
	items, total, e := h.conns.DefinitionRevisions(r.Context(), id, size, (page-1)*size)
	if e != nil {
		catalogFailure(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *Handler) validateDefinition(r *http.Request, d *db.SourceDefinition) error {
	d.Name = strings.TrimSpace(d.Name)
	d.Description = strings.TrimSpace(d.Description)
	if d.Name == "" || len([]rune(d.Name)) > 100 || len([]rune(d.Description)) > 1000 {
		return httpx.BadRequest("数据源名称或说明长度无效")
	}
	in := requestOf(d.Config.Connection())
	in.Name = d.Name
	if e := h.prepareRequest(r.Context(), &in); e != nil {
		return e
	}
	d.Config = db.ConfigOf(connectionOf(&in, claimsOf(r).Subject))
	if d.Visibility == "global" && in.Type != "redis" && strings.TrimSpace(in.Database) == "" {
		return httpx.BadRequest("全局定义需要明确数据库或服务名")
	}
	if in.template != nil && d.Visibility == "global" && (in.template.Visibility != "global" || in.template.Driver.Visibility != "global") {
		return httpx.BadRequest("全局数据源必须引用全局可用的驱动配置")
	}
	return nil
}
func (h *Handler) SaveDefinition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Visibility  string          `json:"visibility"`
		AccessMode  string          `json:"access_mode"`
		Config      db.SourceConfig `json:"config"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	scope, e := scopeFor(r, in.Visibility)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	previous := int64(0)
	if r.PathValue("id") != "" {
		previous, e = idFromPath(r)
		if e != nil {
			httpx.Fail(w, e)
			return
		}
	}
	d := &db.SourceDefinition{Name: in.Name, Description: in.Description, Visibility: scope, AccessMode: in.AccessMode, Config: in.Config}
	if e = h.validateDefinition(r, d); e != nil {
		httpx.Fail(w, e)
		return
	}
	if e = h.conns.SaveDefinition(r.Context(), d, previous); e != nil {
		catalogFailure(w, e)
		return
	}
	h.definitionAudit(r, d.ID, "revision_published")
	httpx.JSON(w, 201, httpx.Envelope{Code: 0, Message: "ok", Data: d})
}

// PublishConnection reads the owner's connection, then copies an explicit
// credential-free structure. No caller-supplied or saved authentication is copied.
func (h *Handler) PublishConnection(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	var in struct {
		Visibility  string `json:"visibility"`
		Description string `json:"description"`
	}
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	scope, e := scopeFor(r, in.Visibility)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	c, e := h.conns.Get(r.Context(), id)
	if e != nil {
		httpx.Fail(w, mapNotFound(e))
		return
	}
	if c.ProxyID != nil || c.SSHTunnelID != nil {
		httpx.Fail(w, httpx.BadRequest("发布定义暂不支持携带个人代理或 SSH，请先配置直连目标"))
		return
	}
	d := &db.SourceDefinition{Name: c.Name, Description: in.Description, Visibility: scope, Config: db.ConfigOf(c)}
	if e = h.validateDefinition(r, d); e != nil {
		httpx.Fail(w, e)
		return
	}
	previous := int64(0)
	if c.DefinitionRevisionID != nil {
		previous = *c.DefinitionRevisionID
	}
	if e = h.conns.SaveDefinition(r.Context(), d, previous); e != nil {
		catalogFailure(w, e)
		return
	}
	h.definitionAudit(r, d.ID, "revision_published")
	httpx.JSON(w, 201, httpx.Envelope{Code: 0, Message: "ok", Data: d})
}
func (h *Handler) DefinitionStatus(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	var in struct {
		Enabled          *bool  `json:"enabled"`
		Confirmed        bool   `json:"confirmed"`
		ExpectedBindings *int64 `json:"expected_binding_count"`
	}
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if in.Enabled == nil {
		httpx.Fail(w, httpx.BadRequest("enabled 必填"))
		return
	}
	if !*in.Enabled && !in.Confirmed {
		httpx.Fail(w, httpx.BadRequest(db.ErrDefinitionConfirmation.Error()))
		return
	}
	if e = h.conns.SetDefinitionEnabled(r.Context(), id, *in.Enabled, in.ExpectedBindings); e != nil {
		catalogFailure(w, e)
		return
	}
	h.definitionAudit(r, id, "revision_status_updated")
	httpx.OK(w, map[string]any{"id": id, "enabled": *in.Enabled})
}

type personalIdentity struct {
	Name                 string `json:"name"`
	Username             string `json:"username"`
	Password             string `json:"password"`
	Confirmed            bool   `json:"confirmed"`
	DefinitionRevisionID int64  `json:"definition_revision_id"`
}

func (h *Handler) personalCandidate(r *http.Request, id int64, in personalIdentity) (*db.Connection, error) {
	d, e := h.conns.Definition(r.Context(), id)
	if e != nil {
		return nil, mapNotFound(e)
	}
	if !d.Enabled {
		return nil, httpx.BadRequest("该数据源修订已停用")
	}
	if len(in.Username) > 100 || len(in.Password) > 4096 || len([]rune(in.Name)) > 100 {
		return nil, httpx.BadRequest("个人连接字段超出允许长度")
	}
	if !in.Confirmed {
		return nil, httpx.BadRequest("请确认数据源目标与修订后再配置个人身份")
	}
	if strings.TrimSpace(in.Username) == "" && d.Config.Type != "redis" {
		return nil, httpx.BadRequest("请填写自己的数据库账号")
	}
	if in.Password == "" && d.Config.Type != "redis" {
		return nil, httpx.BadRequest("请填写自己的数据库密码")
	}
	c := d.Config.Connection()
	c.UserID = claimsOf(r).Subject
	c.Name = strings.TrimSpace(in.Name)
	if c.Name == "" {
		c.Name = d.Name
	}
	c.Username = strings.TrimSpace(in.Username)
	c.Password = in.Password
	c.Definition = d
	c.DefinitionRevisionID = &d.ID
	c.DefinitionFamilyID = d.FamilyID
	draft := requestOf(c)
	if e = h.prepareRequest(r.Context(), &draft); e != nil {
		return nil, e
	}
	c.Template = draft.template
	return c, nil
}
func (h *Handler) AddPersonal(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	var in personalIdentity
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	c, e := h.personalCandidate(r, id, in)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	if r.URL.Query().Get("test") == "true" {
		h.runTest(w, r, c, c.Password)
		return
	}
	if e = h.conns.Create(r.Context(), c); e != nil {
		catalogFailure(w, e)
		return
	}
	saved, e := h.conns.Get(r.Context(), c.ID)
	if e != nil {
		httpx.Fail(w, mapNotFound(e))
		return
	}
	httpx.JSON(w, 201, httpx.Envelope{Code: 0, Message: "ok", Data: saved})
}
func (h *Handler) UpgradePersonal(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	old, e := h.conns.Get(r.Context(), id)
	if e != nil {
		httpx.Fail(w, mapNotFound(e))
		return
	}
	var in personalIdentity
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	c, e := h.personalCandidate(r, in.DefinitionRevisionID, in)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	if old.DefinitionFamilyID != c.DefinitionFamilyID {
		httpx.Fail(w, httpx.BadRequest("请选择同一数据源的新修订"))
		return
	}
	c.ID = old.ID
	c.ColorLabel = old.ColorLabel
	if e = h.conns.Update(r.Context(), c); e != nil {
		catalogFailure(w, e)
		return
	}
	saved, e := h.conns.Get(r.Context(), id)
	if e != nil {
		httpx.Fail(w, mapNotFound(e))
		return
	}
	httpx.OK(w, saved)
}

func requestOf(c *db.Connection) connectionRequest {
	return connectionRequest{Name: c.Name, Type: c.Type, Host: c.Host, Port: c.Port, Database: c.Database, Username: c.Username, Password: c.Password, SSLMode: c.SSLMode, ConnectionTimeout: c.ConnectionTimeout, Environment: c.Environment, TemplateRevisionID: c.TemplateRevisionID, Parameters: c.Parameters, ProxyID: c.ProxyID, SSHTunnelID: c.SSHTunnelID, ColorLabel: c.ColorLabel}
}
func connectionOf(in *connectionRequest, user int64) *db.Connection {
	return &db.Connection{UserID: user, Name: in.Name, Type: in.Type, Host: in.Host, Port: in.Port, Database: in.Database, Username: in.Username, Password: in.Password, SSLMode: in.SSLMode, ConnectionTimeout: in.ConnectionTimeout, Environment: in.Environment, TemplateRevisionID: in.TemplateRevisionID, Parameters: in.Parameters, Template: in.template, ProxyID: in.ProxyID, SSHTunnelID: in.SSHTunnelID, ColorLabel: in.ColorLabel}
}
