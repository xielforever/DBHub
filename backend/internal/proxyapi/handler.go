package proxyapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// Handler 代理管理占位（M5 正式实现，M4 仅占位）
// 设计：替代 SSH 隧道，支持 http/https/socks5/db_proxy/custom
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
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	isAdmin := c.Role == "admin"
	list, err := h.proxies.List(r.Context(), c.Subject, isAdmin)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询代理失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": list, "total": len(list), "note": "占位实现，M5 将支持真实代理拨号"})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	var in proxyRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Host) == "" || in.Port <= 0 {
		httpx.Fail(w, httpx.BadRequest("name/host/port 必填"))
		return
	}
	if in.Type == "" {
		in.Type = "http"
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
	var in proxyRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
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
	if err := h.proxies.Delete(r.Context(), id); err != nil {
		httpx.Fail(w, httpx.Internal("删除代理失败", err))
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	// 占位：M5 实现真实连通测试
	httpx.OK(w, map[string]any{"ok": true, "note": "占位，M5 将实现 HTTP/SOCKS5/DB Proxy 连通测试"})
}
