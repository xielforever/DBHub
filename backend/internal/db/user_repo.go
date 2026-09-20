package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/user/dbhub/internal/auth"
)

// Role 角色实体（对齐 sys_roles 表）。
type Role struct {
	ID          int64     `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// UserDetail 用户列表/详情结构（绝不包含密码哈希）。
type UserDetail struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Email       string     `json:"email"`
	RoleID      int64      `json:"role_id"`
	Role        string     `json:"role"`
	RoleName    string     `json:"role_name"`
	IsActive    bool       `json:"is_active"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// UserRepository sys_users / sys_roles 仓储。
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository 创建用户仓储。
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// FindByUsername 按用户名查找（含密码哈希与角色编码），禁用/不存在统一返回 auth 层错误。
func (r *UserRepository) FindByUsername(ctx context.Context, username string) (*auth.User, error) {
	const q = `
		SELECT u.id, u.username, r.code, u.password_hash, u.is_active, u.last_login_at
		FROM sys_users u
		JOIN sys_roles r ON r.id = u.role_id
		WHERE u.username = $1`
	u := &auth.User{}
	err := r.pool.QueryRow(ctx, q, username).
		Scan(&u.ID, &u.Username, &u.Role, &u.PasswordHash, &u.IsActive, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	if !u.IsActive {
		return nil, auth.ErrUserNotFound
	}
	return u, nil
}

// TouchLogin 更新最后登录时间。
func (r *UserRepository) TouchLogin(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sys_users SET last_login_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("更新登录时间失败: %w", err)
	}
	return nil
}

// ListRoles 列出全部内置角色。
func (r *UserRepository) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, code, name, COALESCE(description, ''), created_at
		 FROM sys_roles ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询角色列表失败: %w", err)
	}
	defer rows.Close()

	roles := make([]Role, 0, 4)
	for rows.Next() {
		var role Role
		if err := rows.Scan(&role.ID, &role.Code, &role.Name, &role.Description, &role.CreatedAt); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

// ListUsers 分页拉取用户列表（不传 keyword 时不加过滤）。
func (r *UserRepository) ListUsers(ctx context.Context, keyword string, limit, offset int) ([]UserDetail, int64, error) {
	like := "%" + keyword + "%"
	var total int64
	countErr := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_users
		WHERE $1 = '' OR username ILIKE $1 OR COALESCE(email, '') ILIKE $1`, like).Scan(&total)
	if countErr != nil {
		return nil, 0, fmt.Errorf("统计用户失败: %w", countErr)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT u.id, u.username, COALESCE(u.email, ''), u.role_id, r.code, r.name,
		       u.is_active, u.last_login_at, u.created_at
		FROM sys_users u
		JOIN sys_roles r ON r.id = u.role_id
		WHERE $1 = '' OR u.username ILIKE $1 OR COALESCE(u.email, '') ILIKE $1
		ORDER BY u.id
		LIMIT $2 OFFSET $3`, like, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("查询用户列表失败: %w", err)
	}
	defer rows.Close()

	users := make([]UserDetail, 0, limit)
	for rows.Next() {
		var u UserDetail
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.RoleID, &u.Role,
			&u.RoleName, &u.IsActive, &u.LastLoginAt, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// GetByID 按主键取启用用户（标注 Owner 校验用）。
func (r *UserRepository) GetByID(ctx context.Context, id int64) (*BriefUser, error) {
	u := &BriefUser{}
	err := r.pool.QueryRow(ctx, `
		SELECT u.id, u.username, ro.code
		FROM sys_users u JOIN sys_roles ro ON ro.id = u.role_id
		WHERE u.id = $1 AND u.is_active = TRUE`, id).Scan(&u.ID, &u.Username, &u.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// BriefUser 资产 Owner 名簿的最小字段（全员可查，仅 id/用户名/角色）。
type BriefUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// ListBrief 全部启用用户的精简列表（资产 Owner 选择用）。
func (r *UserRepository) ListBrief(ctx context.Context) ([]BriefUser, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id, u.username, r.code
		FROM sys_users u JOIN sys_roles r ON r.id = u.role_id
		WHERE u.is_active = TRUE
		ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("查询用户简要列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]BriefUser, 0)
	for rows.Next() {
		var u BriefUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser 创建新用户，用户名重复时返回 ErrDuplicate。
func (r *UserRepository) CreateUser(ctx context.Context, username, passwordHash, roleCode, email string) (*UserDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_users WHERE username = $1)`, username).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrDuplicate
	}

	u := &UserDetail{}
	emailArg := nullableText(email)
	err = tx.QueryRow(ctx, `
		INSERT INTO sys_users (username, password_hash, email, role_id)
		SELECT $1, $2, $3, id FROM sys_roles WHERE code = $4
		RETURNING id, username, COALESCE(email, ''), role_id,
		          (SELECT code FROM sys_roles WHERE id = sys_users.role_id),
		          (SELECT name FROM sys_roles WHERE id = sys_users.role_id),
		          is_active, last_login_at, created_at`,
		username, passwordHash, emailArg, roleCode,
	).Scan(&u.ID, &u.Username, &u.Email, &u.RoleID, &u.Role, &u.RoleName,
		&u.IsActive, &u.LastLoginAt, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("插入用户失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUser 更新角色与邮箱。
func (r *UserRepository) UpdateUser(ctx context.Context, id int64, roleCode, email string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE sys_users
		SET role_id = (SELECT id FROM sys_roles WHERE code = $2),
		    email = $3
		WHERE id = $1`, id, roleCode, nullableText(email))
	if err != nil {
		return fmt.Errorf("更新用户失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetActive 启用/停用用户。
func (r *UserRepository) SetActive(ctx context.Context, id int64, active bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE sys_users SET is_active = $2 WHERE id = $1`, id, active)
	if err != nil {
		return fmt.Errorf("更新用户状态失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePassword 重置/修改密码哈希。
func (r *UserRepository) UpdatePassword(ctx context.Context, id int64, passwordHash string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE sys_users SET password_hash = $2 WHERE id = $1`, id, passwordHash)
	if err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser 删除用户（级联删除其连接、隧道、历史等）。
func (r *UserRepository) DeleteUser(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("删除用户失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAdmins 统计启用中的管理员数量，用于防止删除/降级最后一个管理员。
func (r *UserRepository) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sys_users u
		JOIN sys_roles r ON r.id = u.role_id
		WHERE r.code = 'admin' AND u.is_active = TRUE`).Scan(&n)
	return n, err
}

// EnsureAdmin 幂等引导管理员：不存在则创建，存在则不做任何修改。
func (r *UserRepository) EnsureAdmin(ctx context.Context, username, passwordHash string) error {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_users WHERE username = $1)`, username).Scan(&exists); err != nil {
		return fmt.Errorf("检查引导管理员失败: %w", err)
	}
	if exists {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sys_users (username, password_hash, role_id)
		SELECT $1, $2, id FROM sys_roles WHERE code = 'admin'`,
		username, passwordHash)
	if err != nil {
		return fmt.Errorf("创建引导管理员失败: %w", err)
	}
	return nil
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
