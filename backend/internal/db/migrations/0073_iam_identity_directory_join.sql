-- The navigation endpoint needs both current login identity and the current
-- navigation directory. Read them together, without a second wire round trip.
-- Directory rows never authorize binding or execution; those paths keep their
-- independent current checks. This private function includes a password hash
-- for normal session-version verification and must never be runtime-callable.
CREATE FUNCTION dbhub_iam_identity_directory(login_name TEXT)
RETURNS TABLE(id BIGINT,user_name TEXT,data_role TEXT,password_hash TEXT,
 is_active BOOLEAN,last_login_at TIMESTAMPTZ,auth_version BIGINT,directory JSONB,has_recovery BOOLEAN)
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
BEGIN
 RETURN QUERY SELECT u.id,u.username::text,r.code::text,u.password_hash::text,u.is_active,u.last_login_at,u.auth_version,
  nav.directory,nav.has_recovery
 FROM sys_users u JOIN sys_roles r ON r.id=u.role_id
 CROSS JOIN LATERAL (
  SELECT COALESCE(jsonb_agg(jsonb_build_object(
   'id',d.id,'name',d.name,'role',d.role_code,'allow_unknown_freshness',d.allow_unknown_freshness,
   'membership_version',d.membership_version,'capabilities',d.capabilities,'expires_at',d.expires_at,
   'authorization_epoch',d.authorization_epoch,'config_version',d.config_version,'permission_mode',d.permission_mode)
   ORDER BY d.id),'[]'::jsonb) AS directory,COALESCE(bool_or(d.has_recovery),false) AS has_recovery
  FROM dbhub_iam_workspace_directory(u.id,false)d
 )nav
 WHERE u.username=login_name;
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_identity_directory(TEXT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_identity_directory(TEXT) FROM PUBLIC;
