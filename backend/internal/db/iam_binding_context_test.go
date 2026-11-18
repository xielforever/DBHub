package db

import (
	"context"
	"strings"
	"testing"
)

func TestIAMDirectBindingMatchesOriginalAcrossQualificationAndSpaces(t *testing.T) {
	for _, mode := range []string{"legacy", "target", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			pool, users, ctx := iamTestDatabase(t)
			// Retain the original implementation as a private test oracle. Its
			// directory/source helpers are unchanged, so every returned field is
			// compared, including the old floor, current epochs and finite sources.
			original, e := migrationFS.ReadFile("migrations/0067_iam_single_statement_binding.sql")
			if e != nil {
				t.Fatal(e)
			}
			reference := strings.ReplaceAll(string(original), "dbhub_iam_bind_workspace", "dbhub_iam_bind_workspace_reference")
			if _, e = pool.Exec(ctx, reference); e != nil {
				t.Fatal(e)
			}
			if _, e = pool.Exec(ctx, `INSERT INTO sys_workspaces(id,name) VALUES(2,'other-space binding');INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES(2,2,'readonly')`); e != nil {
				t.Fatal(e)
			}
			if mode != "legacy" {
				for _, uid := range []int64{2, 3} {
					iamTargetFixture(t, pool, users, uid, []string{"report.page", "report.view", "query.page", "query.read", "query.write"})
				}
				if mode == "rollback" {
					if _, e = pool.Exec(ctx, `UPDATE sys_iam_principal_modes SET mode='LEGACY_GUARDED',rollback_guard=true`); e != nil {
						t.Fatal(e)
					}
				}
			}
			users.ConfigureIAMIsolation("unit-only", "binding-context-parity")
			for _, kind := range []string{"api", "database", "worker"} {
				if e = users.IAMHeartbeat(ctx, kind, "binding-context-parity"); e != nil {
					t.Fatal(e)
				}
			}
			cases := []struct{ name, mutation string }{
				{"unchanged", ""},
				{"member-inactive", `UPDATE sys_workspace_members SET active=false WHERE workspace_id=1 AND user_id=2`},
				{"member-expired", `UPDATE sys_workspace_members SET expires_at=statement_timestamp()-interval '1 second' WHERE workspace_id=1 AND user_id=2`},
				{"user-inactive", `UPDATE sys_users SET is_active=false WHERE id=2`},
				{"space-disabled", `UPDATE sys_workspaces SET enabled=false WHERE id=1`},
				{"lower-data-floor", `UPDATE sys_workspace_members SET role_code='readonly' WHERE workspace_id=1 AND user_id=2`},
				{"profile-expired", `UPDATE sys_iam_principal_modes SET valid_until=statement_timestamp()-interval '1 second' WHERE user_id=2`},
				{"source-expired", `UPDATE sys_iam_assignments SET valid_from=statement_timestamp()-interval '2 seconds',valid_until=statement_timestamp()-interval '1 second' WHERE subject_id=2`},
				{"role-disabled", `UPDATE sys_iam_role_heads SET enabled=false WHERE role_id IN(SELECT role_id FROM sys_iam_assignments WHERE subject_id=2)`},
				{"member-frozen", `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,2,'10000000-0000-0000-0000-000000000011','member_freeze','binding context parity')`},
				{"action-prohibited", `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,action_id,reason) VALUES(1,1,2,'10000000-0000-0000-0000-000000000012','action_revoke','report.view','binding context parity')`},
				{"recovery-only", `WITH r AS(INSERT INTO sys_iam_recovery_requests(workspace_id,request_id,requester_id,target_id,reason,baseline_epoch,roster_version,expires_at,state) VALUES(1,'10000000-0000-0000-0000-000000000013',1,2,'binding parity',1,'synthetic-only',statement_timestamp()+interval '1 hour','applied') RETURNING request_id) INSERT INTO sys_iam_recovery_grants(workspace_id,user_id,request_id,valid_until) SELECT 1,2,request_id,statement_timestamp()+interval '1 hour' FROM r`},
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
					for _, uid := range []int64{1, 2, 3, 999} {
						for _, space := range []int64{1, 2, 999} {
							var equal bool
							e = tx.QueryRow(ctx, `SELECT (SELECT to_jsonb(r) FROM dbhub_iam_bind_workspace_reference($1,$2)r) IS NOT DISTINCT FROM (SELECT to_jsonb(r) FROM dbhub_iam_bind_workspace($1,$2)r)`, uid, space).Scan(&equal)
							if e != nil || !equal {
								t.Fatal("direct binding differs from original", mode, tc.name, uid, space, e)
							}
						}
					}
				})
			}
		})
	}
}
