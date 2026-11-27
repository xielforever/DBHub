\set ON_ERROR_STOP on
CREATE FUNCTION pg_temp.perf_hash(ns TEXT,tab TEXT) RETURNS TEXT LANGUAGE plpgsql AS $$
DECLARE result TEXT;
BEGIN
 EXECUTE format('SELECT encode(sha256(convert_to(COALESCE(string_agg(row::text,'''' ORDER BY row::text),''''),''UTF8'')),''hex'') FROM (SELECT to_jsonb(t)-ARRAY[''updated_at'',''last_login_at'',''expires_at'',''permission_bridge'',''permission_config'',''permission_epoch''] AS row FROM %I.%I t) a',ns,tab) INTO result;
 RETURN result;
END$$;
DO $$DECLARE tab TEXT;BEGIN
 FOREACH tab IN ARRAY ARRAY['sys_users','sys_workspace_members','sys_connections','sys_query_policies','sys_source_definitions','sys_source_families','sys_managed_reports','sys_report_versions','sys_report_reviews','sys_managed_dashboards','sys_dashboard_versions','sys_dashboard_reviews','sys_source_tasks'] LOOP
  IF pg_temp.perf_hash('iam_perf_baseline',tab)<>pg_temp.perf_hash('iam_perf_candidate_v2',tab) THEN RAISE EXCEPTION 'logical fixture mismatch: %',tab;END IF;
 END LOOP;
 IF (SELECT count(*) FROM iam_perf_baseline.sys_users)<>1000 OR (SELECT count(*) FROM iam_perf_baseline.sys_workspace_members)<>2000 OR (SELECT count(*) FROM iam_perf_baseline.sys_connections)<>2000 THEN RAISE EXCEPTION 'registered fixture mismatch';END IF;
END$$;
SELECT jsonb_build_object('format','A52_LOGICAL_FIXTURE_V1','same_logical_data',true,'synthetic_metadata_only',true,
 'workspaces',(SELECT count(*) FROM iam_perf_baseline.sys_workspaces WHERE id>0),'users',(SELECT count(*) FROM iam_perf_baseline.sys_users),'memberships',(SELECT count(*) FROM iam_perf_baseline.sys_workspace_members),
 'connections',(SELECT count(*) FROM iam_perf_baseline.sys_connections),'sources',(SELECT count(*) FROM iam_perf_baseline.sys_source_definitions),'query_policies',(SELECT count(*) FROM iam_perf_baseline.sys_query_policies),
 'published_reports',(SELECT count(*) FROM iam_perf_baseline.sys_managed_reports WHERE active_release_id IS NOT NULL),'published_dashboards',(SELECT count(*) FROM iam_perf_baseline.sys_managed_dashboards WHERE active_release_id IS NOT NULL),
 'historical_tasks',(SELECT count(*) FROM iam_perf_baseline.sys_source_tasks),'audit_records',(SELECT count(*) FROM iam_perf_baseline.sys_audit_logs),
 'fingerprints',(SELECT jsonb_object_agg(tab,pg_temp.perf_hash('iam_perf_baseline',tab)) FROM unnest(ARRAY['sys_users','sys_workspace_members','sys_connections','sys_query_policies','sys_source_definitions','sys_managed_reports','sys_managed_dashboards','sys_source_tasks'])tab));
