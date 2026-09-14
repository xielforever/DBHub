package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/user/dbhub/internal/config"
)

// ErrUserNotFound 用户不存在。
var ErrUserNotFound = errors.New("用户不存在或已禁用")

// User 系统用户实体（对齐 sys_users 表）。
type User struct {
	ID           int64
	Username     string
	Role         string
	PasswordHash string
	IsActive     bool
	LastLoginAt  *time.Time
}

// UserStore 用户存储抽象，后续由 PostgreSQL Repository 实现替换。
type UserStore interface {
	FindByUsername(ctx context.Context, username string) (*User, error)
	TouchLogin(ctx context.Context, id int64) error
}

// MemoryStore 开发期内存用户存储，仅包含引导管理员。
// TODO: 接入 PostgreSQL 后替换为 db 层 Repository 实现。
type MemoryStore struct {
	mu    sync.RWMutex
	users map[string]*User
}

// NewMemoryStore 创建内存用户存储并初始化管理员账号。
func NewMemoryStore(admin config.AdminConfig) (*MemoryStore, error) {
	hash, err := HashPassword(admin.Password)
	if err != nil {
		return nil, err
	}
	s := &MemoryStore{users: make(map[string]*User)}
	s.users[admin.Username] = &User{
		ID:           1,
		Username:     admin.Username,
		Role:         "admin",
		PasswordHash: hash,
		IsActive:     true,
	}
	return s, nil
}

// FindByUsername 按用户名查找用户。
func (s *MemoryStore) FindByUsername(_ context.Context, username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[username]
	if !ok || !u.IsActive {
		return nil, ErrUserNotFound
	}
	// 返回副本，避免调用方修改存储内部状态
	copied := *u
	return &copied, nil
}

// TouchLogin 更新最后登录时间。
func (s *MemoryStore) TouchLogin(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.ID == id {
			now := time.Now()
			u.LastLoginAt = &now
			return nil
		}
	}
	return ErrUserNotFound
}
