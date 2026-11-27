-- Keep one current relational read and its reusable plan, while returning one
-- identity row with the existing compact directory payload. Do not send the
-- same password/session fields once for every workspace over the wire.
CREATE OR REPLACE FUNCTION dbhub_iam_identity_directory(login_name TEXT)
RETURNS TABLE(id BIGINT,user_name TEXT,data_role TEXT,password_hash TEXT,
 is_active BOOLEAN,last_login_at TIMESTAMPTZ,auth_version BIGINT,directory JSONB,has_recovery BOOLEAN)
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
BEGIN
 RETURN QUERY
 SELECT u.id,u.username::text,r.code::text,u.password_hash::text,u.is_active,u.last_login_at,u.auth_version,
  COALESCE(jsonb_agg(jsonb_build_object('id',d.id,'name',d.name,'role',d.role_code,
   'allow_unknown_freshness',d.allow_unknown_freshness,'membership_version',d.membership_version,
   'capabilities',d.capabilities,'expires_at',d.expires_at,'authorization_epoch',d.authorization_epoch,
   'config_version',d.config_version,'permission_mode',d.permission_mode) ORDER BY d.id)
   FILTER(WHERE d.id IS NOT NULL),'[]'::jsonb),
  COALESCE(bool_or(d.id IS NOT NULL AND EXISTS(SELECT 1 FROM sys_iam_recovery_grants g
   WHERE g.user_id=u.id AND g.valid_until>statement_timestamp())),false)
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
 WHERE u.username=login_name GROUP BY u.id,r.id;
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_identity_directory(TEXT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_identity_directory(TEXT) FROM PUBLIC;
ALTER FUNCTION dbhub_iam_identity_directory(TEXT) SET plan_cache_mode=force_generic_plan;
