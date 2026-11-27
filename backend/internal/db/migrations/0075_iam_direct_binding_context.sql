-- The isolated generic-plan experiment did not improve the full cold/warm
-- behavior. Restore automatic plan selection, keeping all failure evidence.
DO $migration$ DECLARE p RECORD;changed INT:=0;ns TEXT:=current_schema();body TEXT;previous TEXT;
BEGIN
 FOR p IN SELECT f.proname,pg_get_function_identity_arguments(f.oid) AS args
 FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace
 WHERE n.nspname=ns AND f.proname=ANY(ARRAY[
  'dbhub_iam_workspace_directory','dbhub_iam_binding','dbhub_iam_bind_workspace',
  'dbhub_iam_request_current','dbhub_iam_admit_current','dbhub_iam_current_target_action',
  'dbhub_iam_identity_directory','dbhub_connection_list','dbhub_inbox_todos']) LOOP
  EXECUTE format('ALTER FUNCTION %I.%I(%s) RESET plan_cache_mode',ns,p.proname,p.args);changed:=changed+1;
 END LOOP;
 IF changed<>9 THEN RAISE EXCEPTION 'incomplete current read plan inventory: %',changed;END IF;
 -- Extend the private inline identity projection verbatim. Do not duplicate
 -- qualification or frozen-floor semantics in the binding implementation.
 SELECT f.prosrc INTO body FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace
 WHERE n.nspname=ns AND f.proname='dbhub_iam_identity' AND f.pronargs=1;
 previous:=body;
 body:=replace(body,'u.is_active,w.enabled,','u.is_active,w.enabled,w.name AS workspace_name,w.allow_unknown_freshness,');
 IF body IS NULL OR body=previous THEN RAISE EXCEPTION 'unexpected identity context projection';END IF;
 previous:=body;
 body:=replace(body,'r.valid_until,r.rollback_guard FROM identity_row r',
  'r.valid_until,r.rollback_guard,r.workspace_name,r.allow_unknown_freshness,r.expires_at,r.mode FROM identity_row r');
 IF body=previous THEN RAISE EXCEPTION 'unexpected identity context result';END IF;
 EXECUTE format('CREATE FUNCTION dbhub_iam_identity_context(uid BIGINT) RETURNS TABLE(qualified BOOLEAN,target BOOLEAN,data_role TEXT,legacy_caps TEXT[],membership_version BIGINT,config_version BIGINT,authorization_epoch BIGINT,profile_until TIMESTAMPTZ,rollback_guard BOOLEAN,workspace_name TEXT,allow_unknown BOOLEAN,member_until TIMESTAMPTZ,permission_mode TEXT) LANGUAGE SQL STABLE SECURITY INVOKER ROWS 1 AS %L',body);
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_identity_context(BIGINT) FROM PUBLIC;

-- One current relational statement binds just the requested space, rather than
-- reading every navigation row and then qualifying the same identity again.
CREATE OR REPLACE FUNCTION dbhub_iam_bind_workspace(principal_uid BIGINT,requested_space BIGINT)
RETURNS TABLE(workspace_id BIGINT,workspace_name TEXT,data_role TEXT,allow_unknown BOOLEAN,
 membership_version BIGINT,legacy_caps TEXT[],member_until TIMESTAMPTZ,
 authorization_epoch BIGINT,config_version BIGINT,permission_mode TEXT,
 revocation_sequence BIGINT,denied_actions TEXT[],target BOOLEAN,profile_until TIMESTAMPTZ,
 rollback_guard BOOLEAN,sources JSONB,actions TEXT[])
LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $body$
BEGIN
 PERFORM set_config('dbhub.workspace_id',requested_space::text,true),set_config('dbhub.actor_id',principal_uid::text,true);
 RETURN QUERY WITH who AS MATERIALIZED (
  SELECT i.* FROM dbhub_iam_identity_context(principal_uid)i WHERE i.qualified
   AND NOT EXISTS(SELECT 1 FROM sys_iam_recovery_grants g WHERE g.user_id=principal_uid AND g.valid_until>statement_timestamp())
 ), envelope AS MATERIALIZED (
  SELECT i.*,
   COALESCE((SELECT max(v.sequence) FROM sys_iam_revocations v WHERE v.workspace_id=requested_space),0) AS seq,
   ARRAY(SELECT DISTINCT v.action_id FROM sys_iam_revocations v WHERE v.workspace_id=requested_space
    AND v.target_id=principal_uid AND v.kind='action_revoke' AND NOT EXISTS(
     SELECT 1 FROM sys_iam_action_releases x JOIN sys_iam_proposals p USING(workspace_id,proposal_id)
     WHERE x.workspace_id=v.workspace_id AND x.user_id=v.target_id AND x.action_id=v.action_id
      AND x.revocation_sequence>=v.sequence AND x.valid_until>statement_timestamp() AND p.state='applied') ORDER BY v.action_id) AS denied,
   CASE WHEN i.target THEN (
    SELECT COALESCE(jsonb_agg(jsonb_build_object('id',a.assignment_id,'kind',a.subject_type,
     'role_id',a.role_id,'role_name',h.display_name,'revision',h.revision,
     'group_id',CASE WHEN a.subject_type='group' THEN a.subject_id ELSE 0 END,
     'group_version',COALESCE(g.version,0),'actions',r.actions,'expires_at',a.valid_until)
     ORDER BY a.assignment_id),'[]'::jsonb)
    FROM sys_iam_assignments a JOIN sys_iam_role_heads h USING(workspace_id,role_id)
    JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision
    LEFT JOIN sys_iam_group_versions g ON a.subject_type='group' AND g.workspace_id=a.workspace_id AND g.group_id=a.subject_id
    WHERE a.workspace_id=requested_space AND a.approved_members @> ARRAY[principal_uid]
     AND a.revoked_at IS NULL AND a.role_revision=h.revision AND h.enabled AND r.enabled
     AND a.valid_from<=statement_timestamp() AND a.valid_until>statement_timestamp()
     AND (a.subject_type='user' AND a.subject_id=principal_uid OR a.subject_type='group' AND EXISTS(
      SELECT 1 FROM sys_user_group_members gm WHERE gm.workspace_id=a.workspace_id AND gm.group_id=a.subject_id AND gm.user_id=principal_uid)))
    ELSE '[]'::jsonb END AS source_rows
  FROM who i
 ) SELECT requested_space,b.workspace_name,b.data_role,b.allow_unknown,b.membership_version,b.legacy_caps,b.member_until,
  b.authorization_epoch,b.config_version,b.permission_mode,b.seq,b.denied,b.target,b.profile_until,b.rollback_guard,b.source_rows,
  CASE WHEN b.target THEN ARRAY(SELECT DISTINCT c.action_id FROM jsonb_array_elements(b.source_rows)src
   CROSS JOIN LATERAL jsonb_array_elements_text(src->'actions')a(action) JOIN sys_iam_capabilities c ON c.action_id=a.action
   WHERE c.scope='workspace' AND c.runtime_enabled AND NOT(c.action_id=ANY(b.denied))
    AND (c.action_id<>'query.write' OR b.data_role<>'readonly') ORDER BY c.action_id) ELSE '{}'::text[] END
 FROM envelope b;
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_bind_workspace(BIGINT,BIGINT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_bind_workspace(BIGINT,BIGINT) FROM PUBLIC;
