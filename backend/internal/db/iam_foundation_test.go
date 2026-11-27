package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

func iamTestDatabase(t *testing.T) (*pgxpool.Pool, *UserRepository, context.Context) {
	t.Helper()
	return iamTestDatabasePrepared(t, nil)
}

func iamTestDatabasePrepared(t *testing.T, prepare func(context.Context, *pgxpool.Pool)) (*pgxpool.Pool, *UserRepository, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_METADATA_DSN")
	if dsn == "" {
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		t.Skip("isolated PostgreSQL not configured")
	}
	ctx := context.WithValue(WithWorkspace(context.Background(), 1), auth.ClaimsContextKey, &auth.Claims{Subject: 1, Role: "admin"})
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("iam_foundation_%d", time.Now().UnixNano())
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["dbhub.workspace_id"] = "1"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		CloseScopedRuntime(pool)
		pool.Close()
		_, _ = root.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	})
	if prepare != nil {
		prepare(ctx, pool)
	}
	if _, e = Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	u := NewUserRepository(pool)
	if e = u.EnsureAdmin(ctx, "iam-admin", "opaque-fixture-hash"); e != nil {
		t.Fatal(e)
	}
	for _, role := range []string{"developer", "readonly"} {
		if _, e = pool.Exec(ctx, `INSERT INTO sys_users(username,password_hash,role_id) SELECT $1,'opaque-fixture-hash',id FROM sys_roles WHERE code=$1`, role); e != nil {
			t.Fatal(e)
		}
	}
	return pool, u, ctx
}

func TestIAMGoSQLCatalogAndLegacyParity(t *testing.T) {
	pool, u, ctx := iamTestDatabase(t)
	catalog := iam.ReadCatalog()
	var raw []byte
	if e := pool.QueryRow(ctx, `SELECT jsonb_agg(jsonb_build_object('id',action_id,'scope',scope,'dependencies',dependencies,'state',implementation_state,'legacy_roles',legacy_roles,'legacy_capabilities',legacy_capabilities) ORDER BY action_id) FROM sys_iam_capabilities`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var actual []iam.Capability
	if e := json.Unmarshal(raw, &actual); e != nil {
		t.Fatal(e)
	}
	sort.Slice(catalog.Capabilities, func(i, j int) bool { return catalog.Capabilities[i].ID < catalog.Capabilities[j].ID })
	if !reflect.DeepEqual(actual, catalog.Capabilities) {
		t.Fatal("SQL catalog differs from Go frozen catalog")
	}
	for _, menu := range catalog.Menus {
		var got iam.Menu
		if e := pool.QueryRow(ctx, `SELECT menu_id,label,category,action_id,route FROM sys_iam_menus WHERE menu_id=$1`, menu.ID).Scan(&got.ID, &got.Label, &got.Group, &got.Capability, &got.Path); e != nil || got != menu {
			t.Fatal("SQL menu differs", menu.ID, e)
		}
	}
	old := []string{"report_review", "datasource_manage", "audit_view", "operation_review"}
	comparisons := 0
	for userID, role := range map[int64]string{1: "admin", 2: "developer", 3: "readonly"} {
		for mask := 0; mask < 16; mask++ {
			caps := []string{}
			for i, c := range old {
				if mask&(1<<i) != 0 {
					caps = append(caps, c)
				}
			}
			if _, e := pool.Exec(ctx, `UPDATE sys_workspace_members SET capabilities=$1 WHERE workspace_id=1 AND user_id=$2`, caps, userID); e != nil {
				t.Fatal(e)
			}
			identity := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{Subject: userID, Role: "untrusted-token-role"})
			bound, e := u.BindWorkspace(identity, 1)
			if e != nil {
				t.Fatal(e)
			}
			claims, _ := auth.ClaimsFromContext(bound)
			if claims.Role != role || claims.IAM.LegacyDataRole != role {
				t.Fatal("current legacy data identity changed")
			}
			for _, cap := range catalog.Capabilities {
				var sqlAllowed bool
				if e = pool.QueryRow(ctx, `SELECT dbhub_iam_legacy_action($1,$2)`, userID, cap.ID).Scan(&sqlAllowed); e != nil {
					t.Fatal(e)
				}
				goAllowed := iam.ExplainLegacy(*claims.IAM, cap.ID, time.Now()).Allowed
				if sqlAllowed != goAllowed {
					t.Fatalf("Go/SQL mismatch user=%d mask=%d action=%s go=%v sql=%v", userID, mask, cap.ID, goAllowed, sqlAllowed)
				}
				comparisons++
			}
			for _, name := range append(append([]string{}, old...), "report.authorize", "future.manage") {
				var allowed bool
				if e = pool.QueryRow(ctx, `SELECT dbhub_has_capability($1,$2)`, userID, name).Scan(&allowed); e != nil {
					t.Fatal(e)
				}
				if allowed != auth.HasCapability(claims, name) {
					t.Fatal("legacy Go/SQL mismatch", role, name)
				}
			}
		}
	}
	t.Logf("Go/SQL functional comparisons: %d; data/object boundaries remain separate", comparisons)
}

