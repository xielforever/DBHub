package db

import (
	"context"
	"strconv"

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
func ActorCapability(ctx context.Context, capability string) bool {
	c, _ := auth.ClaimsFromContext(ctx)
	return auth.HasCapability(c, capability)
}

func ActorAction(ctx context.Context, action string, legacy bool) bool {
	c, _ := auth.ClaimsFromContext(ctx)
	return auth.FunctionalAction(c, action, legacy)
}
func ActorReportReviewer(ctx context.Context) bool { return ActorCapability(ctx, "report_review") }
func ActorDashboardReviewer(ctx context.Context) bool {
	return ActorAction(ctx, "dashboard.review", ActorCapability(ctx, "report_review"))
}
func ActorExportReviewer(ctx context.Context) bool {
	return ActorAction(ctx, "report.exportReview", ActorCapability(ctx, "report_review"))
}

func requireCurrentActor(ctx context.Context, pool *ScopedPool) error {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c == nil || c.Subject <= 0 {
		return ErrNotFound
	}
	var valid bool
	e := pool.QueryRow(ctx, `SELECT dbhub_scope_allowed()`).Scan(&valid)
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
		" AND access_conn.user_id=" + strconv.FormatInt(ActorID(ctx), 10) + " AND dbhub_source_visible(access_conn.definition_revision_id,access_conn.user_id))"
}
func requireConnection(ctx context.Context, pool *ScopedPool, id int64) error {
	var found bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_connections WHERE id=$1 AND user_id=$2 AND dbhub_source_visible(definition_revision_id,$2))`, id, ActorID(ctx)).Scan(&found); err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

func actorRole(ctx context.Context) (string, bool) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c == nil {
		return "", false
	}
	return c.Role, true
}
