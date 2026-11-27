-- Cache query plans in the session, never authorization results. SECURITY
-- INVOKER keeps the identical connection-list row policies and column surface.
CREATE FUNCTION dbhub_connection_list(dbtype TEXT,keyword TEXT,principal_uid BIGINT)
RETURNS TABLE(id sys_connections.id%TYPE,user_id sys_connections.user_id%TYPE,
 ssh_tunnel_id sys_connections.ssh_tunnel_id%TYPE,name sys_connections.name%TYPE,
 type sys_connections.type%TYPE,host sys_connections.host%TYPE,port sys_connections.port%TYPE,
 database sys_connections.database%TYPE,username sys_connections.username%TYPE,
 has_password BOOLEAN,ssl_mode sys_connections.ssl_mode%TYPE,
 connection_timeout sys_connections.connection_timeout%TYPE,color_label sys_connections.color_label%TYPE,
 environment sys_connections.environment%TYPE,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,
 tunnel_name sys_ssh_tunnels.name%TYPE,proxy_id sys_connections.proxy_id%TYPE,
 proxy_name sys_proxies.name%TYPE,check_result JSONB,template_revision_id BIGINT,
 parameters JSONB,template JSONB,definition_revision_id BIGINT,definition_family_id TEXT,definition JSONB)
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
BEGIN
 IF principal_uid<>dbhub_actor() THEN RETURN;END IF;
 RETURN QUERY SELECT c.id,c.user_id,c.ssh_tunnel_id,c.name,c.type,c.host,c.port,
  COALESCE(c.database,''),COALESCE(c.username,''),c.password IS NOT NULL AND c.password<>'',
  c.ssl_mode,c.connection_timeout,COALESCE(c.color_label,''),COALESCE(c.environment,'dev'),
  c.created_at,c.updated_at,COALESCE(t.name,''),c.proxy_id,
  COALESCE((SELECT p.name FROM sys_proxies p WHERE p.id=c.proxy_id),''),
  (SELECT cc.result FROM sys_connection_checks cc WHERE cc.connection_id=c.id AND cc.config_updated_at=c.updated_at),
  c.template_revision_id,c.parameters,
  (SELECT to_jsonb(dt)||jsonb_build_object('driver',to_jsonb(jd)) FROM sys_datasource_templates dt JOIN sys_jdbc_drivers jd ON jd.id=dt.driver_id WHERE dt.id=c.template_revision_id),
  c.definition_revision_id,COALESCE(c.definition_family_id::text,''),
  (SELECT to_jsonb(sd)||jsonb_build_object('access_denied',NOT dbhub_source_visible(sd.id,c.user_id)) FROM sys_source_definitions sd WHERE sd.id=c.definition_revision_id)
 FROM sys_connections c LEFT JOIN sys_ssh_tunnels t ON t.id=c.ssh_tunnel_id
 WHERE c.user_id=principal_uid AND (dbtype='' OR c.type=dbtype)
  AND (keyword='' OR c.name ILIKE '%'||keyword||'%' OR c.host ILIKE '%'||keyword||'%')
 ORDER BY c.id DESC;
END $body$;

-- The checked scope function already verifies the current action's explicit
-- prohibition. Avoid a second identical ban lookup in the request envelope.
CREATE FUNCTION dbhub_iam_request_current() RETURNS BOOLEAN
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
DECLARE action TEXT:=COALESCE(current_setting('dbhub.action_id',true),'');
BEGIN
 IF NOT dbhub_scope_allowed() THEN RETURN false;END IF;
 IF action='' OR NOT dbhub_iam_is_target(dbhub_actor()) THEN RETURN true;END IF;
 RETURN dbhub_iam_has_action(dbhub_actor(),action);
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_connection_list(TEXT,TEXT,BIGINT) SET search_path=%I,pg_temp',current_schema());
 EXECUTE format('ALTER FUNCTION dbhub_iam_request_current() SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_connection_list(TEXT,TEXT,BIGINT),dbhub_iam_request_current() FROM PUBLIC;
