-- Shared ordinary role definitions. Compatibility principals keep their
-- existing mode until an administrator explicitly saves a direct allocation.
CREATE TABLE sys_iam_builtin_roles (
 code TEXT PRIMARY KEY,
 role_id UUID NOT NULL UNIQUE,
 display_name TEXT NOT NULL,
 actions TEXT[] NOT NULL,
 display_order INTEGER NOT NULL UNIQUE
);
INSERT INTO sys_iam_builtin_roles(code,role_id,display_name,actions,display_order) VALUES
 ('business_it','0f18644e-3bde-4673-93a8-8729a0f50301','业务 IT',
  ARRAY['catalog.page','catalog.metadata','connection.page','connection.use','connection.manage','query.page','query.read','report.page','report.view','report.design','report.submit','task.page'],1),
 ('business_supervisor','0f18644e-3bde-4673-93a8-8729a0f50302','业务主管',
  ARRAY['report.page','report.view','report.review','report.publish','task.page'],2),
 ('viewer','0f18644e-3bde-4673-93a8-8729a0f50303','查看者',
  ARRAY['report.page','report.view'],3);

ALTER TABLE sys_workspace_members ADD COLUMN direct_role_managed BOOLEAN NOT NULL DEFAULT false;

-- Separate explicitly allocated roles from dormant compatibility grants.
-- Principals already selected by the existing rollout retain their sources.
CREATE FUNCTION dbhub_iam_direct_source_allowed(uid BIGINT, rid UUID) RETURNS BOOLEAN
LANGUAGE SQL STABLE SECURITY INVOKER AS $body$
 SELECT EXISTS(SELECT 1 FROM sys_iam_builtin_roles t WHERE t.role_id=rid)
 OR NOT EXISTS(
  SELECT 1 FROM sys_workspace_members m JOIN sys_iam_workspace_state s USING(workspace_id)
  LEFT JOIN sys_iam_principal_modes p USING(workspace_id,user_id)
  WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid
    AND s.matrix_enabled AND m.direct_role_managed
    AND NOT COALESCE(s.mode='COMPAT_ACTIVE' AND s.activation_ready AND p.mode='TARGET',false)
 )
$body$;

CREATE FUNCTION dbhub_iam_builtin_role(uid BIGINT, role_code TEXT) RETURNS BOOLEAN
LANGUAGE SQL STABLE SECURITY DEFINER AS $body$
 SELECT dbhub_iam_qualified(uid) AND EXISTS(
  SELECT 1 FROM sys_workspace_members m
  JOIN sys_iam_workspace_state s USING(workspace_id)
  JOIN sys_iam_assignments a ON a.workspace_id=m.workspace_id AND a.subject_type='user' AND a.subject_id=m.user_id
  JOIN sys_iam_builtin_roles t ON t.role_id=a.role_id
  JOIN sys_iam_role_heads h ON h.workspace_id=a.workspace_id AND h.role_id=a.role_id AND h.revision=a.role_revision
  JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision
  WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid AND m.direct_role_managed AND s.matrix_enabled
    AND t.code=$2 AND h.enabled AND r.enabled AND a.revoked_at IS NULL
    AND a.valid_from<=statement_timestamp() AND a.valid_until>statement_timestamp()
 )
$body$;

-- Add only the participant-level selection and source filter to the audited
-- runtime resolvers. Keep dependency, deny, expiry and data floors unchanged.
DO $migration$
DECLARE ns TEXT:=current_schema(); f RECORD; body TEXT; previous TEXT; count_changed INT:=0;
BEGIN
 FOR f IN SELECT p.oid,p.proname,p.prosrc,pg_get_functiondef(p.oid) AS definition
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=ns AND p.proname=ANY(ARRAY['dbhub_iam_identity','dbhub_iam_identity_context']) LOOP
  previous:=f.prosrc;
  body:=replace(previous,
    'COALESCE(s.mode=''COMPAT_ACTIVE'' AND s.activation_ready AND p.mode=''TARGET'',false) AS target',
    'COALESCE(s.mode=''COMPAT_ACTIVE'' AND s.activation_ready AND p.mode=''TARGET'',false) AS rollout_target,
     (COALESCE(s.mode=''COMPAT_ACTIVE'' AND s.activation_ready AND p.mode=''TARGET'',false)
      OR s.matrix_enabled AND m.direct_role_managed AND m.role_code<>''admin'' AND NOT COALESCE(p.rollback_guard,false)) AS target');
  body:=replace(body,'r.target OR r.rollback_guard','r.rollout_target OR r.rollback_guard');
  IF body=previous OR position('r.rollout_target OR r.rollback_guard' in body)=0 THEN
   RAISE EXCEPTION 'unexpected IAM identity definition: %',f.proname;
  END IF;
  EXECUTE replace(f.definition,previous,body);
  count_changed:=count_changed+1;
 END LOOP;
 IF count_changed<>2 THEN RAISE EXCEPTION 'incomplete IAM identity inventory'; END IF;

 EXECUTE format($fn$CREATE OR REPLACE FUNCTION dbhub_iam_is_target(uid BIGINT) RETURNS BOOLEAN
  LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
   SELECT COALESCE((SELECT target FROM dbhub_iam_identity(uid)),false)
  $body$$fn$,ns);

 count_changed:=0;
 FOR f IN SELECT p.oid,p.proname,p.prosrc,pg_get_functiondef(p.oid) AS definition
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=ns AND p.proname=ANY(ARRAY['dbhub_iam_binding','dbhub_iam_bind_workspace','dbhub_iam_has_action','dbhub_iam_current_target_action']) LOOP
  previous:=f.prosrc;
  body:=replace(previous,'a.approved_members @> ARRAY[principal_uid]',
    'a.approved_members @> ARRAY[principal_uid] AND dbhub_iam_direct_source_allowed(principal_uid,a.role_id)');
  body:=replace(body,'a.approved_members @> ARRAY[uid]',
    'a.approved_members @> ARRAY[uid] AND dbhub_iam_direct_source_allowed(uid,a.role_id)');
  IF body=previous THEN RAISE EXCEPTION 'unexpected IAM role source definition: %',f.proname; END IF;
  EXECUTE replace(f.definition,previous,body);
  count_changed:=count_changed+1;
 END LOOP;
 IF count_changed<>4 THEN RAISE EXCEPTION 'incomplete IAM role source inventory'; END IF;
 EXECUTE format('ALTER FUNCTION dbhub_iam_direct_source_allowed(BIGINT,UUID) SET search_path=%I,pg_temp',ns);
 EXECUTE format('ALTER FUNCTION dbhub_iam_builtin_role(BIGINT,TEXT) SET search_path=%I,pg_temp',ns);
END $migration$;

REVOKE EXECUTE ON FUNCTION dbhub_iam_direct_source_allowed(BIGINT,UUID),dbhub_iam_builtin_role(BIGINT,TEXT) FROM PUBLIC;
