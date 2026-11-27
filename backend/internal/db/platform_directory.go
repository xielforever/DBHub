package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// PlatformDirectoryFilter holds platform directory filters and bounded pagination.
type PlatformDirectoryFilter struct {
	Keyword, Status, LoginMethod string
	Limit, Offset                int
}

// PlatformRoleTemplate describes one shared role without a workspace binding.
type PlatformRoleTemplate struct {
	ID      string   `json:"id"`
	Code    string   `json:"code"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// PlatformRoleTemplates reads the current shared role definitions for a superadmin.
func (r *UserRepository) PlatformRoleTemplates(ctx context.Context) ([]PlatformRoleTemplate, error) {
	tx, err := r.pool.maintenance.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = platformAccountActor(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT role_id::text,code,display_name,actions FROM sys_iam_builtin_roles ORDER BY display_order`)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[PlatformRoleTemplate])
	if err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}

const platformAccountMatching = `WITH accounts AS (` + platformAccountProjection + `), matched AS (
	SELECT * FROM accounts
	WHERE ($1='' OR strpos(lower(username),lower($1))>0 OR strpos(lower(email),lower($1))>0)
	AND ($2='' OR status=$2)
	AND ($3='' OR ($3='local' AND local_login) OR ($3='sso' AND sso_login))
) `

// PlatformAccounts reads platform login identities under a current superadmin check.
func (r *UserRepository) PlatformAccounts(ctx context.Context, f PlatformDirectoryFilter) ([]PlatformAccount, int64, error) {
	tx, err := r.pool.maintenance.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = platformAccountActor(ctx, tx); err != nil {
		return nil, 0, err
	}
	var total int64
	args := []any{f.Keyword, f.Status, f.LoginMethod}
	if err = tx.QueryRow(ctx, platformAccountMatching+"SELECT count(*) FROM matched", args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, platformAccountMatching+"SELECT * FROM matched ORDER BY username,id LIMIT $4 OFFSET $5",
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []PlatformAccount{}
	for rows.Next() {
		item, err := scanPlatformAccount(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, tx.Commit(ctx)
}

type PlatformWorkspaceFounder struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type PlatformWorkspace struct {
	ID            int64                     `json:"id"`
	Name          string                    `json:"name"`
	Enabled       bool                      `json:"enabled"`
	CreatedAt     time.Time                 `json:"created_at"`
	FoundingAdmin *PlatformWorkspaceFounder `json:"founding_admin,omitempty"`
}

// PlatformWorkspaces reads directory metadata without binding or granting workspace access.
func (r *UserRepository) PlatformWorkspaces(ctx context.Context, f PlatformDirectoryFilter) ([]PlatformWorkspace, int64, error) {
	tx, err := r.pool.maintenance.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = platformAccountActor(ctx, tx); err != nil {
		return nil, 0, err
	}
	const matching = " FROM sys_workspaces WHERE id>0 AND ($1='' OR strpos(lower(name),lower($1))>0)"
	var total int64
	if err = tx.QueryRow(ctx, "SELECT count(*)"+matching, f.Keyword).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `WITH page AS (
		SELECT id,name,enabled,created_at`+matching+` ORDER BY id LIMIT $2 OFFSET $3)
		SELECT w.id,w.name,w.enabled,w.created_at,u.id,COALESCE(u.username,'')
		FROM page w LEFT JOIN LATERAL (
			SELECT actor_id FROM sys_workspace_creation_commands
			WHERE result->>'id'=w.id::text ORDER BY created_at LIMIT 1
		) founder ON true
		LEFT JOIN sys_users u ON u.id=founder.actor_id
		ORDER BY w.id`, f.Keyword, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []PlatformWorkspace{}
	for rows.Next() {
		var item PlatformWorkspace
		var founderID *int64
		var username string
		if err := rows.Scan(&item.ID, &item.Name, &item.Enabled, &item.CreatedAt, &founderID, &username); err != nil {
			return nil, 0, err
		}
		if founderID != nil {
			item.FoundingAdmin = &PlatformWorkspaceFounder{ID: *founderID, Username: username}
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, tx.Commit(ctx)
}
