-- A single current row supplies qualification, the frozen data floor and the
-- original four responsibilities. Values are local to the executing statement.
DO $migration$ DECLARE ns TEXT:=current_schema();body TEXT;previous TEXT;
BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_identity(uid BIGINT) RETURNS TABLE(
 qualified BOOLEAN,target BOOLEAN,data_role TEXT,legacy_caps TEXT[],membership_version BIGINT,
 config_version BIGINT,authorization_epoch BIGINT,profile_until TIMESTAMPTZ,rollback_guard BOOLEAN)
 LANGUAGE SQL STABLE SECURITY DEFINER ROWS 1 SET search_path=%I,pg_temp AS $body$
 WITH identity_row AS (
  SELECT m.*,u.is_active,w.enabled,s.mode,s.activation_ready,s.config_version,s.authorization_epoch,
   p.legacy_data_role,p.legacy_capabilities,p.confirmed,p.valid_until,
   COALESCE(p.rollback_guard,false) AS rollback_guard,
   COALESCE(s.mode='COMPAT_ACTIVE' AND s.activation_ready AND p.mode='TARGET',false) AS target
  FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id JOIN sys_workspaces w ON w.id=m.workspace_id
  JOIN sys_iam_workspace_state s ON s.workspace_id=m.workspace_id
  LEFT JOIN sys_iam_principal_modes p ON p.workspace_id=m.workspace_id AND p.user_id=m.user_id
  WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid
 )
 SELECT r.active AND r.is_active AND r.enabled AND (r.expires_at IS NULL OR r.expires_at>statement_timestamp())
  AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=r.workspace_id AND v.target_id=uid AND v.kind='member_freeze')
  AND (NOT(r.target OR r.rollback_guard) OR r.confirmed AND (r.valid_until IS NULL OR r.valid_until>statement_timestamp())),
 r.target,CASE WHEN r.target OR r.rollback_guard THEN
  CASE WHEN r.role_code='readonly' OR r.legacy_data_role='readonly' THEN 'readonly'
   WHEN r.role_code='developer' OR r.legacy_data_role='developer' THEN 'developer' ELSE 'admin' END ELSE r.role_code END,
 ARRAY(SELECT name FROM unnest(ARRAY['report_review','datasource_manage','audit_view','operation_review'])name
  WHERE (r.role_code='admin' OR name=ANY(r.capabilities))
  AND (NOT(r.target OR r.rollback_guard) OR r.legacy_data_role='admin' OR name=ANY(r.legacy_capabilities))),
 r.version,r.config_version,r.authorization_epoch,r.valid_until,r.rollback_guard FROM identity_row r
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_iam_qualified(uid BIGINT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT COALESCE((SELECT qualified FROM dbhub_iam_identity(uid)),false)
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_member_role(uid BIGINT) RETURNS TEXT
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT data_role FROM dbhub_iam_identity(uid) WHERE qualified
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_iam_legacy_capability(uid BIGINT,capability TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT EXISTS(SELECT 1 FROM dbhub_iam_identity(uid) WHERE qualified AND capability=ANY(legacy_caps))
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_scope_allowed() RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT EXISTS(SELECT 1 FROM dbhub_iam_identity(dbhub_actor()) i WHERE i.qualified
  AND i.data_role=current_setting('dbhub.actor_role',true)
  AND (dbhub_member_epoch()=0 OR i.membership_version=dbhub_member_epoch())
  AND (COALESCE(NULLIF(current_setting('dbhub.authorization_epoch',true),''),'0')::bigint=0 OR i.authorization_epoch=current_setting('dbhub.authorization_epoch',true)::bigint)
  AND (COALESCE(NULLIF(current_setting('dbhub.config_version',true),''),'0')::bigint=0 OR i.config_version=current_setting('dbhub.config_version',true)::bigint)
  AND (NOT i.target AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=dbhub_workspace() AND v.target_id=dbhub_actor() AND v.kind='action_revoke')
   OR COALESCE(current_setting('dbhub.runtime_bridge',true),'')='phase1-bridge-2')
  AND (COALESCE(current_setting('dbhub.action_id',true),'')='' OR NOT dbhub_iam_action_denied(dbhub_actor(),current_setting('dbhub.action_id',true))))
 $body$$f$,ns);
 -- Retain the exact finite dependency/source resolver from 0055; replace only
 -- its repeated current-identity calls. Unexpected input definitions stop here.
 SELECT p.prosrc INTO body FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=ns AND p.proname='dbhub_iam_has_action' AND p.pronargs=2;
 previous:=body;
 body:=replace(body,'SELECT dbhub_iam_qualified(uid) AS qualified,dbhub_iam_is_target(uid) AS target,dbhub_member_role(uid) AS data_role',
  'SELECT qualified,target,data_role,legacy_caps FROM dbhub_iam_identity(uid)');
 IF body=previous THEN RAISE EXCEPTION 'unexpected IAM action lookup definition';END IF;
 previous:=body;
 body:=replace(body,'c.legacy_capabilities && (SELECT items FROM legacy_caps)','c.legacy_capabilities && w.legacy_caps');
 IF body=previous THEN RAISE EXCEPTION 'unexpected IAM legacy capability lookup';END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION dbhub_iam_has_action(uid BIGINT,action TEXT) RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS %L',ns,body);
END $migration$;
