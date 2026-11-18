#!/bin/bash
set -euo pipefail
export PGHOST=metadata PGUSER=iam_test PGDATABASE=iam_test
case "${1:-}" in
 clone)
  test "$(psql -Atc "SELECT count(*) FROM pg_namespace WHERE nspname='iam_mixed_versions'")" = 0
  pg_dump --schema=iam_perf_baseline --no-owner --no-privileges | sed 's/iam_perf_baseline/iam_mixed_versions/g' | psql -q -v ON_ERROR_STOP=1
  ;;
 target)
  psql -v ON_ERROR_STOP=1 <<'SQL'
SET search_path=iam_mixed_versions;
BEGIN;
DO $$BEGIN
 IF current_database()<>'iam_test' OR NOT EXISTS(SELECT 1 FROM schema_migrations WHERE version='0064_iam_binding_read_plan.sql')
 THEN RAISE EXCEPTION 'fresh isolated mixed-version schema at 0064 required';END IF;
END$$;
-- Raw initialization is a synthetic test fixture, never governance approval.
INSERT INTO sys_iam_bundles SELECT * FROM iam_perf_candidate_v2.sys_iam_bundles WHERE workspace_id=1;
INSERT INTO sys_iam_role_revisions SELECT * FROM iam_perf_candidate_v2.sys_iam_role_revisions WHERE workspace_id=1;
INSERT INTO sys_iam_role_heads SELECT * FROM iam_perf_candidate_v2.sys_iam_role_heads WHERE workspace_id=1;
INSERT INTO sys_iam_assignments SELECT * FROM iam_perf_candidate_v2.sys_iam_assignments WHERE workspace_id=1 AND subject_id=1;
INSERT INTO sys_iam_principal_modes SELECT * FROM iam_perf_candidate_v2.sys_iam_principal_modes WHERE workspace_id=1 AND user_id=1;
UPDATE sys_iam_workspace_state SET mode='COMPAT_ACTIVE',activation_ready=true,active_bundle_id=md5('A52-bundle-1')::uuid,minimum_bridge='phase1-bridge-2' WHERE workspace_id=1;
COMMIT;
SQL
  ;;
 *) exit 2;;
esac
