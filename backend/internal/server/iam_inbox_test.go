package server

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIAMInboxPlanKeepsPendingRowsAndExactReviewPermission(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	id, _ := f.draft("SELECT id,email FROM public." + f.view)
	f.submit(id, 1)
	if a.call("GET", "/inbox/todos", a.admin, nil, 200)["total"] != float64(1) {
		t.Fatal("legacy pending review missing")
	}
	ctx := context.Background()
	var runtimeRole string
	if err := a.pool.QueryRow(ctx, `SELECT rolname FROM pg_roles WHERE rolname LIKE 'dbhub_runtime_%' AND NOT rolsuper AND NOT rolbypassrls AND has_schema_privilege(rolname,current_schema(),'USAGE') ORDER BY oid DESC LIMIT 1`).Scan(&runtimeRole); err != nil {
		t.Fatal(err)
	}
	// The new invoker helper is callable by the restricted runtime. Its scalar
	// parameters must not let a caller impersonate an admin or invent review
	// rights over a pending review that is visible to its developer/author.
	func() {
		tx, err := a.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{runtimeRole}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT set_config('dbhub.workspace_id','1',true),set_config('dbhub.actor_id','2',true),set_config('dbhub.actor_role','developer',true),set_config('dbhub.runtime_bridge','phase1-bridge-2',true)`); err != nil {
			t.Fatal(err)
		}
		for _, principal := range []int64{1, 2} {
			var count int64
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM dbhub_inbox_todos($1,true)`, principal).Scan(&count); err != nil || count != 0 {
				t.Fatal("invoker parameters manufactured review authority", principal, count, err)
			}
		}
	}()
	role, assignment, bundle := testRequestID(), testRequestID(), testRequestID()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := a.pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Synthetic target setup only, following the database IAM fixture. This is
	// not a real migration approval or a new grant path in the application.
	exec(`INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by) SELECT 1,$1::uuid,1,'inbox fixture',ARRAY(SELECT action_id FROM sys_iam_capabilities WHERE scope='workspace' AND runtime_enabled),true,1`, role)
	exec(`INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled) VALUES(1,$1::uuid,1,'inbox fixture',true)`, role)
	exec(`INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id) VALUES(1,$1::uuid,$2::uuid,1,'user',1,ARRAY[1]::bigint[],now()+interval '1 hour',1,$3::uuid)`, assignment, role, testRequestID())
	exec(`INSERT INTO sys_iam_bundles(workspace_id,bundle_id,config_version,minimum_bridge,manifest,state,created_by) SELECT 1,$1::uuid,config_version+1,'phase1-bridge-2','{}','shadow',1 FROM sys_iam_workspace_state WHERE workspace_id=1`, bundle)
	exec(`INSERT INTO sys_iam_principal_modes(workspace_id,user_id,mode,confirmed,legacy_data_role,legacy_capabilities,envelope_ref,bundle_id) SELECT 1,user_id,'TARGET',true,role_code,capabilities,'{"fixture":"isolated inbox"}',$1::uuid FROM sys_workspace_members WHERE workspace_id=1 AND user_id=1`, bundle)
	exec(`UPDATE sys_iam_workspace_state SET mode='COMPAT_ACTIVE',activation_ready=true,active_bundle_id=$1::uuid,minimum_bridge='phase1-bridge-2' WHERE workspace_id=1`, bundle)
	todos := a.call("GET", "/inbox/todos", a.admin, nil, 200)
	if todos["total"] != float64(1) || todos["items"].([]any)[0].(map[string]any)["kind"] != "report_review" {
		t.Fatal("target plan lost the original pending review", todos)
	}
	if a.call("GET", "/inbox", a.admin, nil, 200)["todo_count"] != float64(1) {
		t.Fatal("notification count disagrees with pending list")
	}
	a.deps.UserRepo.ConfigureIAMIsolation("inbox-unit-only", a.deps.IAMRouteFingerprint)
	for _, kind := range []string{"api", "database", "worker"} {
		if err := a.deps.UserRepo.IAMHeartbeat(ctx, kind, a.deps.IAMRouteFingerprint); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,action_id,reason) VALUES(1,1,1,$1::uuid,'action_revoke','report.review','isolated exact review denial')`, testRequestID())
	if a.call("GET", "/inbox/todos", a.admin, nil, 200)["total"] != float64(0) {
		t.Fatal("dashboard review permission substituted for denied report review")
	}
	if a.call("GET", "/inbox", a.admin, nil, 200)["todo_count"] != float64(0) {
		t.Fatal("stale notification kept a denied review actionable")
	}
}
