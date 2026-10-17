-- 源库与恢复库应输出相同结果；只输出行数和摘要，不输出用户/连接字段。
SELECT 'migrations', count(*), md5(coalesce(string_agg(to_jsonb(t)::text, E'\n' ORDER BY to_jsonb(t)::text), '')) FROM schema_migrations t
UNION ALL
SELECT 'users', count(*), md5(coalesce(string_agg(to_jsonb(t)::text, E'\n' ORDER BY to_jsonb(t)::text), '')) FROM sys_users t
UNION ALL
SELECT 'connections', count(*), md5(coalesce(string_agg(to_jsonb(t)::text, E'\n' ORDER BY to_jsonb(t)::text), '')) FROM sys_connections t
UNION ALL
SELECT 'receipts', count(*), md5(coalesce(string_agg(to_jsonb(t)::text, E'\n' ORDER BY to_jsonb(t)::text), '')) FROM sys_query_executions t
UNION ALL
SELECT 'demo', count(*), md5(coalesce(string_agg(to_jsonb(t)::text, E'\n' ORDER BY to_jsonb(t)::text), '')) FROM dbhub_cloud_demo t
ORDER BY 1;
