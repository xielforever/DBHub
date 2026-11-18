package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/dbhub/internal/auth"
)

func TestIAMRelationalNavigationMatchesOriginalAndCurrentQualification(t *testing.T) {
	pool, _, ctx := iamTestDatabase(t)
	original, e := migrationFS.ReadFile("migrations/0073_iam_identity_directory_join.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, strings.ReplaceAll(string(original), "dbhub_iam_identity_directory", "dbhub_iam_identity_directory_reference")); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO sys_workspaces(id,name) VALUES(2,'navigation parity other space');
	 INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES(2,2,'readonly')`); e != nil {
		t.Fatal(e)
	}
	cases := []struct{ name, mutation string }{
		{"unchanged", ""},
		{"member-inactive", `UPDATE sys_workspace_members SET active=false WHERE workspace_id=1 AND user_id=2`},
		{"member-expired", `UPDATE sys_workspace_members SET expires_at=statement_timestamp()-interval '1 second' WHERE user_id=2`},
		{"no-memberships", `DELETE FROM sys_workspace_members WHERE user_id=2`},
		{"user-inactive", `UPDATE sys_users SET is_active=false WHERE id=2`},
		{"space-disabled", `UPDATE sys_workspaces SET enabled=false WHERE id=1`},
		{"role-and-caps", `UPDATE sys_workspace_members SET role_code='readonly',capabilities=ARRAY['audit_view'] WHERE user_id=2`},
		{"password-session-version", `UPDATE sys_users SET auth_version=auth_version+1 WHERE id=2`},
		{"member-frozen", `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,2,'10000000-0000-0000-0000-000000000021','member_freeze','navigation parity')`},
		{"recovery", `WITH r AS(INSERT INTO sys_iam_recovery_requests(workspace_id,request_id,requester_id,target_id,reason,baseline_epoch,roster_version,expires_at,state) VALUES(1,'10000000-0000-0000-0000-000000000022',1,2,'navigation parity',1,'synthetic-only',statement_timestamp()+interval '1 hour','applied') RETURNING request_id) INSERT INTO sys_iam_recovery_grants(workspace_id,user_id,request_id,valid_until) SELECT 1,2,request_id,statement_timestamp()+interval '1 hour' FROM r`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, e := pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(context.Background())
			if tc.mutation != "" {
				if _, e = tx.Exec(ctx, tc.mutation); e != nil {
					t.Fatal(e)
				}
			}
			for _, name := range []string{"iam-admin", "developer", "readonly", "not-a-user"} {
				var before, compact, after []byte
				e = tx.QueryRow(ctx, `SELECT (SELECT to_jsonb(d) FROM dbhub_iam_identity_directory_reference($1)d),
				 (SELECT to_jsonb(d) FROM dbhub_iam_identity_directory($1)d),
				 (SELECT jsonb_build_object('id',n.id,'user_name',n.user_name,'data_role',n.data_role,
				 'password_hash',n.password_hash,'is_active',n.is_active,'last_login_at',n.last_login_at,'auth_version',n.auth_version,
				 'directory',COALESCE(jsonb_agg(jsonb_build_object('id',n.nav_id,'name',n.nav_name,'role',n.nav_role,
				 'allow_unknown_freshness',n.nav_allow_unknown,'membership_version',n.nav_membership_version,
				 'capabilities',n.nav_capabilities,'expires_at',n.nav_expires_at,'authorization_epoch',n.nav_authorization_epoch,
				 'config_version',n.nav_config_version,'permission_mode',n.nav_permission_mode) ORDER BY n.nav_id)
				 FILTER(WHERE n.nav_id IS NOT NULL),'[]'::jsonb),'has_recovery',bool_or(n.has_recovery))
				 FROM dbhub_iam_identity_navigation($1)n GROUP BY n.id,n.user_name,n.data_role,n.password_hash,
				 n.is_active,n.last_login_at,n.auth_version)`, name).Scan(&before, &compact, &after)
				if e != nil || string(before) != string(after) || string(before) != string(compact) {
					t.Fatal("navigation identity or directory differs from original", tc.name, name, e)
				}
			}
		})
	}
	var public, runtime bool
	if e := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_proc p,
	 LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a
	 WHERE p.oid='dbhub_iam_identity_navigation(text)'::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE'),
	 has_function_privilege($1,'dbhub_iam_identity_navigation(text)','EXECUTE')`, scopedPool(pool).runtime.Config().ConnConfig.User).Scan(&public, &runtime); e != nil || public || runtime {
		t.Fatal("private identity navigation became runtime-callable", public, runtime, e)
	}
}

func TestIAMRelationalNavigationTypedRowsPreserveEmptyDirectory(t *testing.T) {
	pool, users, ctx := iamTestDatabase(t)
	if _, e := pool.Exec(ctx, `DELETE FROM sys_workspace_members WHERE user_id=2`); e != nil {
		t.Fatal(e)
	}
	_, joined, e := users.FindIdentityAndWorkspaceDirectory(auth.WithClaims(ctx, &auth.Claims{Subject: 2, Role: "developer"}), "developer")
	if e != nil {
		t.Fatal(e)
	}
	spaces, e := users.WorkspacesDirectory(joined)
	b, _ := json.Marshal(spaces)
	if e != nil || string(b) != "[]" {
		t.Fatal("empty directory shape changed", string(b), e)
	}
}
