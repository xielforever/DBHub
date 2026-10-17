package proxyapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/netx"
)

// Handler 管理代理及真实 CONNECT 连通测试。
type Handler struct {
	proxies *db.ProxyRepository
	log     *slog.Logger
}

func NewHandler(proxies *db.ProxyRepository, log *slog.Logger) *Handler {
	return &Handler{proxies: proxies, log: log}
}

type proxyRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // http|https|socks5|db_proxy|custom
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Description string `json:"description"`
	Status      string `json:"status"`
	ID          int64  `json:"id"`
	TargetHost  string `json:"target_host"`
	TargetPort  int    `json:"target_port"`
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	isAdmin := false
	list, err := h.proxies.List(r.Context(), c.Subject, isAdmin)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询代理失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": list, "total": len(list)})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	var in proxyRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := validate(in); err != nil {
		httpx.Fail(w, httpx.BadRequest(err.Error()))
		return
	}

	p := &db.Proxy{
		UserID:      c.Subject,
		Name:        strings.TrimSpace(in.Name),
		Type:        in.Type,
		Host:        strings.TrimSpace(in.Host),
		Port:        in.Port,
		Username:    strings.TrimSpace(in.Username),
		Password:    in.Password,
		Description: strings.TrimSpace(in.Description),
		Status:      "active",
	}
	if err := h.proxies.Create(r.Context(), p); err != nil {
		httpx.Fail(w, httpx.Internal("创建代理失败", err))
		return
	}
	httpx.OK(w, p)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	existing, err := h.owned(r, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var in proxyRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.Name == "" {
		in.Name = existing.Name
	}
	if in.Type == "" {
		in.Type = existing.Type
	}
	if in.Host == "" {
		in.Host = existing.Host
	}
	if in.Port == 0 {
		in.Port = existing.Port
	}
	if in.Status == "" {
		in.Status = existing.Status
	}
	if e := validate(in); e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	p := &db.Proxy{
		ID:          id,
		Name:        in.Name,
		Type:        in.Type,
		Host:        in.Host,
		Port:        in.Port,
		Username:    in.Username,
		Password:    in.Password,
		Description: in.Description,
		Status:      in.Status,
	}
	if err := h.proxies.Update(r.Context(), p); err != nil {
		httpx.Fail(w, httpx.Internal("更新代理失败", err))
		return
	}
	updated, _ := h.proxies.Get(r.Context(), id)
	httpx.OK(w, updated)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		httpx.Fail(w, httpx.BadRequest("ID 非法"))
		return
	}
	if _, err := h.owned(r, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.proxies.Delete(r.Context(), id); err != nil {
		httpx.Fail(w, httpx.Internal("删除代理失败", err))
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	var in proxyRequest
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	p := &db.Proxy{Type: in.Type, Host: in.Host, Port: in.Port, Username: in.Username, Password: in.Password, Status: "active"}
	if in.ID > 0 {
		existing, e := h.owned(r, in.ID)
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		_ = existing
		p, e = h.proxies.GetSecrets(r.Context(), in.ID)
		if e != nil {
			httpx.Fail(w, httpx.Internal("读取代理失败", e))
			return
		}
	}
	if p.Type != "db_proxy" && (in.TargetHost == "" || in.TargetPort < 1 || in.TargetPort > 65535) {
		httpx.Fail(w, httpx.BadRequest("请指定 target_host/target_port，以验证真实代理拨号"))
		return
	}
	destination := ""
	if in.TargetHost != "" {
		destination = net.JoinHostPort(in.TargetHost, strconv.Itoa(in.TargetPort))
	}
	allowedTarget := &db.Connection{Host: in.TargetHost, Port: in.TargetPort, Proxy: p}
	if p.Type == "db_proxy" {
		allowedTarget.Host = p.Host
		allowedTarget.Port = p.Port
	}
	if e := connectorruntime.ValidateDestination(allowedTarget); e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if e := netx.Test(ctx, p, destination); e != nil {
		httpx.Fail(w, httpx.BadRequest(e.Error()))
		return
	}
	httpx.OK(w, map[string]any{"ok": true, "latency_ms": time.Since(start).Milliseconds()})
}
func validate(in proxyRequest) error {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Host) == "" || in.Port < 1 || in.Port > 65535 {
		return errors.New("name/host/port 无效")
	}
	switch in.Type {
	case "http", "https", "socks5", "db_proxy":
	default:
		return errors.New("仅支持 HTTP/HTTPS CONNECT、SOCKS5、数据库代理网关")
	}
	if in.Status != "" && in.Status != "active" && in.Status != "inactive" {
		return errors.New("代理状态无效")
	}
	return nil
}
func (h *Handler) owned(r *http.Request, id int64) (*db.Proxy, error) {
	p, e := h.proxies.Get(r.Context(), id)
	if errors.Is(e, db.ErrNotFound) {
		return nil, httpx.NotFound("代理不存在")
	}
	if e != nil {
		return nil, httpx.Internal("读取代理失败", e)
	}
	c, _ := auth.ClaimsFromContext(r.Context())
	if p.UserID != c.Subject {
		return nil, httpx.Forbidden("只能修改自己的代理")
	}
	return p, nil
}
