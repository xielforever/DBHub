package datasource

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/connector/jdbc"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

func catalogFailure(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, db.ErrSourceInput):
		httpx.Fail(w, httpx.BadRequest(e.Error()))
	case errors.Is(e, db.ErrSourceAccessDenied):
		httpx.Fail(w, httpx.Forbidden(e.Error()))
	case errors.Is(e, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("配置不存在或不可访问"))
	case errors.Is(e, db.ErrSourceVersionConflict), errors.Is(e, db.ErrCatalogConflict), errors.Is(e, db.ErrCatalogInUse), errors.Is(e, db.ErrPersonalBindingExists), errors.Is(e, db.ErrDefinitionImpactChanged):
		httpx.Fail(w, httpx.Conflict(e.Error()))
	case errors.Is(e, db.ErrCatalogDisabled), errors.Is(e, db.ErrDefinitionConfirmation):
		httpx.Fail(w, httpx.BadRequest(e.Error()))
	default:
		var ce *connector.Error
		if errors.As(e, &ce) {
			httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: ce.Message})
			return
		}
		httpx.Fail(w, httpx.Internal("连接器目录操作失败", e))
	}
}
func (h *Handler) available(ctx context.Context) ([]jdbc.Artifact, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return h.drivers.AvailableArtifacts(ctx)
}
func (h *Handler) AvailableDrivers(w http.ResponseWriter, r *http.Request) {
	items, e := h.available(r.Context())
	if e != nil {
		catalogFailure(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}
func (h *Handler) Drivers(w http.ResponseWriter, r *http.Request) {
	items, e := h.conns.Catalog().Drivers(r.Context())
	if e != nil {
		catalogFailure(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}
func (h *Handler) RegisterDriver(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ArtifactID string `json:"artifact_id"`
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 100 {
		httpx.Fail(w, httpx.BadRequest("驱动名称必填，最多 100 字符"))
		return
	}
	scope, e := scopeFor(r, in.Visibility)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	items, e := h.available(r.Context())
	if e != nil {
		catalogFailure(w, e)
		return
	}
	for _, a := range items {
		if a.ID != in.ArtifactID || !a.Ready {
			continue
		}
		d := &db.JDBCDriver{ArtifactID: a.ID, Name: in.Name, Version: a.Version, ClassName: a.ClassName, SHA256: a.SHA256, CreatedBy: claimsOf(r).Subject, Visibility: scope}
		if e = h.conns.Catalog().RegisterDriver(r.Context(), d); e != nil {
			catalogFailure(w, e)
			return
		}
		httpx.JSON(w, 201, httpx.Envelope{Code: 0, Message: "ok", Data: d})
		return
	}
	httpx.Fail(w, httpx.BadRequest("请选择 JDBC 服务已安装并校验的驱动版本"))
}
func (h *Handler) Templates(w http.ResponseWriter, r *http.Request) {
	items, e := h.conns.Catalog().Templates(r.Context(), isAdmin(claimsOf(r)))
	if e != nil {
		catalogFailure(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}

type templateRequest struct {
	Key         string             `json:"template_key"`
	Name        string             `json:"name"`
	Product     string             `json:"product"`
	DriverID    int64              `json:"driver_id"`
	Dialect     string             `json:"dialect"`
	URLTemplate string             `json:"url_template"`
	Fields      []db.TemplateField `json:"fields"`
	Visibility  string             `json:"visibility"`
}

func (h *Handler) PublishTemplate(w http.ResponseWriter, r *http.Request) {
	var in templateRequest
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	scope, e := scopeFor(r, in.Visibility)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	appendRevision := r.Method == http.MethodPut
	if appendRevision {
		id, e := idFromPath(r)
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		old, e := h.conns.Catalog().Template(r.Context(), id)
		if e != nil {
			catalogFailure(w, e)
			return
		}
		if old.Visibility == "global" && !isAdmin(claimsOf(r)) || old.Visibility == "private" && old.CreatedBy != claimsOf(r).Subject {
			httpx.Fail(w, httpx.NotFound("驱动配置不存在"))
			return
		}
		if old.Visibility != scope {
			httpx.Fail(w, httpx.BadRequest("修改可见范围请另建驱动配置"))
			return
		}
		if old.Key != in.Key {
			httpx.Fail(w, httpx.BadRequest("发布修订不能修改模板标识"))
			return
		}
	}
	d, e := h.conns.Catalog().Driver(r.Context(), in.DriverID)
	if e != nil {
		catalogFailure(w, e)
		return
	}
	if scope == "global" && d.Visibility != "global" {
		httpx.Fail(w, httpx.BadRequest("全局配置必须引用全局驱动"))
		return
	}
	if !d.Enabled {
		catalogFailure(w, db.ErrCatalogDisabled)
		return
	}
	t := &db.DatasourceTemplate{Key: in.Key, Name: strings.TrimSpace(in.Name), Product: strings.TrimSpace(in.Product), DriverID: d.ID, Driver: *d, Dialect: in.Dialect, URLTemplate: in.URLTemplate, Fields: in.Fields, CreatedBy: claimsOf(r).Subject, Visibility: scope}
	if e = connectorruntime.ValidateTemplate(t); e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	artifacts, e := h.available(r.Context())
	if e != nil {
		catalogFailure(w, e)
		return
	}
	ready := false
	for _, a := range artifacts {
		if a.ID == d.ArtifactID && a.SHA256 == d.SHA256 && a.ClassName == d.ClassName {
			ready = true
		}
	}
	if !ready {
		httpx.Fail(w, httpx.Conflict("驱动版本未在当前 JDBC 服务就绪"))
		return
	}
	if e = h.drivers.ValidateArtifactTemplate(r.Context(), t); e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	if e = h.conns.Catalog().Publish(r.Context(), t, appendRevision); e != nil {
		catalogFailure(w, e)
		return
	}
	httpx.JSON(w, 201, httpx.Envelope{Code: 0, Message: "ok", Data: t})
}
func (h *Handler) SetCatalogStatus(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, e := idFromPath(r)
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if e = httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		if in.Enabled == nil {
			httpx.Fail(w, httpx.BadRequest("enabled 必填"))
			return
		}
		if e = h.conns.Catalog().SetEnabled(r.Context(), kind, id, *in.Enabled); e != nil {
			catalogFailure(w, e)
			return
		}
		httpx.OK(w, map[string]any{"id": id, "enabled": *in.Enabled})
	}
}
func (h *Handler) DeleteCatalog(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, e := idFromPath(r)
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		if e = h.conns.Catalog().Delete(r.Context(), kind, id); e != nil {
			catalogFailure(w, e)
			return
		}
		httpx.OK(w, map[string]any{"id": id})
	}
}
func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	c, e := h.conns.Get(r.Context(), id)
	if e != nil {
		httpx.Fail(w, mapNotFound(e))
		return
	}
	httpx.OK(w, h.drivers.Capabilities(c))
}
func (h *Handler) prepareRequest(ctx context.Context, in *connectionRequest) error {
	if e := validConnReq(in); e != nil {
		return e
	}
	if in.Type != "jdbc" {
		if in.TemplateRevisionID != nil || len(in.Parameters) > 0 {
			return httpx.BadRequest("原生数据源不能绑定 JDBC 模板")
		}
		if e := connectorruntime.ValidateDestination(connectionOf(in, db.ActorID(ctx))); e != nil {
			return httpx.BadRequest(e.Error())
		}
		return nil
	}
	t, e := h.conns.Catalog().Template(ctx, *in.TemplateRevisionID)
	if e != nil {
		return httpx.BadRequest("数据源模板不存在")
	}
	if !t.Enabled || !t.Driver.Enabled {
		return httpx.BadRequest("驱动或模板已停用")
	}
	params, e := connectorruntime.NormalizeParameters(t, in.Parameters)
	if e != nil {
		return httpx.BadRequest(e.Error())
	}
	in.Parameters = params
	in.template = t
	in.Host = params["host"]
	if in.Host == "" {
		in.Host = "jdbc-memory"
	}
	in.Port = 0
	if p := params["port"]; p != "" {
		for _, ch := range p {
			in.Port = in.Port*10 + int(ch-'0')
		}
	}
	in.Database = connectorruntime.ProfileDatabase(params)
	if in.ProxyID != nil || in.SSHTunnelID != nil {
		return httpx.BadRequest("JDBC 首轮尚未支持代理或 SSH")
	}
	if (!connectorruntime.KnownJDBCClass(t.Driver.ClassName) || t.Driver.ClassName == "org.h2.Driver") && in.SSLMode != "disable" {
		return httpx.BadRequest("此模板的 TLS 覆盖尚未验证，请使用模板传输配置")
	}
	if e := connectorruntime.ValidateDestination(connectionOf(in, db.ActorID(ctx))); e != nil {
		return httpx.BadRequest(e.Error())
	}
	return nil
}
