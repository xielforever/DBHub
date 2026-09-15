// Package adminapi 提供用户与角色（RBAC）管理接口，全部仅限 admin 角色。
package adminapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// Handler 用户管理处理器。
type Handler struct {
	users *db.UserRepository
	log   *slog.Logger
}

func NewHandler(users *db.UserRepository, log *slog.Logger) *Handler {
	return &Handler{users: users, log: log}
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Email    string `json:"email"`
}

type updateUserRequest struct {
	Role  string `json:"role"`
	Email string `json:"email"`
}

type statusRequest struct {
	IsActive bool `json:"is_active"`
}

type passwordRequest struct {
	NewPassword string `json:"new_password"`
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

var roleSet = map[string]bool{"admin": true, "developer": true, "readonly": true}

func claimsOf(r *http.Request) *auth.Claims {
	c, _ := auth.ClaimsFromContext(r.Context())
	return c
}

func idFromPath(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("ID 非法")
	}
	return id, nil
}

// Roles GET /api/v1/roles
func (h *Handler) Roles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.users.ListRoles(r.Context())
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询角色失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": roles})
}

// Users GET /api/v1/users
func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	pageSize := atoiDefault(q.Get("page_size"), 20)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	items, total, err := h.users.ListUsers(r.Context(), strings.TrimSpace(q.Get("keyword")), pageSize, (page-1)*pageSize)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询用户失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// CreateUser POST /api/v1/users
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in createUserRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if len(in.Username) < 3 {
		httpx.Fail(w, httpx.BadRequest("用户名至少 3 个字符"))
		return
	}
	if err := validatePassword(in.Password); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !roleSet[in.Role] {
		httpx.Fail(w, httpx.BadRequest("角色必须是 admin / developer / readonly"))
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		httpx.Fail(w, httpx.Internal("密码哈希失败", err))
		return
	}
	u, err := h.users.CreateUser(r.Context(), in.Username, hash, in.Role, strings.TrimSpace(in.Email))
	if err != nil {
		if errors.Is(err, db.ErrDuplicate) {
			httpx.Fail(w, httpx.Conflict("用户名已存在"))
			return
		}
		httpx.Fail(w, httpx.Internal("创建用户失败", err))
		return
	}
	h.log.Info("管理员创建用户", "operator_id", c.Subject, "new_user", in.Username, "role", in.Role)
	httpx.JSON(w, http.StatusCreated, httpx.Envelope{Code: httpx.CodeOK, Message: "ok", Data: u})
}

// UpdateUser PUT /api/v1/users/{id}
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var in updateUserRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if !roleSet[in.Role] {
		httpx.Fail(w, httpx.BadRequest("角色必须是 admin / developer / readonly"))
		return
	}
	if id == c.Subject && in.Role != "admin" {
		httpx.Fail(w, httpx.BadRequest("不能降低自己的管理员角色"))
		return
	}
	if in.Role != "admin" {
		if err := h.guardLastAdmin(r, id); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	if err := h.users.UpdateUser(r.Context(), id, in.Role, strings.TrimSpace(in.Email)); err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	h.log.Info("管理员更新用户", "operator_id", c.Subject, "target_id", id, "role", in.Role)
	httpx.OK(w, map[string]any{"id": id})
}

// SetUserStatus PATCH /api/v1/users/{id}/status
func (h *Handler) SetUserStatus(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var in statusRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if id == c.Subject && !in.IsActive {
		httpx.Fail(w, httpx.BadRequest("不能停用当前登录的自己"))
		return
	}
	if !in.IsActive {
		if err := h.guardLastAdmin(r, id); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	if err := h.users.SetActive(r.Context(), id, in.IsActive); err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	h.log.Info("管理员修改用户状态", "operator_id", c.Subject, "target_id", id, "active", in.IsActive)
	httpx.OK(w, map[string]any{"id": id, "is_active": in.IsActive})
}

// ResetPassword POST /api/v1/users/{id}/reset-password
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var in passwordRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := validatePassword(in.NewPassword); err != nil {
		httpx.Fail(w, err)
		return
	}
	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		httpx.Fail(w, httpx.Internal("密码哈希失败", err))
		return
	}
	if err := h.users.UpdatePassword(r.Context(), id, hash); err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}

// DeleteUser DELETE /api/v1/users/{id}
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	id, err := idFromPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if id == c.Subject {
		httpx.Fail(w, httpx.BadRequest("不能删除自己的账号"))
		return
	}
	if err := h.guardLastAdmin(r, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := h.users.DeleteUser(r.Context(), id); err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	h.log.Info("管理员删除用户", "operator_id", c.Subject, "target_id", id)
	httpx.OK(w, map[string]any{"id": id})
}

// guardLastAdmin 若目标是启用中的 admin，且降级/停用/删除后无启用 admin 则拒绝。
func (h *Handler) guardLastAdmin(r *http.Request, targetID int64) error {
	admins, err := h.users.CountAdmins(r.Context())
	if err != nil {
		return httpx.Internal("校验管理员数量失败", err)
	}
	if admins <= 1 {
		// 还需确认目标确实是启用管理员：查角色
		items, _, err := h.users.ListUsers(r.Context(), "", 1000, 0)
		if err != nil {
			return httpx.Internal("校验管理员数量失败", err)
		}
		for _, u := range items {
			if u.ID == targetID && u.Role == "admin" && u.IsActive {
				return httpx.BadRequest("系统必须保留至少一个启用的管理员")
			}
		}
	}
	return nil
}

// ChangePassword POST /api/v1/auth/change-password（任意登录用户修改自己的密码）
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in changePasswordRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	user, err := h.users.FindByUsername(r.Context(), c.Username)
	if err != nil {
		httpx.Fail(w, httpx.Unauthorized("用户不可用"))
		return
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, in.OldPassword)
	if err != nil || !ok {
		httpx.Fail(w, httpx.BadRequest("原密码不正确"))
		return
	}
	if err := validatePassword(in.NewPassword); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.NewPassword == in.OldPassword {
		httpx.Fail(w, httpx.BadRequest("新密码不能与原密码相同"))
		return
	}
	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		httpx.Fail(w, httpx.Internal("密码哈希失败", err))
		return
	}
	if err := h.users.UpdatePassword(r.Context(), c.Subject, hash); err != nil {
		httpx.Fail(w, httpx.Internal("修改密码失败", err))
		return
	}
	httpx.OK(w, map[string]any{"ok": true})
}

func validatePassword(pw string) error {
	if len(pw) < 8 {
		return httpx.BadRequest("密码至少 8 个字符")
	}
	if len(pw) > 128 {
		return httpx.BadRequest("密码长度不能超过 128 个字符")
	}
	return nil
}

func atoiDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func mapNotFound(err error) error {
	if errors.Is(err, db.ErrNotFound) {
		return &httpx.AppError{Code: httpx.CodeNotFound, Message: "用户不存在"}
	}
	return httpx.Internal("数据访问失败", err)
}
