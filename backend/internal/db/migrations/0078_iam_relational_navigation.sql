-- Navigation reads current identity and current directory rows together.
-- Return typed rows instead of aggregating to JSON and decoding that JSON in
-- Go. This is a private metadata read, never an execution authorization source.
CREATE FUNCTION dbhub_iam_identity_navigation(login_name TEXT)
RETURNS TABLE(id BIGINT,user_name TEXT,data_role TEXT,password_hash TEXT,
 is_active BOOLEAN,last_login_at TIMESTAMPTZ,auth_version BIGINT,
 nav_id BIGINT,nav_name TEXT,nav_role TEXT,nav_allow_unknown BOOLEAN,
 nav_membership_version BIGINT,nav_capabilities TEXT[],nav_expires_at TIMESTAMPTZ,
 nav_authorization_epoch BIGINT,nav_config_version BIGINT,nav_permission_mode TEXT,
 has_recovery BOOLEAN)
LANGUAGE sql STABLE SECURITY INVOKER AS $body$
 SELECT u.id,u.username::text,r.code::text,u.password_hash::text,
  u.is_active,u.last_login_at,u.auth_version,
  d.id,COALESCE(d.name,''),COALESCE(d.role_code,''),COALESCE(d.allow_unknown_freshness,false),
  COALESCE(d.membership_version,0),COALESCE(d.capabilities,'{}'::text[]),d.expires_at,
  COALESCE(d.authorization_epoch,0),COALESCE(d.config_version,0),COALESCE(d.permission_mode,''),
  d.id IS NOT NULL AND EXISTS(SELECT 1 FROM sys_iam_recovery_grants g
   WHERE g.user_id=u.id AND g.valid_until>statement_timestamp())
 FROM sys_users u JOIN sys_roles r ON r.id=u.role_id
 LEFT JOIN LATERAL (
  SELECT w.id,w.name::text,m.role_code::text,w.allow_unknown_freshness,
   m.version AS membership_version,m.capabilities,m.expires_at,
   s.authorization_epoch,s.config_version,s.mode::text AS permission_mode
  FROM sys_workspace_members m JOIN sys_workspaces w ON w.id=m.workspace_id
  LEFT JOIN sys_iam_workspace_state s ON s.workspace_id=w.id
  WHERE m.user_id=u.id AND m.active AND u.is_active AND w.enabled
   AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())
   AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=w.id
    AND v.target_id=u.id AND v.kind='member_freeze')
 )d ON true
 WHERE u.username=login_name ORDER BY d.id;
$body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_identity_navigation(TEXT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_identity_navigation(TEXT) FROM PUBLIC;
