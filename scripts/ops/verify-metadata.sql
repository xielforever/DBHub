-- Run on quiesced source and freshly restored metadata. Only counts/digests are
-- output: never usernames, credentials, SQL, report definitions or result rows.
-- Canonical timestamps/bytea permit comparison across different server defaults.
SET TIME ZONE 'UTC';
SET bytea_output = 'hex';
-- Aggregate fixed-size row hashes rather than full SQL/definition payloads.
SELECT 'migrations', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM schema_migrations t
UNION ALL SELECT 'users', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_users t
UNION ALL SELECT 'workspaces', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_workspaces t
UNION ALL SELECT 'members', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_workspace_members t
UNION ALL SELECT 'connections', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_connections t
UNION ALL SELECT 'report_accounts', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_report_accounts t
UNION ALL SELECT 'reports', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_managed_reports t
UNION ALL SELECT 'report_versions', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_report_versions t
UNION ALL SELECT 'report_reviews', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_report_reviews t
UNION ALL SELECT 'report_releases', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_report_releases t
UNION ALL SELECT 'dashboards', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_managed_dashboards t
UNION ALL SELECT 'dashboard_versions', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_dashboard_versions t
UNION ALL SELECT 'dashboard_reviews', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_dashboard_reviews t
UNION ALL SELECT 'dashboard_releases', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_dashboard_releases t
UNION ALL SELECT 'access_requests', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_access_requests t
UNION ALL SELECT 'receipts', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_query_executions t
UNION ALL SELECT 'notifications', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_notifications t
UNION ALL SELECT 'source_tasks', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_source_tasks t
UNION ALL SELECT 'source_schedules', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_source_schedules t
UNION ALL SELECT 'source_check_history', count(*), md5(coalesce(string_agg(md5(to_jsonb(t)::text), E'\n' ORDER BY md5(to_jsonb(t)::text)), '')) FROM sys_source_check_history t
ORDER BY 1;
