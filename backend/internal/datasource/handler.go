// Package datasource 提供数据源（目标数据库连接）与 SSH 隧道的 HTTP 处理器。
package datasource

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/sshx"
)

// Handler 数据源模块处理器。
type Handler struct {
	conns   *db.ConnectionRepository
	tunnels *db.TunnelRepository
	log     *slog.Logger
	proxies *db.ProxyRepository
	drivers *target.Runtime
}

// NewHandler 创建处理器。
func NewHandler(conns *db.ConnectionRepository, tunnels *db.TunnelRepository, log *slog.Logger, proxies *db.ProxyRepository, runtimes ...*target.Runtime) *Handler {
	return &Handler{conns: conns, tunnels: tunnels, log: log, proxies: proxies, drivers: target.Default(runtimes)}
}

type connectionRequest struct {
	Name               string            `json:"name"`
	Type               string            `json:"type"`
	Host               string            `json:"host"`
	Port               int               `json:"port"`
	Database           string            `json:"database"`
	Username           string            `json:"username"`
	Password           string            `json:"password"`
	SSHTunnelID        *int64            `json:"ssh_tunnel_id"`
	ProxyID            *int64            `json:"proxy_id"`
	SSLMode            string            `json:"ssl_mode"`
	ConnectionTimeout  int               `json:"connection_timeout"`
	ColorLabel         string            `json:"color_label"`
	Environment        string            `json:"environment"`
	TemplateRevisionID *int64            `json:"template_revision_id,omitempty"`
	Parameters         map[string]string `json:"parameters,omitempty"`
	template           *db.DatasourceTemplate
}

