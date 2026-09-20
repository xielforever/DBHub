package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Dashboard struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Visibility  string          `json:"visibility"`
	Layout      json.RawMessage `json:"layout"`
	OwnerUserID int64           `json:"owner_user_id"`
	StarredBy   []int64         `json:"starred_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	OwnerName   string          `json:"owner_name,omitempty"`
	Starred     bool            `json:"starred,omitempty"`
}

type DashboardFilter struct {
	Keyword    string
	OwnerID    int64
	StarredBy  int64
	Visibility string
	Offset     int
	Limit      int
}

type DashboardRepository struct {
	pool *pgxpool.Pool
}

func NewDashboardRepository(pool *pgxpool.Pool) *DashboardRepository {
	return &DashboardRepository{pool: pool}
}

func (r *DashboardRepository) Create(ctx context.Context, d *Dashboard) error {
	if d.Layout == nil {
		d.Layout = json.RawMessage("[]")
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO sys_dashboards (name, description, visibility, layout, owner_user_id)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, created_at, updated_at`,
		d.Name, d.Description, d.Visibility, d.Layout, d.OwnerUserID,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
}

func (r *DashboardRepository) Get(ctx context.Context, id int64) (*Dashboard, error) {
	d := &Dashboard{}
	err := r.pool.QueryRow(ctx, `
		SELECT d.id, d.name, d.description, d.visibility, d.layout, d.owner_user_id,
		       COALESCE(d.starred_by,'{}'), d.created_at, d.updated_at,
		       COALESCE(u.username,'')
		FROM sys_dashboards d
		LEFT JOIN sys_users u ON u.id = d.owner_user_id
		WHERE d.id=$1`, id).Scan(
		&d.ID, &d.Name, &d.Description, &d.Visibility, &d.Layout, &d.OwnerUserID,
		&d.StarredBy, &d.CreatedAt, &d.UpdatedAt, &d.OwnerName)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return d, err
}

func (r *DashboardRepository) List(ctx context.Context, f DashboardFilter) ([]Dashboard, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	conds := []string{"1=1"}
	args := []any{}
	add := func(tmpl string, vals ...any) {
		for _, v := range vals {
			args = append(args, v)
			tmpl = strings.Replace(tmpl, "?", fmt.Sprintf("$%d", len(args)), 1)
		}
		conds = append(conds, tmpl)
	}
	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		add("(d.name ILIKE ? OR d.description ILIKE ?)", like, like)
	}
	if f.OwnerID > 0 {
		add("d.owner_user_id = ?", f.OwnerID)
	}
	if f.StarredBy > 0 {
		add("? = ANY(d.starred_by)", f.StarredBy)
	}
	if f.Visibility != "" {
		add("d.visibility = ?", f.Visibility)
	}
	where := strings.Join(conds, " AND ")
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sys_dashboards d WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计仪表盘失败: %w", err)
	}
	q := fmt.Sprintf(`
		SELECT d.id, d.name, d.description, d.visibility, d.layout, d.owner_user_id,
		       COALESCE(d.starred_by,'{}'), d.created_at, d.updated_at,
		       COALESCE(u.username,''),
		       COALESCE($%d = ANY(d.starred_by), FALSE)
		FROM sys_dashboards d
		LEFT JOIN sys_users u ON u.id = d.owner_user_id
		WHERE %s
		ORDER BY d.updated_at DESC
		LIMIT $%d OFFSET $%d`, len(args)+1, where, len(args)+2, len(args)+3)
	args = append(args, f.StarredBy, f.Limit, f.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询仪表盘失败: %w", err)
	}
	defer rows.Close()
	out := []Dashboard{}
	for rows.Next() {
		var d Dashboard
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.Visibility, &d.Layout, &d.OwnerUserID,
			&d.StarredBy, &d.CreatedAt, &d.UpdatedAt, &d.OwnerName, &d.Starred); err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

func (r *DashboardRepository) Update(ctx context.Context, d *Dashboard) error {
	if d.Layout == nil {
		d.Layout = json.RawMessage("[]")
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE sys_dashboards SET name=$2, description=$3, visibility=$4, layout=$5
		WHERE id=$1`, d.ID, d.Name, d.Description, d.Visibility, d.Layout)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *DashboardRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_dashboards WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *DashboardRepository) SetStar(ctx context.Context, id int64, userID int64, star bool) error {
	if star {
		_, err := r.pool.Exec(ctx, `
			UPDATE sys_dashboards SET starred_by = (
				SELECT COALESCE(array_agg(DISTINCT x), '{}')
				FROM unnest(array_append(COALESCE(starred_by,'{}'), $2)) x)
			WHERE id=$1`, id, userID)
		return err
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE sys_dashboards SET starred_by = array_remove(COALESCE(starred_by,'{}'), $2)
		WHERE id=$1`, id, userID)
	return err
}
