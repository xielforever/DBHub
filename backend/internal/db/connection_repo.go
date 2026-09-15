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

// Connection 目标数据库连接配置。Password 仅写入/建连时短暂存在，
// 列表与详情接口只返回 HasPassword。
type Connection struct {
	ID                int64     `json:"id"`
	UserID            int64     `json:"user_id"`
	SSHTunnelID       *int64    `json:"ssh_tunnel_id"`
	Name              string    `json:"name"`
	Type              string    `json:"type"` // mysql | postgres | redis | mongo
	Host              string    `json:"host"`
	Port              int       `json:"port"`
	Database          string    `json:"database"`
	Username          string    `json:"username"`
	Password          string    `json:"password,omitempty"`
	SSLMode           string    `json:"ssl_mode"`
	ConnectionTimeout int       `json:"connection_timeout"`
	ColorLabel        string    `json:"color_label"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`

	HasPassword bool `json:"has_password"`

	// 关联隧道摘要（列表联表带）
	TunnelName string `json:"tunnel_name,omitempty"`
}

// ConnectionRepository 连接仓储，口令 AES-GCM 加密落库。
type ConnectionRepository struct {
	pool   *pgxpool.Pool
	cipher *crypto.Cipher
}

func NewConnectionRepository(pool *pgxpool.Pool, cipher *crypto.Cipher) *ConnectionRepository {
	return &ConnectionRepository{pool: pool, cipher: cipher}
}

const connSelectCols = `
	c.id, c.user_id, c.ssh_tunnel_id, c.name, c.type, c.host, c.port,
	COALESCE(c.database, ''), COALESCE(c.username, ''),
	c.password IS NOT NULL AND c.password <> '',
	c.ssl_mode, c.connection_timeout, COALESCE(c.color_label, ''),
	c.created_at, c.updated_at, COALESCE(t.name, '')`

func scanConnection(row pgx.Row) (*Connection, error) {
	c := &Connection{}
	err := row.Scan(&c.ID, &c.UserID, &c.SSHTunnelID, &c.Name, &c.Type, &c.Host, &c.Port,
		&c.Database, &c.Username, &c.HasPassword, &c.SSLMode, &c.ConnectionTimeout,
		&c.ColorLabel, &c.CreatedAt, &c.UpdatedAt, &c.TunnelName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// List 连接列表，可按类型/关键字过滤；管理员可查看全部。
func (r *ConnectionRepository) List(ctx context.Context, userID int64, isAdmin bool, dbType, keyword string) ([]Connection, error) {
	like := "%" + keyword + "%"
	q := `SELECT ` + connSelectCols + `
		FROM sys_connections c
		LEFT JOIN sys_ssh_tunnels t ON t.id = c.ssh_tunnel_id
		WHERE ($2 OR c.user_id = $1)
		  AND ($3 = '' OR c.type = $3)
		  AND ($4 = '' OR c.name ILIKE $4 OR c.host ILIKE $4)
		ORDER BY c.id DESC`
	rows, err := r.pool.Query(ctx, q, userID, isAdmin, dbType, like)
	if err != nil {
		return nil, fmt.Errorf("查询连接列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]Connection, 0)
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Get 取单条连接元数据。
func (r *ConnectionRepository) Get(ctx context.Context, id int64) (*Connection, error) {
	return scanConnection(r.pool.QueryRow(ctx,
		`SELECT `+connSelectCols+`
		 FROM sys_connections c
		 LEFT JOIN sys_ssh_tunnels t ON t.id = c.ssh_tunnel_id
		 WHERE c.id = $1`, id))
}

// GetSecrets 取解密口令（仅用于建连，禁止进入响应体）。
func (r *ConnectionRepository) GetSecrets(ctx context.Context, id int64) (*Connection, string, error) {
	c, err := r.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	var enc *string
	if err := r.pool.QueryRow(ctx, `SELECT password FROM sys_connections WHERE id = $1`, id).Scan(&enc); err != nil {
		return nil, "", err
	}
	pwd, err := r.decrypt(enc)
	if err != nil {
		return nil, "", err
	}
	return c, pwd, nil
}

func (r *ConnectionRepository) decrypt(enc *string) (string, error) {
	if enc == nil || *enc == "" {
		return "", nil
	}
	return r.cipher.Decrypt(*enc)
}

// Create 新建连接。
func (r *ConnectionRepository) Create(ctx context.Context, c *Connection) error {
	enc, err := r.cipher.Encrypt(c.Password)
	if err != nil {
		return err
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO sys_connections
			(user_id, ssh_tunnel_id, name, type, host, port, database, username,
			 password, ssl_mode, connection_timeout, color_label)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),
		        NULLIF($9,''),$10,$11,NULLIF($12,''))
		RETURNING id, created_at, updated_at`,
		c.UserID, c.SSHTunnelID, c.Name, c.Type, c.Host, c.Port, c.Database,
		c.Username, enc, c.SSLMode, c.ConnectionTimeout, c.ColorLabel,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

// Update 更新连接；Password 为空表示不修改口令。
func (r *ConnectionRepository) Update(ctx context.Context, c *Connection) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if c.Password != "" {
		enc, err := r.cipher.Encrypt(c.Password)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE sys_connections SET password = NULLIF($2,'') WHERE id = $1`, c.ID, enc); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE sys_connections SET
			ssh_tunnel_id = $2, name = $3, type = $4, host = $5, port = $6,
			database = NULLIF($7,''), username = NULLIF($8,''),
			ssl_mode = $9, connection_timeout = $10, color_label = NULLIF($11,'')
		WHERE id = $1`,
		c.ID, c.SSHTunnelID, c.Name, c.Type, c.Host, c.Port, c.Database,
		c.Username, c.SSLMode, c.ConnectionTimeout, c.ColorLabel)
	if err != nil {
		return fmt.Errorf("更新连接失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// Delete 删除连接（级联清理查询历史）。
func (r *ConnectionRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_connections WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("删除连接失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Count 按类型统计连接数量（仪表盘用）。
func (r *ConnectionRepository) Count(ctx context.Context) (total int64, byType map[string]int64, err error) {
	byType = map[string]int64{}
	if err = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sys_connections`).Scan(&total); err != nil {
		return
	}
	rows, err := r.pool.Query(ctx, `SELECT type, COUNT(*) FROM sys_connections GROUP BY type`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var n int64
		if err = rows.Scan(&t, &n); err != nil {
			return
		}
		byType[t] = n
	}
	err = rows.Err()
	return
}