type tunnelRequest struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthType   string `json:"auth_type"`
	HostKey    string `json:"host_key"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
	Password   string `json:"password"`
}

var allowedTypes = map[string]bool{"mysql": true, "postgres": true, "redis": true, "jdbc": true}

func validConnReq(in *connectionRequest) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	if in.Name == "" || len([]rune(in.Name)) > 100 {
		return httpx.BadRequest("连接名称必填，最多 100 字符")
	}
	if len(in.Username) > 100 || len(in.Password) > 4096 || len(in.Host) > 253 {
		return httpx.BadRequest("连接字段超出允许长度")
	}
	if in.ConnectionTimeout > 30 {
		return httpx.BadRequest("连接超时最多 30 秒")
	}
	if !allowedTypes[in.Type] {
		return httpx.BadRequest("请选择内置数据源类型或自定义 JDBC 模板")
	}
	if in.Type == "jdbc" {
		if in.TemplateRevisionID == nil || *in.TemplateRevisionID < 1 {
			return httpx.BadRequest("请选择已发布的 JDBC 数据源模板")
		}
	}
	if in.Host == "" && in.Type != "jdbc" {
		return httpx.BadRequest("主机地址不能为空")
	}
	if in.Type != "jdbc" && (in.Port <= 0 || in.Port > 65535) {
		return httpx.BadRequest("端口必须在 1-65535 之间")
	}
	if in.SSHTunnelID != nil && in.ProxyID != nil {
		return httpx.BadRequest("代理和 SSH 隧道不能同时启用")
	}
	if in.SSLMode != "" && in.SSLMode != "disable" && in.SSLMode != "require" && in.SSLMode != "verify-full" {
		return httpx.BadRequest("SSL 模式无效")
	}
	if in.SSLMode == "" {
		in.SSLMode = "verify-full"
	}
	if in.ProxyID != nil && in.SSHTunnelID != nil {
		return httpx.BadRequest("代理与 SSH 隧道不能同时启用")
	}
	if in.Environment == "" {
		in.Environment = "dev"
	}
	if in.Environment != "dev" && in.Environment != "test" && in.Environment != "prod" {
		return httpx.BadRequest("环境必须为 dev、test 或 prod")
	}
	if in.ConnectionTimeout <= 0 {
		in.ConnectionTimeout = 10
	}
	return nil
}

func validTunnelReq(in *tunnelRequest) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	if in.Name == "" || in.Host == "" || in.Username == "" {
		return httpx.BadRequest("隧道名称、主机、用户名不能为空")
	}
	if in.Port <= 0 || in.Port > 65535 {
		in.Port = 22
	}
	if in.AuthType != "password" && in.AuthType != "private_key" {
		return httpx.BadRequest("认证方式必须是 password 或 private_key")
	}
	return nil
}

func claimsOf(r *http.Request) *auth.Claims {
	c, _ := auth.ClaimsFromContext(r.Context())
	return c
}

func isAdmin(c *auth.Claims) bool { return c.Role == "admin" }

func idFromPath(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("ID 非法")
	}
	return id, nil
}

// ---------- 连接 ----------

// List GET /api/v1/connections
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	dbType := r.URL.Query().Get("type")
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	// 只返回当前用户的个人连接。
	list, err := h.conns.List(r.Context(), dbType, keyword)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询连接列表失败", err))
		return
	}
	for i := range list {
		list[i].Capabilities = h.drivers.Capabilities(&list[i])
	}
	httpx.OK(w, map[string]any{"items": list, "total": len(list)})
}

// Create POST /api/v1/connections
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in connectionRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Type != "redis" && (strings.TrimSpace(in.Username) == "" || in.Password == "") {
		httpx.Fail(w, httpx.BadRequest("请填写自己的数据库账号和密码"))
		return
	}
	if err := h.prepareRequest(r.Context(), &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.SSHTunnelID != nil {
		if err := h.checkTunnelOwner(r.Context(), *in.SSHTunnelID, c); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	conn := &db.Connection{
		UserID: c.Subject, SSHTunnelID: in.SSHTunnelID, ProxyID: in.ProxyID,
		Name: in.Name, Type: in.Type, Host: in.Host, Port: in.Port, TemplateRevisionID: in.TemplateRevisionID, Parameters: in.Parameters, Template: in.template,
		Database: in.Database, Username: in.Username, Password: in.Password,
		SSLMode: in.SSLMode, ConnectionTimeout: in.ConnectionTimeout, ColorLabel: in.ColorLabel,
		Environment: in.Environment,
	}
	if conn.ProxyID != nil {
		if _, e := h.proxyForUser(r.Context(), *conn.ProxyID, c); e != nil {
			httpx.Fail(w, e)
			return
		}
	}
	if err := h.conns.Create(r.Context(), conn); err != nil {
		httpx.Fail(w, httpx.Internal("创建连接失败", err))
		return
	}
	created, err := h.conns.Get(r.Context(), conn.ID)
	if err != nil {
		httpx.Fail(w, httpx.Internal("回读连接失败", err))
		return
	}
	h.log.Info("创建数据源", "user_id", c.Subject, "id", conn.ID, "name", conn.Name)
	httpx.JSON(w, http.StatusCreated, httpx.Envelope{Code: httpx.CodeOK, Message: "ok", Data: created})
}

// Get GET /api/v1/connections/{id}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	conn, err := h.conns.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	// 元数据与口令归属一致，管理员也不能代用其他用户的连接。
	conn.Capabilities = h.drivers.Capabilities(conn)
	httpx.OK(w, conn)
}

// Update PUT /api/v1/connections/{id}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var in connectionRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Type != "redis" && strings.TrimSpace(in.Username) == "" {
		httpx.Fail(w, httpx.BadRequest("请填写自己的数据库账号"))
		return
	}
	if err := h.prepareRequest(r.Context(), &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	existing, err := h.conns.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	if existing.UserID != c.Subject {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "连接不存在"})
		return
	}
	if existing.HasPassword && in.Password == "" && (!sameCredentialDestination(existing, &in) || existing.Database != in.Database) {
		httpx.Fail(w, httpx.BadRequest("连接目标或账号已变化，请重新填写个人密码"))
		return
	}
	if existing.Definition != nil && existing.Definition.Visibility == "global" {
		oldConfig, _ := json.Marshal(db.ConfigOf(existing))
		newConfig, _ := json.Marshal(db.ConfigOf(connectionOf(&in, c.Subject)))
		if string(oldConfig) != string(newConfig) {
			httpx.Fail(w, httpx.BadRequest("共享定义不可覆盖，请从目录确认并切换新修订"))
			return
		}
	}
	if in.SSHTunnelID != nil {
		if err := h.checkTunnelOwner(r.Context(), *in.SSHTunnelID, c); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	conn := &db.Connection{
		ID: id, UserID: existing.UserID, SSHTunnelID: in.SSHTunnelID, ProxyID: in.ProxyID,
		Name: in.Name, Type: in.Type, Host: in.Host, Port: in.Port, TemplateRevisionID: in.TemplateRevisionID, Parameters: in.Parameters, Template: in.template,
		Database: in.Database, Username: in.Username, Password: in.Password,
		SSLMode: in.SSLMode, ConnectionTimeout: in.ConnectionTimeout, ColorLabel: in.ColorLabel,
		Environment: in.Environment,
	}
	if conn.ProxyID != nil {
		if _, e := h.proxyForUser(r.Context(), *conn.ProxyID, c); e != nil {
			httpx.Fail(w, e)
			return
		}
	}
	if err := h.conns.Update(r.Context(), conn); err != nil {
		if errors.Is(err, db.ErrConnectionPolicyInUse) {
			httpx.Fail(w, httpx.Conflict(err.Error()))
			return
		}
		httpx.Fail(w, httpx.Internal("更新连接失败", err))
		return
	}
	updated, _ := h.conns.Get(r.Context(), id)
	httpx.OK(w, updated)
}

// Delete DELETE /api/v1/connections/{id}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	existing, err := h.conns.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	if existing.UserID != c.Subject {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "连接不存在"})
		return
	}
	if err := h.conns.Delete(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrConnectionPolicyInUse) {
			httpx.Fail(w, httpx.Conflict(err.Error()))
			return
		}
		httpx.Fail(w, httpx.Internal("删除连接失败", err))
		return
	}
	h.log.Info("删除数据源", "user_id", c.Subject, "id", id)
	httpx.OK(w, map[string]any{"id": id})
}

// TestUnpersisted POST /api/v1/connections/test 测试尚未保存的连接配置。
func (h *Handler) TestUnpersisted(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in connectionRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Type != "redis" && (strings.TrimSpace(in.Username) == "" || in.Password == "") {
		httpx.Fail(w, httpx.BadRequest("请填写自己的数据库账号和密码"))
		return
	}
	if err := h.prepareRequest(r.Context(), &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	conn := &db.Connection{
		UserID: c.Subject, SSHTunnelID: in.SSHTunnelID, ProxyID: in.ProxyID,
		Type: in.Type, Host: in.Host, Port: in.Port, TemplateRevisionID: in.TemplateRevisionID, Parameters: in.Parameters, Template: in.template,
		Database: in.Database, Username: in.Username,
		SSLMode: in.SSLMode, ConnectionTimeout: in.ConnectionTimeout,
	}
	h.runTest(w, r, conn, in.Password)
}

// TestPersisted 空请求体测试已保存配置；有请求体则测试编辑草稿，不修改配置或最近检查。
func (h *Handler) TestPersisted(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var draft *connectionRequest
	present, err := httpx.DecodeOptionalJSON(w, r, &draft)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if present && draft == nil {
		httpx.Fail(w, httpx.BadRequest("测试参数必须为 JSON 对象"))
		return
	}
	if present {
		if err := h.prepareRequest(r.Context(), draft); err != nil {
			httpx.Fail(w, err)
			return
		}
		c := claimsOf(r)
		conn, err := h.conns.Get(r.Context(), id)
		if err != nil {
			httpx.Fail(w, mapNotFound(err))
			return
		}
		if conn.UserID != c.Subject {
			httpx.Fail(w, httpx.Forbidden("无权测试该数据源的编辑配置"))
			return
		}
	}
	conn, password, err := h.conns.GetSecrets(r.Context(), id)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	if present {
		if draft.Password == "" && conn.HasPassword && !sameCredentialDestination(conn, draft) {
			httpx.Fail(w, httpx.BadRequest("连接地址、账号、TLS 或代理已修改，请重新输入口令后测试"))
			return
		}
		if draft.Password != "" {
			password = draft.Password
		}
		conn = &db.Connection{Type: draft.Type, Host: draft.Host, Port: draft.Port, Database: draft.Database,
			TemplateRevisionID: draft.TemplateRevisionID, Parameters: draft.Parameters, Template: draft.template, Username: draft.Username, SSLMode: draft.SSLMode, ConnectionTimeout: draft.ConnectionTimeout,
			SSHTunnelID: draft.SSHTunnelID, ProxyID: draft.ProxyID}
	}
	// 测试仅使用当前用户的身份。
	h.runTest(w, r, conn, password)
}

func sameCredentialDestination(conn *db.Connection, draft *connectionRequest) bool {
	sameID := func(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
	if conn.Type == "jdbc" || draft.Type == "jdbc" {
		old, _ := json.Marshal(conn.Parameters)
		next, _ := json.Marshal(draft.Parameters)
		if !sameID(conn.TemplateRevisionID, draft.TemplateRevisionID) || string(old) != string(next) {
			return false
		}
	}
	return conn.Type == draft.Type && conn.Host == draft.Host && conn.Port == draft.Port &&
		conn.Database == draft.Database && conn.Username == draft.Username && conn.SSLMode == draft.SSLMode &&
		sameID(conn.SSHTunnelID, draft.SSHTunnelID) && sameID(conn.ProxyID, draft.ProxyID)
}

func (h *Handler) runTest(w http.ResponseWriter, r *http.Request, conn *db.Connection, password string) {
	c := claimsOf(r)
	if conn.ProxyID != nil && conn.Proxy == nil {
		p, e := h.proxyForUser(r.Context(), *conn.ProxyID, c)
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		conn.Proxy = p
	}
	var tunnel *db.SSHTunnel
	if conn.SSHTunnelID != nil {
		t, err := h.tunnels.GetSecrets(r.Context(), *conn.SSHTunnelID)
		if err != nil {
			httpx.Fail(w, mapNotFound(err))
			return
		}
		if t.UserID != c.Subject {
			httpx.Fail(w, httpx.BadRequest("所选 SSH 隧道不可用"))
			return
		}
		tunnel = t
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	info, err := h.drivers.Test(ctx, conn, password, tunnel)
	duration := time.Since(start).Milliseconds()
	result := db.ConnectionCheck{OK: err == nil, Type: conn.Type, ElapsedMS: duration,
		StartedAt: start.UTC(), CheckedAt: time.Now().UTC()}
	if err != nil {
		diagnostic := target.DiagnoseConnectionError(err)
		result.Diagnostic = &diagnostic
		h.log.Warn("数据源连接测试失败",
			"user_id", c.Subject, "connection_id", conn.ID, "type", conn.Type, "elapsed_ms", duration, "error_code", diagnostic.Code)
		result.Error = "连接测试失败: " + diagnostic.Title
	} else {
		result.Version = info.Version
		result.Compatibility, _ = json.Marshal(info.Compatibility)
	}
	recorded := false
	if conn.ID > 0 {
		persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer persistCancel()
		var saveErr error
		recorded, saveErr = h.conns.SaveCheck(persistCtx, conn.ID, conn.UpdatedAt, &result)
		if saveErr != nil {
			httpx.Fail(w, httpx.Internal("保存连接检查失败", saveErr))
			return
		}
	}
	response := struct {
		db.ConnectionCheck
		Recorded bool `json:"recorded"`
	}{result, recorded}
	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, httpx.Envelope{Code: httpx.CodeBadRequest, Message: result.Error, Data: response})
		return
	}
	httpx.OK(w, response)
}

// CompatibilityMatrix 只提供已验证的版本记录，不连接目标数据库。
func (h *Handler) CompatibilityMatrix(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, map[string]any{"items": target.TestedVersions(), "scope": "基础 SQL、元数据和 Redis 单节点键值操作；不保证所有新语法或兼容产品"})
}

func (h *Handler) checkTunnelOwner(ctx context.Context, tunnelID int64, c *auth.Claims) error {
	t, err := h.tunnels.Get(ctx, tunnelID)
	if err != nil {
		return mapNotFound(err)
	}
	if t.UserID != c.Subject {
		return httpx.BadRequest("所选 SSH 隧道不存在")
	}
	return nil
}

// ---------- SSH 隧道 ----------

// ListTunnels GET /api/v1/ssh-tunnels
func (h *Handler) ListTunnels(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	list, err := h.tunnels.List(r.Context(), c.Subject, false)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询隧道列表失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": list, "total": len(list)})
}

// CreateTunnel POST /api/v1/ssh-tunnels
func (h *Handler) CreateTunnel(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in tunnelRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := validTunnelReq(&in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.AuthType == "private_key" && strings.TrimSpace(in.PrivateKey) == "" {
		httpx.Fail(w, httpx.BadRequest("私钥认证必须提供私钥内容"))
		return
	}
	if in.AuthType == "password" && in.Password == "" {
		httpx.Fail(w, httpx.BadRequest("密码认证必须提供跳板机密码"))
		return
	}
	t := &db.SSHTunnel{
		UserID: c.Subject, Name: in.Name, Host: in.Host, Port: in.Port,
		Username: in.Username, AuthType: in.AuthType, HostKey: in.HostKey,
		PrivateKey: in.PrivateKey, Passphrase: in.Passphrase, Password: in.Password,
	}
	if err := h.tunnels.Create(r.Context(), t); err != nil {
		httpx.Fail(w, httpx.Internal("创建隧道失败", err))
		return
	}
	created, err := h.tunnels.Get(r.Context(), t.ID)
	if err != nil {
		httpx.Fail(w, httpx.Internal("回读隧道失败", err))
		return
	}
	h.log.Info("创建 SSH 隧道", "user_id", c.Subject, "id", t.ID, "name", t.Name)
	httpx.JSON(w, http.StatusCreated, httpx.Envelope{Code: httpx.CodeOK, Message: "ok", Data: created})
}

// UpdateTunnel PUT /api/v1/ssh-tunnels/{id}
func (h *Handler) UpdateTunnel(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var in tunnelRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := validTunnelReq(&in); err != nil {
		httpx.Fail(w, err)
		return
	}
	existing, err := h.tunnels.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	if existing.UserID != c.Subject {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "隧道不存在"})
		return
	}
	t := &db.SSHTunnel{
		ID: id, Name: in.Name, Host: in.Host, Port: in.Port,
		Username: in.Username, AuthType: in.AuthType, HostKey: in.HostKey,
		PrivateKey: in.PrivateKey, Passphrase: in.Passphrase, Password: in.Password,
	}
	if err := h.tunnels.Update(r.Context(), t); err != nil {
		httpx.Fail(w, httpx.Internal("更新隧道失败", err))
		return
	}
	updated, _ := h.tunnels.Get(r.Context(), id)
	httpx.OK(w, updated)
}

// DeleteTunnel DELETE /api/v1/ssh-tunnels/{id}
func (h *Handler) DeleteTunnel(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	existing, err := h.tunnels.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	if existing.UserID != c.Subject {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "隧道不存在"})
		return
	}
	if err := h.tunnels.Delete(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNetworkInUse) {
			httpx.Fail(w, httpx.Conflict(err.Error()))
			return
		}
		httpx.Fail(w, httpx.Internal("删除隧道失败", err))
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

// TestTunnel POST /api/v1/ssh-tunnels/test 验证跳板登录（支持未保存配置）。
func (h *Handler) TestTunnel(w http.ResponseWriter, r *http.Request) {
	var in tunnelRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := validTunnelReq(&in); err != nil {
		httpx.Fail(w, err)
		return
	}
	t := &db.SSHTunnel{
		Host: in.Host, Port: in.Port, Username: in.Username, AuthType: in.AuthType, HostKey: in.HostKey,
		PrivateKey: in.PrivateKey, Passphrase: in.Passphrase, Password: in.Password,
	}
	if err := target.ValidateDestination(&db.Connection{Host: t.Host, Port: t.Port}); err != nil {
		httpx.Fail(w, httpx.BadRequest(err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, err := sshx.DialClient(ctx, t)
	if err != nil {
		httpx.Fail(w, httpx.BadRequest("SSH 连接失败: "+safeErr(err)))
		return
	}
	_ = client.Close()
	httpx.OK(w, map[string]any{"ok": true})
}

func mapNotFound(err error) error {
	if errors.Is(err, db.ErrCatalogDisabled) {
		return httpx.BadRequest("数据源定义、驱动或连接规则已停用")
	}
	if errors.Is(err, db.ErrConnectionChanged) {
		return httpx.Conflict(err.Error())
	}
	if errors.Is(err, db.ErrSourceAccessDenied) {
		return httpx.Forbidden(err.Error())
	}
	if errors.Is(err, db.ErrNotFound) {
		return &httpx.AppError{Code: httpx.CodeNotFound, Message: "记录不存在"}
	}
	return httpx.Internal("数据访问失败", err)
}

// safeErr exposes only platform-authored diagnostic text.
func safeErr(err error) string {
	return target.DiagnoseConnectionError(err).Title
}

func (h *Handler) proxyForUser(ctx context.Context, id int64, c *auth.Claims) (*db.Proxy, error) {
	if h.proxies == nil {
		return nil, httpx.BadRequest("代理服务不可用")
	}
	p, e := h.proxies.GetSecrets(ctx, id)
	if e != nil {
		return nil, httpx.NotFound("代理不存在")
	}
	if p.UserID != c.Subject {
		return nil, httpx.Forbidden("无权使用该代理")
	}
	return p, nil
}
