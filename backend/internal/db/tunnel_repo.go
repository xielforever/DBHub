package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/user/dbhub/pkg/crypto"
)

// SSHTunnel SSH 跳板配置。明文字段仅在写入与建连时短暂存在；
// 读取列表/详情时保持为空，通过 Has* 字段告知前端是否已配置。
type SSHTunnel struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	Name       string    `json:"name"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Username   string    `json:"username"`
	AuthType   string    `json:"auth_type"` // password | private_key
	PrivateKey string    `json:"private_key,omitempty"`
	Passphrase string    `json:"passphrase,omitempty"`
	Password   string    `json:"password,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// 仅输出：凭据是否已设置（绝不回传密文/明文）
	HasPrivateKey bool `json:"has_private_key"`
	HasPassphrase bool `json:"has_passphrase"`
	HasPassword   bool `json:"has_password"`
}

// TunnelRepository SSH 隧道仓储，敏感字段经 AES-GCM 加密落库。
type TunnelRepository struct {
	pool   *pgxpool.Pool
	cipher *crypto.Cipher
}

func NewTunnelRepository(pool *pgxpool.Pool, cipher *crypto.Cipher) *TunnelRepository {
	return &TunnelRepository{pool: pool, cipher: cipher}
}

const tunnelSelectCols = `
	id, user_id, name, host, port, username, auth_type,
	private_key IS NOT NULL AND private_key <> '',
	passphrase   IS NOT NULL AND passphrase   <> '',
	password     IS NOT NULL AND password     <> '',
	created_at, updated_at`

func scanTunnel(row pgx.Row) (*SSHTunnel, error) {
	t := &SSHTunnel{}
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Host, &t.Port, &t.Username, &t.AuthType,
		&t.HasPrivateKey, &t.HasPassphrase, &t.HasPassword, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// List 列出用户拥有的隧道（管理员可传 isAdmin=true 查看全部）。
func (r *TunnelRepository) List(ctx context.Context, userID int64, isAdmin bool) ([]SSHTunnel, error) {
	q := `SELECT ` + tunnelSelectCols + ` FROM sys_ssh_tunnels
		  WHERE ($2 OR user_id = $1) ORDER BY id DESC`
	rows, err := r.pool.Query(ctx, q, userID, isAdmin)
	if err != nil {
		return nil, fmt.Errorf("查询 SSH 隧道失败: %w", err)
	}
	defer rows.Close()
	out := make([]SSHTunnel, 0)
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Get 按 ID 取隧道元数据，ownership/admin 校验由调用方完成。
func (r *TunnelRepository) Get(ctx context.Context, id int64) (*SSHTunnel, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+tunnelSelectCols+` FROM sys_ssh_tunnels WHERE id = $1`, id)
	return scanTunnel(row)
}

// GetSecrets 取解密后的全部凭据（仅用于建立 SSH 连接，禁止进入响应体）。
func (r *TunnelRepository) GetSecrets(ctx context.Context, id int64) (*SSHTunnel, error) {
	t, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var encPrivateKey, encPassphrase, encPassword *string
	err = r.pool.QueryRow(ctx,
		`SELECT private_key, passphrase, password FROM sys_ssh_tunnels WHERE id = $1`, id).
		Scan(&encPrivateKey, &encPassphrase, &encPassword)
	if err != nil {
		return nil, err
	}
	if t.PrivateKey, err = r.decrypt(encPrivateKey); err != nil {
		return nil, err
	}
	if t.Passphrase, err = r.decrypt(encPassphrase); err != nil {
		return nil, err
	}
	if t.Password, err = r.decrypt(encPassword); err != nil {
		return nil, err
	}
	return t, nil
}

func (r *TunnelRepository) decrypt(enc *string) (string, error) {
	if enc == nil || *enc == "" {
		return "", nil
	}
	return r.cipher.Decrypt(*enc)
}

// Create 新建隧道。
func (r *TunnelRepository) Create(ctx context.Context, t *SSHTunnel) error {
	priv, err := r.cipher.Encrypt(t.PrivateKey)
	if err != nil {
		return err
	}
	pass, err := r.cipher.Encrypt(t.Passphrase)
	if err != nil {
		return err
	}
	pwd, err := r.cipher.Encrypt(t.Password)
	if err != nil {
		return err
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO sys_ssh_tunnels
			(user_id, name, host, port, username, auth_type, private_key, passphrase, password)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''))
		RETURNING id, created_at, updated_at`,
		t.UserID, t.Name, t.Host, t.Port, t.Username, t.AuthType, priv, pass, pwd,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

// Update 更新隧道；明文字段为空时保留原值（编辑表单不回显密码，留空=不修改）。
func (r *TunnelRepository) Update(ctx context.Context, t *SSHTunnel) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if t.AuthType == "private_key" && t.PrivateKey != "" {
		enc, err := r.cipher.Encrypt(t.PrivateKey)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sys_ssh_tunnels SET private_key = NULLIF($2,'') WHERE id = $1`, t.ID, enc); err != nil {
			return err
		}
	}
	if t.Passphrase != "" {
		enc, err := r.cipher.Encrypt(t.Passphrase)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sys_ssh_tunnels SET passphrase = NULLIF($2,'') WHERE id = $1`, t.ID, enc); err != nil {
			return err
		}
	}
	if t.AuthType == "password" && t.Password != "" {
		enc, err := r.cipher.Encrypt(t.Password)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sys_ssh_tunnels SET password = NULLIF($2,'') WHERE id = $1`, t.ID, enc); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE sys_ssh_tunnels
		SET name = $2, host = $3, port = $4, username = $5, auth_type = $6
		WHERE id = $1`,
		t.ID, t.Name, t.Host, t.Port, t.Username, t.AuthType)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// Delete 删除隧道（被连接引用时外键 SET NULL）。
func (r *TunnelRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_ssh_tunnels WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("删除 SSH 隧道失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
