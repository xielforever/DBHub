package db

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
)

func TestDashboardMatrixUpgradeExistingMembersInOneWorkspace(t *testing.T) {
	const migration = "0105_iam_dashboard_matrix.sql"
	pool, users, root := iamTestDatabasePrepared(t, func(ctx context.Context, pool *pgxpool.Pool) {
		// Populate real builtin grants against the pre-upgrade schema.
		if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (
 version VARCHAR(255) PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
 INSERT INTO schema_migrations(version) VALUES($1)`, migration); err != nil {
			t.Fatal(err)
		}
	})
	asUser := func(id int64) context.Context {
		return auth.WithClaims(context.Background(), &auth.Claims{Subject: id})
	}
	makeSpace := func(name string) int64 {
		t.Helper()
		w, err := users.CreateMatrixWorkspace(root, name, dashboardUUID())
		if err != nil {
			t.Fatal(err)
		}
		admin, err := users.BindWorkspace(asUser(1), w.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"developer", "readonly"} {
			if err := users.AddMember(admin, name, "readonly"); err != nil {
				t.Fatal(err)
			}
		}
		return w.ID
	}
	grant := func(space, user int64, codes ...string) IAMDirectMember {
		t.Helper()
		list, err := users.IAMDirectMembers(root, space)
		if err != nil {
			t.Fatal(err)
		}
		for _, member := range list.Members {
			if member.UserID == user {
				result, err := users.IAMSetDirectRoles(root, space, user, IAMDirectRoleCommand{
					RequestID: dashboardUUID(), ExpectedVersion: member.Version, RoleCodes: append([]string{}, codes...),
				})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
		}
		t.Fatalf("workspace %d is missing member %d", space, user)
		return IAMDirectMember{}
	}
	active, neighbor := makeSpace("dashboard matrix target"), makeSpace("dashboard matrix neighbor")
	author := grant(active, 2, "business_it", "business_supervisor")
	grant(neighbor, 2, "business_it", "business_supervisor")
	grant(active, 3, "viewer")
	expiredMember := grant(active, 3)
	if _, err := pool.Exec(root, `INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,
 subject_type,subject_id,approved_members,valid_from,valid_until,granted_by,proposal_id)
 SELECT workspace_id,gen_random_uuid(),role_id,role_revision,subject_type,subject_id,approved_members,
 now()-interval '2 days',now()-interval '1 day',granted_by,gen_random_uuid()
 FROM sys_iam_assignments WHERE workspace_id=$1 AND subject_id=3 AND revoked_at IS NOT NULL`, active); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(root, `DELETE FROM schema_migrations WHERE version=$1`, migration); err != nil {
		t.Fatal(err)
	}
	if applied, err := Migrate(root, pool); err != nil || !slices.Equal(applied, []string{migration}) {
		t.Fatalf("scoped migration not applied: %v %v", applied, err)
	}
	bind := func(space, user int64) context.Context {
		t.Helper()
		ctx, err := users.BindWorkspace(asUser(user), space)
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	old := bind(active, 2)
	oldClaims, _ := auth.ClaimsFromContext(old)
	if auth.HasAction(oldClaims, "dashboard.design") || !auth.HasAction(oldClaims, "report.review") {
		t.Fatalf("installation changed grants: %v", oldClaims.IAM.Actions)
	}
	var enabled bool
	err := users.pool.runtime.QueryRow(root, `SELECT dbhub_iam_enable_dashboard_matrix($1)`, active).Scan(&enabled)
	var denied *pgconn.PgError
	if !errors.As(err, &denied) || denied.Code != "42501" {
		t.Fatalf("restricted application DB role could activate a space: %v", err)
	}

	snapshot := func(space int64, sql string) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(root, sql, space).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	const assignmentFacts = `SELECT jsonb_agg(to_jsonb(a)-ARRAY['role_revision','version'] ORDER BY assignment_id)::text
 FROM sys_iam_assignments a WHERE workspace_id=$1 AND subject_id=2`
	const historicalFacts = `SELECT jsonb_agg(to_jsonb(a) ORDER BY assignment_id)::text
 FROM sys_iam_assignments a WHERE workspace_id=$1 AND subject_id=3`
	const neighborFacts = `SELECT to_jsonb(s)::text FROM sys_iam_workspace_state s WHERE workspace_id=$1`
	authorGrants, staleGrants, neighborState := snapshot(active, assignmentFacts), snapshot(active, historicalFacts), snapshot(neighbor, neighborFacts)
	for _, expected := range []bool{true, false} {
		if err := pool.QueryRow(root, `SELECT dbhub_iam_enable_dashboard_matrix($1)`, active).Scan(&enabled); err != nil || enabled != expected {
			t.Fatalf("enable result: %v %v, want %v", enabled, err, expected)
		}
	}
	if snapshot(active, assignmentFacts) != authorGrants || snapshot(active, historicalFacts) != staleGrants ||
		snapshot(neighbor, neighborFacts) != neighborState {
		t.Fatal("activation rewrote assignment identity/expiry, historical sources, or the neighboring workspace")
	}
	if err := users.IAMCurrent(old); !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("old request survived activation: %v", err)
	}
	for _, space := range []int64{active, neighbor} {
		ctx := bind(space, 2)
		claims, _ := auth.ClaimsFromContext(ctx)
		for _, action := range []string{"dashboard.page", "dashboard.view", "dashboard.design", "dashboard.submit", "dashboard.review", "dashboard.publish"} {
			if auth.HasAction(claims, action) != (space == active) {
				t.Fatalf("workspace %d action %s: %v", space, action, claims.IAM.Actions)
			}
		}
		var scoped bool
		if err := users.pool.QueryRow(ctx, `SELECT dbhub_iam_dashboard_matrix_enabled()`).Scan(&scoped); err != nil || scoped != (space == active) {
			t.Fatalf("runtime rollout flag in workspace %d: %v %v", space, scoped, err)
		}
		directory, err := users.IAMDirectMembers(root, space)
		if err != nil || len(directory.Roles) != 3 || slices.Contains(directory.Roles[0].Actions, "dashboard.design") != (space == active) {
			t.Fatalf("workspace %d role directory: %#v %v", space, directory.Roles, err)
		}
		if space == active {
			for _, member := range directory.Members {
				if member.UserID == 2 && (member.Version != author.Version || !slices.Equal(member.RoleCodes, author.RoleCodes)) {
					t.Fatalf("existing member required reassignment: before=%#v after=%#v", author, member)
				}
			}
		}
	}
	readonly, _ := auth.ClaimsFromContext(bind(active, 3))
	if auth.HasAction(readonly, "dashboard.view") {
		t.Fatalf("historical viewer source was revived: %v", readonly.IAM.Actions)
	}
	newGrant := grant(active, 3, "viewer")
	if newGrant.Version <= expiredMember.Version {
		t.Fatalf("new assignment did not advance the member version: %#v", newGrant)
	}
	readonly, _ = auth.ClaimsFromContext(bind(active, 3))
	if !auth.HasAction(readonly, "dashboard.view") || auth.HasAction(readonly, "dashboard.design") {
		t.Fatalf("new grant did not follow the active viewer revision: %v", readonly.IAM.Actions)
	}
	if events := snapshot(active, `SELECT count(*)::text FROM sys_iam_events WHERE workspace_id=$1 AND operation='matrix.dashboard.enable'`); events != "1" {
		t.Fatalf("activation evidence count = %s", events)
	}
}
