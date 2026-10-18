package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"time"
)

var ErrCatalogConflict = errors.New("驱动或模板已存在")
var ErrCatalogInUse = errors.New("该版本仍被引用，不能删除")
var ErrCatalogDisabled = errors.New("驱动或模板未就绪或已停用")
var ErrPersonalBindingExists = errors.New("已添加该数据源，请在我的连接中修改账号或确认新修订")

type JDBCDriver struct {
	ID         int64     `json:"id"`
	Visibility string    `json:"visibility"`
	ArtifactID string    `json:"artifact_id"`
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	ClassName  string    `json:"class_name"`
	SHA256     string    `json:"sha256"`
	Enabled    bool      `json:"enabled"`
	CreatedBy  int64     `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}
type TemplateField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  string `json:"default,omitempty"`
}
type DatasourceTemplate struct {
	ID          int64           `json:"id"`
	Visibility  string          `json:"visibility"`
	Key         string          `json:"template_key"`
	Revision    int             `json:"revision"`
	Name        string          `json:"name"`
	Product     string          `json:"product"`
	DriverID    int64           `json:"driver_id"`
	Driver      JDBCDriver      `json:"driver"`
	Dialect     string          `json:"dialect"`
	URLTemplate string          `json:"url_template"`
	Fields      []TemplateField `json:"fields"`
	Enabled     bool            `json:"enabled"`
	CreatedBy   int64           `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
}
type ConnectorCatalog struct{ pool *ScopedPool }

