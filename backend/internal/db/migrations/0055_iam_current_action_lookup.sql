-- Resolve the finite dependency closure and current source union once per
-- statement. No authorization result survives a statement or a request.
DO $migration$ BEGIN
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_iam_has_action(uid BIGINT,action TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 WITH RECURSIVE needed(id,path,cycle) AS (
  SELECT action,ARRAY[action],false
  UNION ALL
  SELECT dep,n.path||dep,dep=ANY(n.path) FROM needed n
  JOIN sys_iam_capabilities c ON c.action_id=n.id CROSS JOIN LATERAL unnest(c.dependencies) dep
  WHERE NOT n.cycle
 ), who AS MATERIALIZED (
  SELECT dbhub_iam_qualified(uid) AS qualified,dbhub_iam_is_target(uid) AS target,dbhub_member_role(uid) AS data_role
 ), live AS MATERIALIZED (
  SELECT DISTINCT unnest(r.actions) AS id FROM sys_iam_assignments a
  JOIN sys_iam_role_heads h USING(workspace_id,role_id)
  JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision
  WHERE a.workspace_id=dbhub_workspace() AND a.approved_members @> ARRAY[uid]
   AND a.revoked_at IS NULL AND a.role_revision=h.revision AND h.enabled AND r.enabled
   AND a.valid_from<=statement_timestamp() AND a.valid_until>statement_timestamp()
   AND (a.subject_type='user' AND a.subject_id=uid OR a.subject_type='group' AND EXISTS(
    SELECT 1 FROM sys_user_group_members gm WHERE gm.workspace_id=a.workspace_id AND gm.group_id=a.subject_id AND gm.user_id=uid))
 ), legacy_caps AS MATERIALIZED (
  SELECT ARRAY(SELECT name FROM unnest(ARRAY['report_review','datasource_manage','audit_view','operation_review'])name
   WHERE dbhub_iam_legacy_capability(uid,name)) AS items
 ), permitted AS MATERIALIZED (
  SELECT c.action_id FROM sys_iam_capabilities c CROSS JOIN who w
  WHERE c.action_id IN(SELECT id FROM needed) AND c.scope='workspace' AND w.qualified
   AND NOT dbhub_iam_action_denied(uid,c.action_id)
   AND CASE WHEN w.target THEN c.runtime_enabled AND c.action_id IN(SELECT id FROM live)
    AND (c.action_id<>'query.write' OR w.data_role<>'readonly')
    ELSE c.implementation_state='legacy' AND
     (w.data_role=ANY(c.legacy_roles) OR c.legacy_capabilities && (SELECT items FROM legacy_caps)) END
 )
 SELECT (SELECT qualified FROM who) AND EXISTS(SELECT 1 FROM sys_iam_capabilities WHERE action_id=action)
  AND NOT EXISTS(SELECT 1 FROM needed WHERE cycle OR id NOT IN(SELECT action_id FROM permitted))
 $body$$f$,current_schema());
 -- Keep the historical helper callable, with the same complete conservative
 -- decision as the public lookup. It cannot bypass dependencies or data floors.
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_iam_action_basis(uid BIGINT,action TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_iam_has_action(uid,action)
 $body$$f$,current_schema());
END $migration$;
