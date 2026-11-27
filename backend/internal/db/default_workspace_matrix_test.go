package db

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
)

const defaultMatrixMigration = "0103_default_workspace_matrix.sql"

func iamTestDatabaseBeforeDefaultMatrix(t *testing.T) (*pgxpool.Pool, *UserRepository, context.Context) {
	t.Helper()
	return iamTestDatabasePrepared(t, func(ctx context.Context, pool *pgxpool.Pool) {
		// Apply the real upgrade after recording the previous schema's members.
		if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (
			version VARCHAR(255) PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
			INSERT INTO schema_migrations(version) VALUES($1)`, defaultMatrixMigration); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDefaultWorkspaceMatrixUpgrade(t *testing.T) {
	pool, users, ctx := iamTestDatabaseBeforeDefaultMatrix(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO sys_workspaces(id,name) VALUES(2,'legacy neighbor');
		INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES(2,2,'developer');
		INSERT INTO sys_users(username,password_hash,role_id)
		SELECT 'restricted-developer','fixture-hash',id FROM sys_roles WHERE code='developer';
		INSERT INTO sys_users(username,password_hash,role_id)
		SELECT 'expired-reader','fixture-hash',id FROM sys_roles WHERE code='readonly';
		INSERT INTO sys_users(username,password_hash,role_id)
		SELECT 'frozen-reader','fixture-hash',id FROM sys_roles WHERE code='readonly';
		UPDATE sys_workspace_members SET expires_at=now()+interval '1 hour' WHERE workspace_id=1 AND user_id=2;
		UPDATE sys_workspace_members SET expires_at=now()-interval '1 hour' WHERE workspace_id=1 AND user_id=5;
		INSERT INTO sys_iam_runtime_agents(workspace_id,agent_id,kind,bridge_version,route_fingerprint)
		SELECT 1,kind,kind,'phase1-bridge-2','isolated-fixture' FROM unnest(ARRAY['api','worker','database']) kind;
		INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,action_id,reason)
		VALUES(1,1,4,gen_random_uuid(),'action_revoke','report.submit','fixture denied action');
		INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason)
		VALUES(1,1,6,gen_random_uuid(),'member_freeze','fixture frozen membership');
		INSERT INTO sys_user_groups(id,workspace_id,name) VALUES(101,1,'unchanged group');
		INSERT INTO sys_user_group_members(group_id,user_id,workspace_id) VALUES(101,2,1);
		INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by)
		VALUES(1,'c5575f9e-c271-4c12-8ae2-8e8715f83eec',1,'Group fixture',ARRAY['report.view'],true,1);
		INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,
			approved_members,valid_until,granted_by,proposal_id)
		VALUES(1,gen_random_uuid(),'c5575f9e-c271-4c12-8ae2-8e8715f83eec',1,'group',101,ARRAY[2]::bigint[],now()+interval '1 day',1,gen_random_uuid())`)
	exec(`DELETE FROM schema_migrations WHERE version=$1`, defaultMatrixMigration)
	if applied, err := Migrate(ctx, pool); err != nil || !slices.Equal(applied, []string{defaultMatrixMigration}) {
		t.Fatalf("default workspace migration mismatch: %v %v", applied, err)
	}
	var defaultMatrix, neighborMatrix bool
	var adminRole string
	var groupGrants, auditEvents int
	if err := pool.QueryRow(ctx, `SELECT (SELECT matrix_enabled FROM sys_iam_workspace_state WHERE workspace_id=1),
		(SELECT matrix_enabled FROM sys_iam_workspace_state WHERE workspace_id=2),
		(SELECT role_code FROM sys_workspace_members WHERE workspace_id=1 AND user_id=1),
		(SELECT count(*) FROM sys_iam_assignments a JOIN sys_user_group_members gm
		 ON a.workspace_id=gm.workspace_id AND a.subject_id=gm.group_id AND gm.user_id=ANY(a.approved_members)
		 WHERE a.workspace_id=1 AND a.subject_type='group' AND a.subject_id=101 AND a.revoked_at IS NULL),
		(SELECT count(*) FROM sys_iam_events WHERE workspace_id=1 AND operation='matrix.default_workspace.enable')`).Scan(
		&defaultMatrix, &neighborMatrix, &adminRole, &groupGrants, &auditEvents); err != nil {
		t.Fatal(err)
	}
	if !defaultMatrix || neighborMatrix || adminRole != "admin" || groupGrants != 1 || auditEvents != 1 {
		t.Fatalf("unexpected cutover state: default=%v neighbor=%v admin=%s groups=%d audit=%d", defaultMatrix, neighborMatrix, adminRole, groupGrants, auditEvents)
	}
	for _, expect := range []struct {
		id    int64
		roles []string
	}{{2, []string{"business_it"}}, {3, []string{"viewer"}}, {4, []string{"viewer"}}, {5, nil}, {6, nil}} {
		var direct, withinMemberExpiry bool
		var codes []string
		if err := pool.QueryRow(ctx, `SELECT m.direct_role_managed,ARRAY(
			SELECT t.code FROM sys_iam_assignments a JOIN sys_iam_builtin_roles t USING(role_id)
			WHERE a.workspace_id=m.workspace_id AND a.subject_type='user' AND a.subject_id=m.user_id AND a.revoked_at IS NULL
			ORDER BY t.code),NOT EXISTS(SELECT 1 FROM sys_iam_assignments a WHERE a.workspace_id=m.workspace_id
			AND a.subject_type='user' AND a.subject_id=m.user_id AND a.valid_until>m.expires_at)
			FROM sys_workspace_members m WHERE m.workspace_id=1 AND m.user_id=$1`, expect.id).Scan(&direct, &codes, &withinMemberExpiry); err != nil {
			t.Fatal(err)
		}
		if !direct || !slices.Equal(codes, expect.roles) || !withinMemberExpiry {
			t.Fatalf("unexpected role conversion for %d: direct=%v roles=%v expiry=%v", expect.id, direct, codes, withinMemberExpiry)
		}
	}
	for _, expect := range []struct {
		id            int64
		allowedAction string
		deniedAction  string
	}{{2, "query.read", "report.review"}, {3, "report.view", "report.design"}, {4, "report.view", "report.submit"}} {
		bound, err := users.BindWorkspace(context.WithValue(ctx, auth.ClaimsContextKey, &auth.Claims{Subject: expect.id}), 1)
		if err != nil {
			t.Fatal(err)
		}
		claims, _ := auth.ClaimsFromContext(bound)
		if !claims.IAM.Target || !auth.HasAction(claims, expect.allowedAction) || auth.HasAction(claims, expect.deniedAction) {
			t.Fatalf("unexpected effective actions for converted member %d", expect.id)
		}
	}
	if applied, err := Migrate(ctx, pool); err != nil || len(applied) != 0 {
		t.Fatalf("repeated migration changed applied versions: %v %v", applied, err)
	}
}

func TestDefaultWorkspaceMatrixKeepsApprovedGroupSource(t *testing.T) {
	pool, users, ctx := iamTestDatabaseBeforeDefaultMatrix(t)
	_, assignment := iamTargetFixture(t, pool, users, 3, []string{"query.page", "query.read"})
	if _, err := pool.Exec(ctx, `INSERT INTO sys_user_groups(id,workspace_id,name) VALUES(101,1,'approved group');
		INSERT INTO sys_user_group_members(group_id,user_id,workspace_id) VALUES(101,3,1);
		UPDATE sys_iam_assignments SET subject_type='group',subject_id=101 WHERE workspace_id=1 AND assignment_id=$1::uuid`, assignment); err != nil {
		t.Fatal(err)
	}
	assertEffective := func() {
		t.Helper()
		claims, _ := auth.ClaimsFromContext(iamBound(t, users, 3))
		if !claims.IAM.Target || !auth.HasAction(claims, "query.read") || auth.HasAction(claims, "query.write") {
			t.Fatalf("the existing group grant changed effective query permissions: %+v", claims.IAM)
		}
	}
	assertEffective()
	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, defaultMatrixMigration); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	assertEffective()
}

func TestFreshDefaultWorkspaceUsesMatrix(t *testing.T) {
	pool, _, ctx := iamTestDatabase(t)
	var enabled bool
	var role string
	if err := pool.QueryRow(ctx, `SELECT s.matrix_enabled,m.role_code FROM sys_iam_workspace_state s
		JOIN sys_workspace_members m USING(workspace_id) JOIN sys_users u ON u.id=m.user_id
		WHERE s.workspace_id=1 AND u.username='iam-admin' AND u.is_platform_admin`).Scan(&enabled, &role); err != nil {
		t.Fatal(err)
	}
	if !enabled || role != "admin" {
		t.Fatalf("fresh default workspace must enable matrix and retain bootstrap administrator: enabled=%v role=%s", enabled, role)
	}
}