func TestIAMRuntimeCannotGrantOrCrossWorkspace(t *testing.T) {
	pool, u, ctx := iamTestDatabase(t)
	bound, e := u.BindWorkspace(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	scoped := scopedPool(pool)
	for _, table := range []string{"sys_iam_capabilities", "sys_iam_menus", "sys_iam_workspace_state", "sys_iam_role_revisions", "sys_iam_assignments", "sys_iam_delegations", "sys_iam_proposals", "sys_iam_bundles", "sys_iam_legacy_envelopes", "sys_iam_revocations"} {
		if _, e = scoped.Exec(bound, "DELETE FROM "+pgx.Identifier{table}.Sanitize()); e == nil {
			t.Fatal("runtime can mutate governance", table)
		}
	}
	w, e := u.CreateWorkspace(ctx, "other-space")
	if e != nil {
		t.Fatal(e)
	}
	other, e := u.BindWorkspace(ctx, w.ID)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 8; i++ {
		var id int64
		if e = scoped.QueryRow(bound, `SELECT workspace_id FROM sys_iam_workspace_state`).Scan(&id); e != nil || id != 1 {
			t.Fatal("pool leaked another workspace", id, e)
		}
		if e = scoped.QueryRow(other, `SELECT workspace_id FROM sys_iam_workspace_state`).Scan(&id); e != nil || id != w.ID {
			t.Fatal("pool lost original scope", id, e)
		}
	}
	// Definer helpers must keep their fixed schema even in the presence of
	// caller-controlled temporary objects.
	tx, e := scoped.Begin(bound)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `CREATE TEMP TABLE sys_iam_capabilities(action_id TEXT); INSERT INTO sys_iam_capabilities VALUES('future.manage')`); e != nil {
		t.Fatal(e)
	}
	var allowed bool
	if e = tx.QueryRow(ctx, `SELECT dbhub_iam_legacy_action(1,'future.manage')`).Scan(&allowed); e != nil || allowed {
		t.Fatal("temporary schema changed authorization", e)
	}
}

