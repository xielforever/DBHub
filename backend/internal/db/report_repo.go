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

// Report 报表：一张图表 = 查询 + 图表配置
type Report struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ConnectionID int64           `json:"connection_id"`
	DatabaseName string          `json:"database_name"`
	SQLText      string          `json:"sql_text"`
	ChartType    string          `json:"chart_type"`
	ChartConfig  json.RawMessage `json:"chart_config"`
	Visibility   string          `json:"visibility"`
	OwnerUserID  int64           `json:"owner_user_id"`
	StarredBy    []int64         `json:"starred_by"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	OwnerName    string          `json:"owner_name,omitempty"`
	Starred      bool            `json:"starred,omitempty"`
}

// ReportFilter 列表过滤
type ReportFilter struct {
	Keyword     string
	OwnerID     int64 // 仅我的
	StarredBy   int64 // 仅收藏
	Visibility  string
	ConnectionID int64
	Offset      int
	Limit       int
}

type ReportRepository struct {
	pool *pgxpool.Pool
}

func NewReportRepository(pool *pgxpool.Pool) *ReportRepository {
	return &ReportRepository{pool: pool}
}

func (r *ReportRepository) Create(ctx context.Context, rep *Report) error {
	if rep.ChartConfig == nil {
		rep.ChartConfig = json.RawMessage("{}")
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO sys_reports
		(name, description, connection_id, database_name, sql_text, chart_type, chart_config, visibility, owner_user_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at, updated_at`,
		rep.Name, rep.Description, rep.ConnectionID, rep.DatabaseName, rep.SQLText,
		rep.ChartType, rep.ChartConfig, rep.Visibility, rep.OwnerUserID,
	).Scan(&rep.ID, &rep.CreatedAt, &rep.UpdatedAt)
}

func (r *ReportRepository) Get(ctx context.Context, id int64) (*Report, error) {
	rep := &Report{}
	err := r.pool.QueryRow(ctx, `
		SELECT r.id, r.name, r.description, r.connection_id, r.database_name, r.sql_text,
		       r.chart_type, r.chart_config, r.visibility, r.owner_user_id,
		       COALESCE(r.starred_by,'{}'), r.created_at, r.updated_at,
		       COALESCE(u.username,'')
		FROM sys_reports r
		LEFT JOIN sys_users u ON u.id = r.owner_user_id
		WHERE r.id = $1`, id).Scan(
		&rep.ID, &rep.Name, &rep.Description, &rep.ConnectionID, &rep.DatabaseName, &rep.SQLText,
		&rep.ChartType, &rep.ChartConfig, &rep.Visibility, &rep.OwnerUserID,
		&rep.StarredBy, &rep.CreatedAt, &rep.UpdatedAt, &rep.OwnerName)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return rep, err
}

func (r *ReportRepository) List(ctx context.Context, f ReportFilter) ([]Report, int64, error) {
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
		add("(r.name ILIKE ? OR r.description ILIKE ? OR r.sql_text ILIKE ?)", like, like, like)
	}
	if f.OwnerID > 0 {
		add("r.owner_user_id = ?", f.OwnerID)
	}
	if f.StarredBy > 0 {
		add("? = ANY(r.starred_by)", f.StarredBy)
	}
	if f.Visibility != "" {
		add("r.visibility = ?", f.Visibility)
	}
	if f.ConnectionID > 0 {
		add("r.connection_id = ?", f.ConnectionID)
	}
	where := strings.Join(conds, " AND ")
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sys_reports r WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计报表失败: %w", err)
	}
	q := fmt.Sprintf(`
		SELECT r.id, r.name, r.description, r.connection_id, r.database_name, r.sql_text,
		       r.chart_type, r.chart_config, r.visibility, r.owner_user_id,
		       COALESCE(r.starred_by,'{}'), r.created_at, r.updated_at,
		       COALESCE(u.username,''),
		       COALESCE($%d = ANY(r.starred_by), FALSE)
		FROM sys_reports r
		LEFT JOIN sys_users u ON u.id = r.owner_user_id
		WHERE %s
		ORDER BY r.updated_at DESC
		LIMIT $%d OFFSET $%d`, len(args)+1, where, len(args)+2, len(args)+3)
	args = append(args, f.StarredBy, f.Limit, f.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询报表失败: %w", err)
	}
	defer rows.Close()
	out := []Report{}
	for rows.Next() {
		var rep Report
		if err := rows.Scan(
			&rep.ID, &rep.Name, &rep.Description, &rep.ConnectionID, &rep.DatabaseName, &rep.SQLText,
			&rep.ChartType, &rep.ChartConfig, &rep.Visibility, &rep.OwnerUserID,
			&rep.StarredBy, &rep.CreatedAt, &rep.UpdatedAt, &rep.OwnerName, &rep.Starred); err != nil {
			return nil, 0, err
		}
		out = append(out, rep)
	}
	return out, total, rows.Err()
}

func (r *ReportRepository) Update(ctx context.Context, rep *Report) error {
	if rep.ChartConfig == nil {
		rep.ChartConfig = json.RawMessage("{}")
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE sys_reports SET
			name=$2, description=$3, connection_id=$4, database_name=$5,
			sql_text=$6, chart_type=$7, chart_config=$8, visibility=$9
		WHERE id=$1`, rep.ID, rep.Name, rep.Description, rep.ConnectionID, rep.DatabaseName,
		rep.SQLText, rep.ChartType, rep.ChartConfig, rep.Visibility)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ReportRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sys_reports WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ReportRepository) SetStar(ctx context.Context, id int64, userID int64, star bool) error {
	if star {
		_, err := r.pool.Exec(ctx, `
			UPDATE sys_reports SET starred_by = (
				SELECT COALESCE(array_agg(DISTINCT x), '{}')
				FROM unnest(array_append(COALESCE(starred_by,'{}'), $2)) x)
			WHERE id=$1`, id, userID)
		return err
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE sys_reports SET starred_by = array_remove(COALESCE(starred_by,'{}'), $2)
		WHERE id=$1`, id, userID)
	return err
}
