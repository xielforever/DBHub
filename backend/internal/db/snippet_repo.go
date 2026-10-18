package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Snippet struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	Name         string    `json:"name"`
	SQL          string    `json:"sql_text"`
	Database     string    `json:"database_name"`
	ConnectionID *int64    `json:"connection_id"`
	Visibility   string    `json:"visibility"`
	OwnerName    string    `json:"owner_name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type SnippetRepository struct{ pool *ScopedPool }

func NewSnippetRepository(pool *pgxpool.Pool) *SnippetRepository {
	return &SnippetRepository{pool: scopedPool(pool)}
}

const snippetCols = `s.id,s.user_id,s.name,s.sql_text,s.database_name,s.connection_id,s.visibility,u.username,s.created_at,s.updated_at`

func scanSnippet(row pgx.Row) (*Snippet, error) {
	s := &Snippet{}
	e := row.Scan(&s.ID, &s.UserID, &s.Name, &s.SQL, &s.Database, &s.ConnectionID, &s.Visibility, &s.OwnerName, &s.CreatedAt, &s.UpdatedAt)
	if e == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return s, e
}
func (r *SnippetRepository) Get(ctx context.Context, id, userID int64, admin bool) (*Snippet, error) {
	return scanSnippet(r.pool.QueryRow(ctx, `SELECT `+snippetCols+` FROM sys_sql_snippets s JOIN sys_users u ON u.id=s.user_id WHERE s.id=$1 AND ($3 OR s.user_id=$2 OR s.visibility='shared')`, id, userID, admin))
}
func (r *SnippetRepository) List(ctx context.Context, userID int64, q, visibility string, connection int64, limit, offset int) ([]Snippet, int64, error) {
	where := ` FROM sys_sql_snippets s JOIN sys_users u ON u.id=s.user_id WHERE (s.user_id=$1 OR s.visibility='shared') AND ($2='' OR s.name ILIKE '%'||$2||'%' OR s.sql_text ILIKE '%'||$2||'%') AND ($3='' OR s.visibility=$3) AND ($4::bigint=0 OR s.connection_id=$4)`
	var total int64
	if e := r.pool.QueryRow(ctx, "SELECT COUNT(*)"+where, userID, q, visibility, connection).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, "SELECT "+snippetCols+where+" ORDER BY s.updated_at DESC,s.id DESC LIMIT $5 OFFSET $6", userID, q, visibility, connection, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Snippet{}
	for rows.Next() {
		s, e := scanSnippet(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, *s)
	}
	return out, total, rows.Err()
}
func (r *SnippetRepository) Create(ctx context.Context, s *Snippet) error {
	return r.pool.QueryRow(ctx, `INSERT INTO sys_sql_snippets(user_id,name,sql_text,database_name,connection_id,visibility) VALUES($1,$2,$3,$4,$5,$6) RETURNING id,created_at,updated_at`, s.UserID, s.Name, s.SQL, s.Database, s.ConnectionID, s.Visibility).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
}
func (r *SnippetRepository) Update(ctx context.Context, s *Snippet, userID int64, admin bool) error {
	tag, e := r.pool.Exec(ctx, `UPDATE sys_sql_snippets SET name=$2,sql_text=$3,database_name=$4,visibility=$5,updated_at=NOW() WHERE id=$1 AND (user_id=$6 OR $7)`, s.ID, s.Name, s.SQL, s.Database, s.Visibility, userID, admin)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}
func (r *SnippetRepository) Delete(ctx context.Context, id, userID int64, admin bool) error {
	tag, e := r.pool.Exec(ctx, `DELETE FROM sys_sql_snippets WHERE id=$1 AND (user_id=$2 OR $3)`, id, userID, admin)
	if e == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return e
}
