// Package datasource 提供数据源（目标数据库连接）与 SSH 隧道的 HTTP 处理器。
package datasource

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/sshx"
	"github.com/user/dbhub/internal/target"
)

// Handler 数据源模块处理器。
type Handler struct {
	conns   *db.ConnectionRepository
	tunnels *db.TunnelRepository
	log     *slog.Logger
}

// NewHandler 创建处理器。
func NewHandler(conns *db.ConnectionRepository, tunnels *db.TunnelRepository, log *slog.Logger) *Handler {
	return &Handler{conns: conns, tunnels: tunnels, log: log}
}

type connectionRequest struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Database          string `json:"database"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	SSHTunnelID       *int64 `json:"ssh_tunnel_id"`
	SSLMode           string `json:"ssl_mode"`
	ConnectionTimeout int    `json:"connection_timeout"`
	ColorLabel        string `json:"color_label"`
}

type tunnelRequest struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthType   string `json:"auth_type"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
	Password   string `json:"password"`
}

var allowedTypes = map[string]bool{"mysql": true, "postgres": true, "redis": true}

func validConnReq(in *connectionRequest) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	if in.Name == "" {
		return httpx.BadRequest("连接名称不能为空")
	}
	if !allowedTypes[in.Type] {
		return httpx.BadRequest("数据库类型仅支持 mysql / postgres / redis")
	}
	if in.Host == "" {
		return httpx.BadRequest("主机地址不能为空")
	}
	if in.Port <= 0 || in.Port > 65535 {
		return httpx.BadRequest("端口必须在 1-65535 之间")
	}
	if in.SSLMode == "" {
		in.SSLMode = "disable"
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
	// 数据源为团队共享资源：列表对所有登录用户可见，写操作在路由中间件鉴权。
	list, err := h.conns.List(r.Context(), dbType, keyword)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询连接列表失败", err))
		return
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
	if err := validConnReq(&in); err != nil {
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
		UserID: c.Subject, SSHTunnelID: in.SSHTunnelID,
		Name: in.Name, Type: in.Type, Host: in.Host, Port: in.Port,
		Database: in.Database, Username: in.Username, Password: in.Password,
		SSLMode: in.SSLMode, ConnectionTimeout: in.ConnectionTimeout, ColorLabel: in.ColorLabel,
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
	// 数据源为团队共享资源，所有登录用户均可查看元数据；口令不随详情返回。
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
	if err := validConnReq(&in); err != nil {
		httpx.Fail(w, err)
		return
	}
	existing, err := h.conns.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	if !isAdmin(c) && existing.UserID != c.Subject {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "连接不存在"})
		return
	}
	if in.SSHTunnelID != nil {
		if err := h.checkTunnelOwner(r.Context(), *in.SSHTunnelID, c); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	conn := &db.Connection{
		ID: id, UserID: existing.UserID, SSHTunnelID: in.SSHTunnelID,
		Name: in.Name, Type: in.Type, Host: in.Host, Port: in.Port,
		Database: in.Database, Username: in.Username, Password: in.Password,
		SSLMode: in.SSLMode, ConnectionTimeout: in.ConnectionTimeout, ColorLabel: in.ColorLabel,
	}
	if err := h.conns.Update(r.Context(), conn); err != nil {
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
	if !isAdmin(c) && existing.UserID != c.Subject {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "连接不存在"})
		return
	}
	if err := h.conns.Delete(r.Context(), id); err != nil {
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
	if err := validConnReq(&in); err != nil {
		httpx.Fail(w, err)
		return
	}
	conn := &db.Connection{
		UserID: c.Subject, SSHTunnelID: in.SSHTunnelID,
		Type: in.Type, Host: in.Host, Port: in.Port,
		Database: in.Database, Username: in.Username,
		SSLMode: in.SSLMode, ConnectionTimeout: in.ConnectionTimeout,
	}
	h.runTest(w, r, conn, in.Password)
}

// TestPersisted POST /api/v1/connections/{id}/test 使用已保存凭据测试。
func (h *Handler) TestPersisted(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	conn, password, err := h.conns.GetSecrets(r.Context(), id)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	// 共享数据源，所有登录用户均可发起连通性测试。
	h.runTest(w, r, conn, password)
}

func (h *Handler) runTest(w http.ResponseWriter, r *http.Request, conn *db.Connection, password string) {
	c := claimsOf(r)
	var tunnel *db.SSHTunnel
	if conn.SSHTunnelID != nil {
		t, err := h.tunnels.GetSecrets(r.Context(), *conn.SSHTunnelID)
		if err != nil {
			httpx.Fail(w, mapNotFound(err))
			return
		}
		if !isAdmin(c) && t.UserID != c.Subject {
			httpx.Fail(w, httpx.BadRequest("所选 SSH 隧道不可用"))
			return
		}
		tunnel = t
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	info, err := target.Test(ctx, conn, password, tunnel)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		h.log.Warn("数据源连接测试失败",
			"user_id", c.Subject, "type", conn.Type, "host", conn.Host, "elapsed_ms", duration, "error", err)
		httpx.Fail(w, httpx.BadRequest("连接测试失败: "+safeErr(err)))
		return
	}
	httpx.OK(w, map[string]any{
		"ok":         true,
		"type":       info.Type,
		"version":    info.Version,
		"elapsed_ms": duration,
	})
}

func (h *Handler) checkTunnelOwner(ctx context.Context, tunnelID int64, c *auth.Claims) error {
	t, err := h.tunnels.Get(ctx, tunnelID)
	if err != nil {
		return mapNotFound(err)
	}
	if !isAdmin(c) && t.UserID != c.Subject {
		return httpx.BadRequest("所选 SSH 隧道不存在")
	}
	return nil
}

// ---------- SSH 隧道 ----------

// ListTunnels GET /api/v1/ssh-tunnels
func (h *Handler) ListTunnels(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	list, err := h.tunnels.List(r.Context(), c.Subject, isAdmin(c))
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
		Username: in.Username, AuthType: in.AuthType,
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
	if !isAdmin(c) && existing.UserID != c.Subject {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "隧道不存在"})
		return
	}
	t := &db.SSHTunnel{
		ID: id, Name: in.Name, Host: in.Host, Port: in.Port,
		Username: in.Username, AuthType: in.AuthType,
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
	if !isAdmin(c) && existing.UserID != c.Subject {
		httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "隧道不存在"})
		return
	}
	if err := h.tunnels.Delete(r.Context(), id); err != nil {
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
		Host: in.Host, Port: in.Port, Username: in.Username, AuthType: in.AuthType,
		PrivateKey: in.PrivateKey, Passphrase: in.Passphrase, Password: in.Password,
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
	if errors.Is(err, db.ErrNotFound) {
		return &httpx.AppError{Code: httpx.CodeNotFound, Message: "记录不存在"}
	}
	return httpx.Internal("数据访问失败", err)
}

// safeErr 清洗驱动错误文案，避免 DSN/口令片段回显；仅保留首行。
func safeErr(err error) string {
	msg := strings.SplitN(err.Error(), "\n", 2)[0]
	for _, key := range []string{"password=", "passwd=", "pwd="} {
		if idx := strings.Index(strings.ToLower(msg), key); idx >= 0 {
			return "目标主机拒绝连接或认证失败"
		}
	}
	const max = 160
	if len(msg) > max {
		return msg[:max]
	}
	return msg
}