func TestIAMRevocationExpiryAndRollbackGenerations(t *testing.T) {
	pool, u, ctx := iamTestDatabase(t)
	w, e := u.CreateWorkspace(ctx, "unaffected-space")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES($1,2,'developer')`, w.ID); e != nil {
		t.Fatal(e)
	}
	identity := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{Subject: 2, Role: "admin"})
	old, e := u.BindWorkspace(identity, 1)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := auth.ClaimsFromContext(old)
	if before.Role != "developer" {
		t.Fatal("JWT role was trusted")
	}
	if _, e = pool.Exec(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,2,'22222222-2222-4222-8222-222222222222','member_freeze','isolated test')`); e != nil {
		t.Fatal(e)
	}
	if e = requireCurrentActor(old, scopedPool(pool)); e == nil {
		t.Fatal("old context survived acknowledged freeze")
	}
	if _, e = u.BindWorkspace(identity, 1); e == nil {
		t.Fatal("frozen member re-bound")
	}
	if _, e = u.BindWorkspace(identity, w.ID); e != nil {
		t.Fatal("freeze escaped the original workspace", e)
	}
	for _, sql := range []string{`DELETE FROM sys_iam_revocations`, `TRUNCATE sys_iam_revocations`, `UPDATE sys_iam_revocations SET reason='erased'`, `UPDATE sys_iam_workspace_state SET authorization_epoch=0 WHERE workspace_id=1`, `UPDATE sys_iam_workspace_state SET mode='COMPAT_ACTIVE' WHERE workspace_id=1`, `UPDATE sys_iam_workspace_state SET activation_ready=true WHERE workspace_id=1`, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason,action_id) VALUES(1,1,3,'55555555-5555-4555-8555-555555555555','action_revoke','incomplete bridge','report.view')`} {
		if _, e = pool.Exec(ctx, sql); e == nil {
			t.Fatal("unsafe rollback or premature activation accepted", sql)
		}
	}
	if _, e = pool.Exec(ctx, `UPDATE sys_iam_workspace_state SET mode='SHADOW' WHERE workspace_id=1; UPDATE sys_iam_workspace_state SET mode='LEGACY_GUARDED' WHERE workspace_id=1; UPDATE sys_workspace_members SET active=true WHERE workspace_id=1 AND user_id=2`); e != nil {
		t.Fatal(e)
	}
	if _, e = u.BindWorkspace(identity, 1); e == nil {
		t.Fatal("mode rollback resurrected frozen member")
	}
	var config, epoch int64
	if e = pool.QueryRow(ctx, `SELECT config_version,authorization_epoch FROM sys_iam_workspace_state WHERE workspace_id=1`).Scan(&config, &epoch); e != nil || config != 3 || epoch <= before.IAM.AuthorizationEpoch {
		t.Fatal("generation rewound", config, epoch, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE sys_workspace_members SET expires_at=statement_timestamp() WHERE workspace_id=1 AND user_id=3`); e != nil {
		t.Fatal(e)
	}
	ro := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{Subject: 3, Role: "readonly"})
	if _, e = u.BindWorkspace(ro, 1); e == nil {
		t.Fatal("expired membership accepted")
	}
	if _, e = pool.Exec(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,1,'33333333-3333-4333-8333-333333333333','member_freeze','last-governor freeze test')`); e != nil {
		t.Fatal("last administrator blocked freeze", e)
	}
	if _, e = u.BindWorkspace(ctx, 1); e == nil {
		t.Fatal("last administrator retained space access")
	}
}

// An actual restored local metadata snapshot is upgraded in an isolated DB.
// Digests cover the original column set; no sensitive values leave this test.
func TestIAMUpgradeFromRestoredBaseline(t *testing.T) {
	dsn := os.Getenv("TEST_IAM_RESTORE_DSN")
	if dsn == "" {
		t.Skip("protected metadata restore not configured")
	}
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	defer CloseScopedRuntime(pool)
	var already bool
	if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version='0047_iam_foundation.sql')`).Scan(&already); e != nil {
		t.Fatal(e)
	}
	if already {
		t.Skip("restore drill already upgraded; recreate baseline_restore from the protected dump to repeat")
	}
	rows, e := pool.Query(ctx, `SELECT table_name,column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name LIKE 'sys_%' ORDER BY table_name,ordinal_position`)
	if e != nil {
		t.Fatal(e)
	}
	columns := map[string][]string{}
	for rows.Next() {
		var table, column string
		if e = rows.Scan(&table, &column); e != nil {
			t.Fatal(e)
		}
		columns[table] = append(columns[table], column)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	digest := func(table string, cols []string) string {
		t.Helper()
		names := make([]string, len(cols))
		for i, n := range cols {
			names[i] = pgx.Identifier{n}.Sanitize()
		}
		selection := ""
		for i, n := range names {
			if i > 0 {
				selection += ","
			}
			selection += n
		}
		r, e := pool.Query(ctx, "SELECT row_to_json(original)::text FROM (SELECT "+selection+" FROM "+pgx.Identifier{table}.Sanitize()+") original ORDER BY 1")
		if e != nil {
			t.Fatal(e)
		}
		defer r.Close()
		h := sha256.New()
		for r.Next() {
			var value string
			if e = r.Scan(&value); e != nil {
				t.Fatal(e)
			}
			h.Write([]byte(value))
			h.Write([]byte{0})
		}
		if e = r.Err(); e != nil {
			t.Fatal(e)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	before := map[string]string{}
	for table, cols := range columns {
		before[table] = digest(table, cols)
	}
	applied, e := Migrate(ctx, pool)
	if e != nil || !reflect.DeepEqual(applied, []string{"0047_iam_foundation.sql", "0048_iam_runtime_bridge.sql", "0049_iam_governance.sql", "0050_iam_execution_versions.sql", "0051_iam_shadow.sql", "0052_iam_worker_qualification.sql", "0053_iam_legacy_mutation_barrier.sql", "0054_iam_restriction_settlement.sql", "0055_iam_current_action_lookup.sql", "0056_iam_identity_lookup.sql", "0057_iam_dependency_filter.sql", "0058_iam_private_identity.sql", "0059_iam_task_version_guard.sql", "0060_iam_reuse_checked_plans.sql", "0061_iam_task_retry_guard.sql", "0062_iam_disabled_future_actions.sql", "0063_iam_workspace_read_plans.sql", "0064_iam_binding_read_plan.sql", "0065_iam_action_release_approval.sql", "0066_iam_current_read_plans.sql", "0067_iam_single_statement_binding.sql", "0068_iam_exact_governance_denials.sql", "0069_iam_inbox_read_plan.sql", "0070_iam_connection_visibility_plan.sql", "0071_iam_admission_read_plan.sql", "0072_iam_target_current_plan.sql", "0073_iam_identity_directory_join.sql", "0074_iam_generic_read_plans.sql", "0075_iam_direct_binding_context.sql", "0076_iam_restricted_runtime_fence.sql", "0077_iam_parameterized_connection_plans.sql", "0078_iam_relational_navigation.sql", "0079_iam_navigation_read_plan.sql", "0080_iam_compact_navigation_read.sql", "0081_iam_navigation_directory_plan.sql", "0082_query_execution_cancellation.sql", "0083_business_identity_datasets.sql", "0084_business_organization_scopes.sql", "0085_business_row_column_policies.sql", "0086_business_output_policies.sql", "0087_isolated_business_reports.sql", "0088_isolated_report_reviews.sql", "0089_isolated_dashboard_reviews.sql", "0090_isolated_content_availability.sql", "0091_isolated_content_custody.sql", "0092_isolated_custody_rejoins.sql", "0093_isolated_formal_exports.sql", "0094_isolated_subscription_reviews.sql", "0095_isolated_recipient_delivery.sql", "0096_delivery_retention_schedule.sql", "0097_dashboard_delivery.sql"}) {
		t.Fatal("restored baseline upgrade failed", applied, e)
	}
	for table, cols := range columns {
		if digest(table, cols) != before[table] {
			t.Fatal("upgrade changed original stored values", table)
		}
	}
	if again, e := Migrate(ctx, pool); e != nil || len(again) != 0 {
		t.Fatal("upgrade not idempotent", again, e)
	}
	t.Logf("Protected metadata restored and upgraded; %d original table/view column sets unchanged", len(columns))
}
