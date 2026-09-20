package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/pkg/crypto"
)

// Proxy 代理配置（占位，替代 SSH 隧道）
type Proxy struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"` // http|https|socks5|db_proxy|custom
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Username    string    `json:"username,omitempty"`
	HasPassword bool      `json:"has_password,omitempty"`
	Password    string    `json:"-"` // 解密后仅内部使用
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const proxySelectCols = `id, user_id, name, type, host, port, COALESCE(username,''), COALESCE(password,''), COALESCE(description,''), status, created_at, updated_at`

type ProxyRepository struct {
	pool   *pgxpool.Pool
	cipher *crypto.Cipher
}

func NewProxyRepository(pool *pgxpool.Pool, cipher *crypto.Cipher) *ProxyRepository {
	return &ProxyRepository{pool: pool, cipher: cipher}
}

func (r *ProxyRepository) List(ctx context.Context, userID int64, isAdmin bool) ([]Proxy, error) {
	q := `SELECT ` + proxySelectCols + ` FROM sys_proxies`
	args := []any{}
	if !isAdmin {
		q += ` WHERE user_id = $1`
		args = append(args, userID)
	}
	q += ` ORDER BY updated_at DESC`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询代理失败: %w", err)
	}
	defer rows.Close()
	out := []Proxy{}
	for rows.Next() {
		var p Proxy
		var encPwd string
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Type, &p.Host, &p.Port, &p.Username, &encPwd, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.HasPassword = encPwd != ""
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *ProxyRepository) Get(ctx context.Context, id int64) (*Proxy, error) {
	var p Proxy
	var encPwd string
	err := r.pool.QueryRow(ctx, `SELECT `+proxySelectCols+` FROM sys_proxies WHERE id=$1`, id).Scan(
		&p.ID, &p.UserID, &p.Name, &p.Type, &p.Host, &p.Port, &p.Username, &encPwd, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.HasPassword = encPwd != ""
	return &p, nil
}

func (r *ProxyRepository) GetSecrets(ctx context.Context, id int64) (*Proxy, error) {
	p, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var encPwd string
	if err := r.pool.QueryRow(ctx, `SELECT password FROM sys_proxies WHERE id=$1`, id).Scan(&encPwd); err != nil {
		return nil, err
	}
	if encPwd != "" && r.cipher != nil {
		if dec, err := r.cipher.Decrypt(encPwd); err == nil {
			p.Password = dec
		}
	}
	return p, nil
}

func (r *ProxyRepository) Create(ctx context.Context, p *Proxy) error {
	encPwd := ""
	if p.Password != "" && r.cipher != nil {
		enc, err := r.cipher.Encrypt(p.Password)
		if err != nil {
			return err
		}
		encPwd = enc
	}
	if p.Type == "" {
		p.Type = "http"
	}
	if p.Status == "" {
		p.Status = "active"
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO sys_proxies (user_id, name, type, host, port, username, password, description, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at, updated_at`,
		p.UserID, strings.TrimSpace(p.Name), p.Type, strings.TrimSpace(p.Host), p.Port, strings.TrimSpace(p.Username), encPwd, strings.TrimSpace(p.Description), p.Status,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *ProxyRepository) Update(ctx context.Context, p *Proxy) error {
	fields := []string{}
	args := []any{p.ID}
	add := func(expr string, v any) {
		args = append(args, v)
		fields = append(fields, fmt.Sprintf(expr, len(args)))
	}
	if p.Name != "" {
		add("name=$%d", strings.TrimSpace(p.Name))
	}
	if p.Type != "" {
		add("type=$%d", p.Type)
	}
	if p.Host != "" {
		add("host=$%d", strings.TrimSpace(p.Host))
	}
	if p.Port > 0 {
		add("port=$%d", p.Port)
	}
	if p.Username != "" || p.Username == "" {
		// 允许清空
		add("username=$%d", strings.TrimSpace(p.Username))
	}
	if p.Password != "" && r.cipher != nil {
		enc, err := r.cipher.Encrypt(p.Password)
		if err != nil {
			return err
		}
		add("password=$%d", enc)
	}
	if p.Description != "" || true {
		add("description=$%d", strings.TrimSpace(p.Description))
	}
	if p.Status != "" {
		add("status=$%d", p.Status)
	}
	if len(fields) == 0 {
		return nil
	}
	q := `UPDATE sys_proxies SET ` + strings.Join(fields, ", ") + ` WHERE id=$1`
	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ProxyRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_proxies WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// 清理 connections 引用
	_, _ = r.pool.Exec(ctx, `UPDATE sys_connections SET proxy_id=NULL WHERE proxy_id=$1`, id)
	return nil
}