func (r *ConnectionRepository) Catalog() *ConnectorCatalog { return &ConnectorCatalog{pool: r.pool} }
func catalogError(e error) error {
	var p *pgconn.PgError
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if errors.As(e, &p) {
		if p.Code == "23505" {
			if p.ConstraintName == "connection_user_definition" {
				return ErrPersonalBindingExists
			}
			return ErrCatalogConflict
		}
		if p.Code == "23503" {
			return ErrCatalogInUse
		}
	}
	return e
}
func (r *ConnectorCatalog) Drivers(ctx context.Context) ([]JDBCDriver, error) {
	if ActorID(ctx) == 0 {
		return nil, ErrNotFound
	}
	rows, e := r.pool.Query(ctx, `SELECT row_to_json(d) FROM sys_jdbc_drivers d WHERE d.visibility='global' OR d.created_by=$1 ORDER BY id`, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []JDBCDriver{}
	for rows.Next() {
		var raw []byte
		var d JDBCDriver
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &d); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r *ConnectorCatalog) Driver(ctx context.Context, id int64) (*JDBCDriver, error) {
	if ActorID(ctx) == 0 {
		return nil, ErrNotFound
	}
	var raw []byte
	e := r.pool.QueryRow(ctx, `SELECT row_to_json(d) FROM sys_jdbc_drivers d WHERE id=$1 AND (d.visibility='global' OR d.created_by=$2)`, id, ActorID(ctx)).Scan(&raw)
	if e != nil {
		return nil, catalogError(e)
	}
	var d JDBCDriver
	e = json.Unmarshal(raw, &d)
	return &d, e
}
func (r *ConnectorCatalog) RegisterDriver(ctx context.Context, d *JDBCDriver) error {
	return catalogError(r.pool.QueryRow(ctx, `INSERT INTO sys_jdbc_drivers(artifact_id,name,version,class_name,sha256,created_by,visibility)VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,enabled,created_at`, d.ArtifactID, d.Name, d.Version, d.ClassName, d.SHA256, d.CreatedBy, d.Visibility).Scan(&d.ID, &d.Enabled, &d.CreatedAt))
}
func (r *ConnectorCatalog) Templates(ctx context.Context, all bool) ([]DatasourceTemplate, error) {
	if ActorID(ctx) == 0 {
		return nil, ErrNotFound
	}
	rows, e := r.pool.Query(ctx, `SELECT to_jsonb(t)||jsonb_build_object('driver',to_jsonb(d)) FROM sys_datasource_templates t JOIN sys_jdbc_drivers d ON d.id=t.driver_id WHERE (t.visibility='global' OR t.created_by=$2) AND (d.visibility='global' OR d.created_by=$2) AND ($1 OR t.created_by=$2 OR (t.enabled AND d.enabled)) ORDER BY t.template_key,t.revision DESC`, all, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DatasourceTemplate{}
	for rows.Next() {
		var raw []byte
		var t DatasourceTemplate
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &t); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (r *ConnectorCatalog) Template(ctx context.Context, id int64) (*DatasourceTemplate, error) {
	if ActorID(ctx) == 0 {
		return nil, ErrNotFound
	}
	var raw []byte
	e := r.pool.QueryRow(ctx, `SELECT to_jsonb(t)||jsonb_build_object('driver',to_jsonb(d)) FROM sys_datasource_templates t JOIN sys_jdbc_drivers d ON d.id=t.driver_id WHERE t.id=$1 AND (t.visibility='global' OR t.created_by=$2) AND (d.visibility='global' OR d.created_by=$2)`, id, ActorID(ctx)).Scan(&raw)
	if e != nil {
		return nil, catalogError(e)
	}
	var t DatasourceTemplate
	e = json.Unmarshal(raw, &t)
	return &t, e
}
func (r *ConnectorCatalog) Publish(ctx context.Context, t *DatasourceTemplate, appendRevision bool) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('dbhub-template:'||$1))`, fmt.Sprintf("%s:%s:%d", t.Key, t.Visibility, func() int64 {
		if t.Visibility == "global" {
			return 0
		}
		return ActorID(ctx)
	}())); e != nil {
		return e
	}
	var previous int
	e = tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0) FROM sys_datasource_templates WHERE template_key=$1 AND visibility=$2 AND (visibility='global' OR created_by=$3)`, t.Key, t.Visibility, ActorID(ctx)).Scan(&previous)
	if e != nil {
		return e
	}
	if !appendRevision && previous > 0 {
		return ErrCatalogConflict
	}
	if appendRevision && previous == 0 {
		return ErrNotFound
	}
	var enabled bool
	e = tx.QueryRow(ctx, `SELECT enabled FROM sys_jdbc_drivers WHERE id=$1 FOR SHARE`, t.DriverID).Scan(&enabled)
	if e != nil {
		return catalogError(e)
	}
	if !enabled {
		return ErrCatalogDisabled
	}
	fields, e := json.Marshal(t.Fields)
	if e != nil {
		return e
	}
	t.Revision = previous + 1
	e = tx.QueryRow(ctx, `INSERT INTO sys_datasource_templates(template_key,revision,name,product,driver_id,dialect,url_template,fields,created_by,visibility)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)RETURNING id,enabled,created_at`, t.Key, t.Revision, t.Name, t.Product, t.DriverID, t.Dialect, t.URLTemplate, string(fields), t.CreatedBy, t.Visibility).Scan(&t.ID, &t.Enabled, &t.CreatedAt)
	if e != nil {
		return catalogError(e)
	}
	return tx.Commit(ctx)
}
func (r *ConnectorCatalog) SetEnabled(ctx context.Context, kind string, id int64, enabled bool) error {
	query := `UPDATE sys_jdbc_drivers SET enabled=$2 WHERE id=$1 AND ((visibility='private' AND created_by=$3) OR (visibility='global' AND $4))`
	if kind == "template" {
		query = `UPDATE sys_datasource_templates SET enabled=$2 WHERE id=$1 AND ((visibility='private' AND created_by=$3) OR (visibility='global' AND $4))`
	}
	tag, e := r.pool.Exec(ctx, query, id, enabled, ActorID(ctx), ActorAdmin(ctx))
	if e != nil {
		return catalogError(e)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *ConnectorCatalog) Delete(ctx context.Context, kind string, id int64) error {
	query := `DELETE FROM sys_jdbc_drivers WHERE id=$1 AND ((visibility='private' AND created_by=$2) OR (visibility='global' AND $3))`
	if kind == "template" {
		query = `DELETE FROM sys_datasource_templates WHERE id=$1 AND ((visibility='private' AND created_by=$2) OR (visibility='global' AND $3))`
	}
	tag, e := r.pool.Exec(ctx, query, id, ActorID(ctx), ActorAdmin(ctx))
	if e != nil {
		return catalogError(e)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
