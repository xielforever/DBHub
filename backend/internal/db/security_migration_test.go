package db

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSecurityUpgradePreservesHistoryAndInvalidatesUnsafeApprovals(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real PostgreSQL not configured")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	schema := fmt.Sprintf("security_upgrade_%d", time.Now().UnixNano())
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["dbhub.workspace_id"] = "1"
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	defer CloseScopedRuntime(pool)
	if _, err = pool.Exec(ctx, `CREATE TABLE schema_migrations(version varchar(255) PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= "0037_" {
			continue
		}
		raw, _ := migrationFS.ReadFile("migrations/" + entry.Name())
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatal(entry.Name(), err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.Name()); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO sys_users(username,password_hash,role_id) SELECT 'owner','test-only-hash',id FROM sys_roles WHERE code='admin'`)
	exec(`INSERT INTO sys_users(username,password_hash,role_id) SELECT 'reviewer','test-only-hash',id FROM sys_roles WHERE code='admin'`)
	exec(`INSERT INTO sys_source_families(family_id) VALUES('11111111-1111-1111-1111-111111111111')`)
	exec(`INSERT INTO sys_source_definitions(family_id,revision,name,owner_id,visibility,config) VALUES('11111111-1111-1111-1111-111111111111',1,'old source',1,'private','{}')`)
	exec(`INSERT INTO sys_ssh_tunnels(user_id,name,host,username,auth_type,host_key) VALUES(1,'old tunnel','fixture.invalid','test','password','SHA256:fixture')`)
	exec(`INSERT INTO sys_proxies(user_id,name,host,port) VALUES(1,'old proxy','fixture.invalid',1080)`)
	exec(`INSERT INTO sys_connections(user_id,name,type,host,port,definition_revision_id,definition_family_id,ssh_tunnel_id,proxy_id)
 VALUES(1,'old binding','postgres','fixture.invalid',5432,1,'11111111-1111-1111-1111-111111111111',1,1)`)
	for _, status := range []string{"pending", "approved", "executing", "executed", "failed"} {
		exec(`INSERT INTO sys_tickets(title,type,table_name,connection_id,sql_text,risk,status,requester_id,approved_by)
 VALUES($1,'dml','fixture',1,'UPDATE fixture SET id=1 WHERE id=2','low',$1,1,2)`, status)
	}
	exec(`INSERT INTO sys_export_requests(user_id,connection_id,format,row_count,reason,payload,status,reviewed_by)
 VALUES(1,1,'json',1,'self','{}','approved',1),(1,1,'json',1,'independent','{}','approved',2)`)
	applied, err := Migrate(ctx, pool)
	// The old assertion predated the 0038–0046 baseline additions. Keep an
	// independent explicit list; all historical approval checks below remain.
	expected := []string{"0037_security_execution_boundaries.sql", "0038_report_parameters.sql", "0039_dashboard_filter_values.sql", "0040_report_exports.sql", "0041_report_chart_types.sql", "0042_workspace_capabilities.sql", "0043_identity_security.sql", "0044_external_identity_revocation.sql", "0045_report_subscriptions.sql", "0046_business_operations.sql", "0047_iam_foundation.sql", "0048_iam_runtime_bridge.sql", "0049_iam_governance.sql", "0050_iam_execution_versions.sql", "0051_iam_shadow.sql", "0052_iam_worker_qualification.sql", "0053_iam_legacy_mutation_barrier.sql", "0054_iam_restriction_settlement.sql", "0055_iam_current_action_lookup.sql", "0056_iam_identity_lookup.sql", "0057_iam_dependency_filter.sql", "0058_iam_private_identity.sql", "0059_iam_task_version_guard.sql", "0060_iam_reuse_checked_plans.sql", "0061_iam_task_retry_guard.sql", "0062_iam_disabled_future_actions.sql", "0063_iam_workspace_read_plans.sql", "0064_iam_binding_read_plan.sql", "0065_iam_action_release_approval.sql", "0066_iam_current_read_plans.sql", "0067_iam_single_statement_binding.sql", "0068_iam_exact_governance_denials.sql", "0069_iam_inbox_read_plan.sql", "0070_iam_connection_visibility_plan.sql", "0071_iam_admission_read_plan.sql", "0072_iam_target_current_plan.sql", "0073_iam_identity_directory_join.sql", "0074_iam_generic_read_plans.sql", "0075_iam_direct_binding_context.sql", "0076_iam_restricted_runtime_fence.sql", "0077_iam_parameterized_connection_plans.sql"}
	expected = append(expected, "0078_iam_relational_navigation.sql", "0079_iam_navigation_read_plan.sql", "0080_iam_compact_navigation_read.sql", "0081_iam_navigation_directory_plan.sql", "0082_query_execution_cancellation.sql", "0083_business_identity_datasets.sql", "0084_business_organization_scopes.sql", "0085_business_row_column_policies.sql", "0086_business_output_policies.sql", "0087_isolated_business_reports.sql", "0088_isolated_report_reviews.sql", "0089_isolated_dashboard_reviews.sql", "0090_isolated_content_availability.sql", "0091_isolated_content_custody.sql", "0092_isolated_custody_rejoins.sql", "0093_isolated_formal_exports.sql", "0094_isolated_subscription_reviews.sql", "0095_isolated_recipient_delivery.sql", "0096_delivery_retention_schedule.sql", "0097_dashboard_delivery.sql")
	if err != nil || !slices.Equal(applied, expected) {
		t.Fatal("security upgrade failed", applied, err)
	}
	var records, invalidated, historical int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE title IN('pending','approved') AND status='rejected' AND execution_error LIKE '%重新提交%'),count(*) FILTER(WHERE title IN('executing','executed','failed') AND status=title) FROM sys_tickets`).Scan(&records, &invalidated, &historical); err != nil || records != 5 || invalidated != 2 || historical != 3 {
		t.Fatal("upgrade lost history or reused an unbound approval", records, invalidated, historical, err)
	}
	var exports int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM sys_export_requests WHERE (reason='self' AND status='pending' AND reviewed_by IS NULL) OR (reason='independent' AND status='approved' AND reviewed_by=2)`).Scan(&exports); err != nil || exports != 2 {
		t.Fatal("export decisions were not migrated safely", exports, err)
	}
	for _, table := range []string{"sys_ssh_tunnels", "sys_proxies"} {
		if _, err = pool.Exec(ctx, "DELETE FROM "+table+" WHERE id=1"); err == nil {
			t.Fatal("metadata owner bypassed in-use route protection", table)
		}
	}
	var unsafeFKs int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='sys_connections'::regclass AND confrelid IN('sys_ssh_tunnels'::regclass,'sys_proxies'::regclass) AND confdeltype<>'r'`).Scan(&unsafeFKs); err != nil || unsafeFKs != 0 {
		t.Fatal("an original or composite SET NULL route survived", unsafeFKs, err)
	}
	if again, err := Migrate(ctx, pool); err != nil || len(again) != 0 {
		t.Fatal("upgrade is not idempotent", again, err)
	}
}
