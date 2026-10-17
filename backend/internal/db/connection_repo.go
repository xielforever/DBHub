package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/pkg/crypto"
)

// Connection 目标数据库连接配置。Password 仅写入/建连时短暂存在，
// 列表与详情接口只返回 HasPassword。
type Connection struct {
	ID                int64            `json:"id"`
	UserID            int64            `json:"user_id"`
	SSHTunnelID       *int64           `json:"ssh_tunnel_id"`
	ProxyID           *int64           `json:"proxy_id"`
	ProxyName         string           `json:"proxy_name"`
	Proxy             *Proxy           `json:"-"`
	Name              string           `json:"name"`
	Type              string           `json:"type"` // mysql | postgres | redis | mongo
	Host              string           `json:"host"`
	Port              int              `json:"port"`
	Database          string           `json:"database"`
	Username          string           `json:"username"`
	Password          string           `json:"-"`
	SSLMode           string           `json:"ssl_mode"`
	ConnectionTimeout int              `json:"connection_timeout"`
	ColorLabel        string           `json:"color_label"`
	Environment       string           `json:"environment"` // dev | test | prod
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	LastCheck         *ConnectionCheck `json:"last_check,omitempty"`

	HasPassword        bool                   `json:"has_password"`
	TemplateRevisionID *int64                 `json:"template_revision_id,omitempty"`
	Parameters         map[string]string      `json:"parameters,omitempty"`
	Template           *DatasourceTemplate    `json:"template,omitempty"`
	Capabilities       connector.Capabilities `json:"capabilities"`

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

// defaultEnv 环境字段兜底，非法/空值一律按开发环境处理。
func defaultEnv(env string) string {
	switch env {
	case "dev", "test", "prod":
		return env
	default:
		return "dev"
	}
}

const connSelectCols = `
	c.id, c.user_id, c.ssh_tunnel_id, c.name, c.type, c.host, c.port,
	COALESCE(c.database, ''), COALESCE(c.username, ''),
	c.password IS NOT NULL AND c.password <> '',
	c.ssl_mode, c.connection_timeout, COALESCE(c.color_label, ''),
	COALESCE(c.environment, 'dev'),
	c.created_at, c.updated_at, COALESCE(t.name, ''), c.proxy_id,
	COALESCE((SELECT p.name FROM sys_proxies p WHERE p.id=c.proxy_id),''),
	(SELECT cc.result FROM sys_connection_checks cc WHERE cc.connection_id=c.id AND cc.config_updated_at=c.updated_at),c.template_revision_id,c.parameters,
 (SELECT to_jsonb(dt)||jsonb_build_object('driver',to_jsonb(jd)) FROM sys_datasource_templates dt JOIN sys_jdbc_drivers jd ON jd.id=dt.driver_id WHERE dt.id=c.template_revision_id)`

func scanConnection(row pgx.Row, extra ...any) (*Connection, error) {
	c := &Connection{}
	var checkJSON, paramsJSON, templateJSON []byte
	dest := []any{&c.ID, &c.UserID, &c.SSHTunnelID, &c.Name, &c.Type, &c.Host, &c.Port,
		&c.Database, &c.Username, &c.HasPassword, &c.SSLMode, &c.ConnectionTimeout,
		&c.ColorLabel, &c.Environment, &c.CreatedAt, &c.UpdatedAt, &c.TunnelName, &c.ProxyID, &c.ProxyName, &checkJSON, &c.TemplateRevisionID, &paramsJSON, &templateJSON}
	err := row.Scan(append(dest, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(checkJSON) > 0 {
		if err := json.Unmarshal(checkJSON, &c.LastCheck); err != nil {
			return nil, err
		}
	}
	if len(paramsJSON) > 0 {
		if err := json.Unmarshal(paramsJSON, &c.Parameters); err != nil {
			return nil, err
		}
	}
	if len(templateJSON) > 0 {
		if err := json.Unmarshal(templateJSON, &c.Template); err != nil {
			return nil, err
		}
	}
	c.Capabilities = c.SupportedCapabilities()
	return c, nil
}

// List 连接列表，可按类型/关键字过滤。
// 数据源为团队共享资源：所有登录用户均可查看与使用；userID/isAdmin 仅用于
// 未来的「个人连接」可见性扩展，当前不做归属过滤（写操作仍在 handler 层鉴权）。
func (r *ConnectionRepository) List(ctx context.Context, dbType, keyword string) ([]Connection, error) {
	like := "%" + keyword + "%"
	q := `SELECT ` + connSelectCols + `
		FROM sys_connections c
		LEFT JOIN sys_ssh_tunnels t ON t.id = c.ssh_tunnel_id
		WHERE ($1 = '' OR c.type = $1)
		  AND ($2 = '' OR c.name ILIKE $2 OR c.host ILIKE $2)
		ORDER BY c.id DESC`
	rows, err := r.pool.Query(ctx, q, dbType, like)
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
	// 配置与口令来自同一条语句，避免并发编辑时拼成旧地址与新口令。
	var enc *string
	c, err := scanConnection(r.pool.QueryRow(ctx,
		`SELECT `+connSelectCols+`, c.password FROM sys_connections c
		LEFT JOIN sys_ssh_tunnels t ON t.id=c.ssh_tunnel_id WHERE c.id=$1`, id), &enc)
	if err != nil {
		return nil, "", err
	}
	pwd, err := r.decrypt(enc)
	if err != nil {
		return nil, "", err
	}
	if c.ProxyID != nil {
		c.Proxy, err = NewProxyRepository(r.pool, r.cipher).GetSecrets(ctx, *c.ProxyID)
		if err != nil {
			return nil, "", err
		}
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
			 password, ssl_mode, connection_timeout, color_label, environment, proxy_id,template_revision_id,parameters)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),
		        NULLIF($9,''),$10,$11,NULLIF($12,''),$13,$14,$15,$16::jsonb)
		RETURNING id, created_at, updated_at`,
		c.UserID, c.SSHTunnelID, c.Name, c.Type, c.Host, c.Port, c.Database,
		c.Username, enc, c.SSLMode, c.ConnectionTimeout, c.ColorLabel,
		defaultEnv(c.Environment), c.ProxyID, c.TemplateRevisionID, mustJSONParameters(c.Parameters),
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
			ssl_mode = $9, connection_timeout = $10, color_label = NULLIF($11,''),
			environment = $12, proxy_id = $13,template_revision_id=$14,parameters=$15::jsonb
		WHERE id = $1`,
		c.ID, c.SSHTunnelID, c.Name, c.Type, c.Host, c.Port, c.Database,
		c.Username, c.SSLMode, c.ConnectionTimeout, c.ColorLabel, defaultEnv(c.Environment), c.ProxyID, c.TemplateRevisionID, mustJSONParameters(c.Parameters))
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

// SQLDialect is independent of the connector transport. Unknown products fail closed.
func (c *Connection) SQLDialect() string {
	if c.Type == "mysql" || c.Type == "postgres" {
		return c.Type
	}
	if c.Type == "jdbc" && c.Template != nil {
		return c.Template.Dialect
	}
	return ""
}
func (c *Connection) SupportedCapabilities() connector.Capabilities {
	if c.Type == "redis" {
		return connector.Capabilities{Keys: true}
	}
	if c.Type == "mysql" || c.Type == "postgres" {
		return connector.Capabilities{SQL: true, Format: true, Metadata: true, Collect: true, Transactions: true, EnforcedReadOnly: true}
	}
	if c.Type == "jdbc" && c.Template != nil {
		if !c.Template.Enabled || !c.Template.Driver.Enabled {
			return connector.Capabilities{Reason: "驱动或模板已停用"}
		}
		sql := c.Template.Dialect == "postgres" || c.Template.Dialect == "mysql"
		reason := ""
		if !sql {
			reason = "此模板仅启用结构浏览，SQL 方言尚未适配"
		}
		return connector.Capabilities{SQL: sql, Format: sql, Metadata: true, Collect: c.Template.Driver.ClassName == "org.postgresql.Driver" || c.Template.Driver.ClassName == "com.mysql.cj.jdbc.Driver" || c.Template.Driver.ClassName == "org.h2.Driver", Transactions: sql, EnforcedReadOnly: sql, Reason: reason}
	}
	return connector.Capabilities{Reason: "未注册的数据源类型"}
}
func mustJSONParameters(params map[string]string) string {
	if params == nil {
		return "{}"
	}
	b, _ := json.Marshal(params)
	return string(b)
}
