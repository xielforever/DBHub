package db

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/auth"
	"strings"
)

func (r *UserRepository) FindExternalIdentity(ctx context.Context, issuer, subject string) (*auth.User, error) {
	u := &auth.User{}
	err := r.pool.maintenance.QueryRow(ctx, `SELECT u.id,u.username,r.code,u.password_hash,u.is_active,u.last_login_at,u.auth_version FROM sys_external_identities e JOIN sys_users u ON u.id=e.user_id JOIN sys_roles r ON r.id=u.role_id WHERE e.issuer=$1 AND e.subject=$2 AND e.enabled AND u.is_active`, issuer, subject).Scan(&u.ID, &u.Username, &u.Role, &u.PasswordHash, &u.IsActive, &u.LastLoginAt, &u.AuthVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, auth.ErrUserNotFound
	}
	return u, err
}
func (r *UserRepository) ExternalIdentities(ctx context.Context, issuer string) ([]auth.ExternalIdentity, error) {
	if !r.IsPlatformAdmin(ctx) {
		return nil, ErrManagedDenied
	}
	rows, err := r.pool.maintenance.Query(ctx, `SELECT e.issuer,e.subject,e.user_id,u.username,e.version,e.enabled,e.updated_at FROM sys_external_identities e JOIN sys_users u ON u.id=e.user_id WHERE e.issuer=$1 ORDER BY u.username,e.subject LIMIT 1000`, issuer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []auth.ExternalIdentity{}
	for rows.Next() {
		var item auth.ExternalIdentity
		if err = rows.Scan(&item.Issuer, &item.Subject, &item.UserID, &item.Username, &item.Version, &item.Enabled, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (r *UserRepository) BindExternalIdentity(ctx context.Context, issuer, subject, username string, version int64, enabled bool) error {
	if !r.IsPlatformAdmin(ctx) {
		return ErrManagedDenied
	}
	if len(issuer) > 2048 || issuer == "" || len(subject) < 1 || len(subject) > 255 || strings.TrimSpace(subject) != subject || len(username) < 1 || len(username) > 256 || version < 0 {
		return ErrManagedInput
	}
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	// Platform role is rechecked in the same transaction as the binding.
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return ErrManagedDenied
	}
	var admin bool
	if err = tx.QueryRow(ctx, `SELECT is_active AND is_platform_admin FROM sys_users WHERE id=$1 FOR SHARE`, claims.Subject).Scan(&admin); err != nil || !admin {
		return ErrManagedDenied
	}
	var uid int64
	if err = tx.QueryRow(ctx, `SELECT id FROM sys_users WHERE username=$1 AND is_active FOR UPDATE`, username).Scan(&uid); errors.Is(err, pgx.ErrNoRows) {
		return ErrManagedInput
	} else if err != nil {
		return err
	}
	if version == 0 {
		if !enabled {
			return ErrManagedInput
		}
		_, err = tx.Exec(ctx, `INSERT INTO sys_external_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, issuer, subject, uid)
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return ErrManagedConflict
		}
		if err != nil {
			return err
		}
	} else {
		tag, e := tx.Exec(ctx, `UPDATE sys_external_identities SET enabled=$5,version=version+1,updated_at=now() WHERE issuer=$1 AND subject=$2 AND user_id=$3 AND version=$4`, issuer, subject, uid, version, enabled)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrManagedConflict
		}
	}
	// Revoking or restoring a binding also invalidates earlier SSO handoffs and
	// sessions. Identity bindings do not grant workspace membership or roles.
	if _, err = tx.Exec(ctx, `UPDATE sys_users SET auth_version=auth_version+1 WHERE id=$1`, uid); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
