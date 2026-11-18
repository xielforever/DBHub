ALTER TABLE sys_workspace_members ADD COLUMN capabilities TEXT[] NOT NULL DEFAULT '{}'
 CHECK(cardinality(capabilities)<=4 AND capabilities<@ARRAY['report_review','datasource_manage','audit_view','operation_review']::text[]);
DO $migration$
DECLARE ns TEXT:=current_schema();
BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_has_capability(uid BIGINT, capability TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id JOIN sys_workspaces w ON w.id=m.workspace_id
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid AND m.active AND u.is_active AND w.enabled
 AND (m.role_code='admin' OR capability=ANY(m.capabilities)))
 $body$$f$,ns);
END $migration$;

CREATE OR REPLACE FUNCTION dbhub_source_visible(definition_id BIGINT, actor_id BIGINT) RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
 SELECT dbhub_member_role(actor_id) IS NOT NULL AND EXISTS(
 SELECT 1 FROM sys_source_definitions d JOIN sys_source_families f USING(family_id) WHERE d.id=definition_id AND (
 (d.visibility='private' AND d.owner_id=actor_id) OR
 (d.visibility='global' AND (dbhub_has_capability(actor_id,'datasource_manage') OR f.access_mode='global' OR EXISTS(
 SELECT 1 FROM sys_source_grants g WHERE g.family_id=f.family_id AND
 (g.user_id=actor_id OR EXISTS(SELECT 1 FROM sys_user_group_members m WHERE m.group_id=g.group_id AND m.user_id=actor_id)))))))
$$;
-- Preserve the existing SECURITY DEFINER and fixed search_path declarations.
DO $migration$
BEGIN
 EXECUTE replace(pg_get_functiondef('dbhub_business_notification()'::regprocedure),'AND role_code=''admin''','AND dbhub_has_capability(user_id,''report_review'')');
 EXECUTE replace(pg_get_functiondef('dbhub_report_export_notice()'::regprocedure),'AND role_code=''admin''','AND dbhub_has_capability(user_id,''report_review'')');
END $migration$;
