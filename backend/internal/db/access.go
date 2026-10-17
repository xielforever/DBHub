package db

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
)

// ActorID fails closed when an operation has lost its authenticated context.
// Administrators have no implicit right to use another person's credentials.
func ActorID(ctx context.Context) int64 {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c == nil || c.Subject <= 0 {
		return 0
	}
	return c.Subject
}
func ActorAdmin(ctx context.Context) bool {
	c, ok := auth.ClaimsFromContext(ctx)
	return ok && c != nil && c.Subject > 0 && c.Role == "admin"
}

func requireCurrentActor(ctx context.Context, pool *pgxpool.Pool) error {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c == nil || c.Subject <= 0 {
		return ErrNotFound
	}
	var valid bool
	e := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_users u JOIN sys_roles r ON r.id=u.role_id WHERE u.id=$1 AND u.is_active AND r.code=$2)`, c.Subject, c.Role).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return ErrConnectionChanged
	}
	return nil
}

// connectionScope only interpolates a server-owned column and an integer actor.
func connectionScope(ctx context.Context, column string) string {
	return "EXISTS (SELECT 1 FROM sys_connections access_conn WHERE access_conn.id=" + column +
		" AND access_conn.user_id=" + strconv.FormatInt(ActorID(ctx), 10) + ")"
}
func requireConnection(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	var found bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_connections WHERE id=$1 AND user_id=$2)`, id, ActorID(ctx)).Scan(&found); err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}
