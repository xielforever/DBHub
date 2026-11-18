-- Candidate evaluation is read-only. It never changes the effective principal
-- or opens a target connection. Configuration rollback keeps its data ceiling.
ALTER TABLE sys_iam_principal_modes ADD COLUMN rollback_guard BOOLEAN NOT NULL DEFAULT false;
UPDATE sys_iam_capabilities SET runtime_enabled=false WHERE action_id IN
 ('source.runtime.manage','metric.manage','audit.export','space.resources.request','space.policy.manage','space.members.manage');
CREATE TABLE sys_iam_isolation_guard (
 isolation_id TEXT PRIMARY KEY, database_name TEXT NOT NULL, description TEXT NOT NULL
);
-- This guard is deliberately empty on upgrade. Only an explicitly provisioned
-- isolated test database may exercise activation; environment flags alone fail.
REVOKE ALL ON sys_iam_isolation_guard FROM PUBLIC;
DO $migration$ DECLARE ns TEXT:=current_schema();
BEGIN
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_iam_qualified(uid BIGINT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
 JOIN sys_workspaces w ON w.id=m.workspace_id WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid
 AND m.active AND u.is_active AND w.enabled AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())
 AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=m.workspace_id AND v.target_id=uid AND v.kind='member_freeze'))
 AND NOT EXISTS(SELECT 1 FROM sys_iam_principal_modes p WHERE p.workspace_id=dbhub_workspace() AND p.user_id=uid
 AND (dbhub_iam_is_target(uid) OR p.rollback_guard) AND (NOT p.confirmed OR p.valid_until<=statement_timestamp()))
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_iam_legacy_capability(uid BIGINT,capability TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT capability=ANY(ARRAY['report_review','datasource_manage','audit_view','operation_review']::TEXT[])
 AND dbhub_iam_qualified(uid) AND EXISTS(SELECT 1 FROM sys_workspace_members m
 LEFT JOIN sys_iam_principal_modes p USING(workspace_id,user_id)
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid
 AND (m.role_code='admin' OR capability=ANY(m.capabilities))
 AND (NOT (dbhub_iam_is_target(uid) OR COALESCE(p.rollback_guard,false)) OR p.legacy_data_role='admin' OR capability=ANY(p.legacy_capabilities)))
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_member_role(uid BIGINT) RETURNS TEXT
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT CASE WHEN dbhub_iam_is_target(uid) OR COALESCE(p.rollback_guard,false) THEN
 CASE WHEN m.role_code='readonly' OR p.legacy_data_role='readonly' THEN 'readonly'
 WHEN m.role_code='developer' OR p.legacy_data_role='developer' THEN 'developer' ELSE 'admin' END
 ELSE m.role_code END FROM sys_workspace_members m LEFT JOIN sys_iam_principal_modes p USING(workspace_id,user_id)
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid AND dbhub_iam_qualified(uid)
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_can_review(uid BIGINT,family TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT family=ANY(ARRAY['report','dashboard']) AND dbhub_iam_qualified(uid) AND
 CASE WHEN dbhub_iam_is_target(uid) THEN dbhub_iam_has_action(uid,family||'.review')
 ELSE dbhub_iam_legacy_capability(uid,'report_review') END
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_shadow_basis(uid BIGINT,action TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_iam_qualified(uid) AND NOT dbhub_iam_action_denied(uid,action) AND EXISTS(
 SELECT 1 FROM sys_iam_assignments a JOIN sys_iam_role_heads h USING(workspace_id,role_id)
 JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision
 JOIN sys_iam_capabilities c ON c.action_id=action WHERE a.workspace_id=dbhub_workspace()
 AND a.role_revision=h.revision AND h.enabled AND r.enabled AND action=ANY(r.actions) AND c.runtime_enabled AND c.scope='workspace'
 AND a.approved_members @> ARRAY[uid] AND a.revoked_at IS NULL AND a.valid_from<=statement_timestamp() AND a.valid_until>statement_timestamp()
 AND (a.subject_type='user' AND a.subject_id=uid OR a.subject_type='group' AND EXISTS(
 SELECT 1 FROM sys_user_group_members gm WHERE gm.workspace_id=a.workspace_id AND gm.group_id=a.subject_id AND gm.user_id=uid))
 AND (action<>'query.write' OR dbhub_member_role(uid)<>'readonly'))
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_shadow_action(uid BIGINT,action TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_iam_shadow_basis(uid,action) AND NOT EXISTS(
 WITH RECURSIVE dependencies(id,path) AS(SELECT unnest(c.dependencies),ARRAY[action] FROM sys_iam_capabilities c WHERE c.action_id=action
 UNION ALL SELECT unnest(c.dependencies),d.path||d.id FROM dependencies d JOIN sys_iam_capabilities c ON c.action_id=d.id WHERE NOT d.id=ANY(d.path))
 SELECT 1 FROM dependencies d WHERE d.id=ANY(d.path) OR NOT dbhub_iam_shadow_basis(uid,d.id))
 $body$$f$,ns);
END $migration$;
