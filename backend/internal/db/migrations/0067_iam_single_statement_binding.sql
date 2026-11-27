-- Normal explicit-space requests obtain one current statement's complete
-- binding. Only SQL plans persist. Recovery sessions retain their separate
-- checked path; no authorization snapshot is cached across requests.
CREATE FUNCTION dbhub_iam_bind_workspace(principal_uid BIGINT,requested_space BIGINT)
RETURNS TABLE(workspace_id BIGINT,workspace_name TEXT,data_role TEXT,allow_unknown BOOLEAN,
 membership_version BIGINT,legacy_caps TEXT[],member_until TIMESTAMPTZ,
 authorization_epoch BIGINT,config_version BIGINT,permission_mode TEXT,
 revocation_sequence BIGINT,denied_actions TEXT[],target BOOLEAN,profile_until TIMESTAMPTZ,
 rollback_guard BOOLEAN,sources JSONB,actions TEXT[])
LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $body$
DECLARE w RECORD;b RECORD;permitted TEXT[]:='{}';
BEGIN
 PERFORM set_config('dbhub.workspace_id',requested_space::text,true),set_config('dbhub.actor_id',principal_uid::text,true);
 SELECT d.* INTO w FROM dbhub_iam_workspace_directory(principal_uid,true)d
  WHERE d.id=requested_space AND NOT d.has_recovery;
 IF NOT FOUND THEN RETURN;END IF;
 SELECT i.* INTO b FROM dbhub_iam_binding(principal_uid)i;
 IF NOT FOUND OR b.data_role='' THEN RETURN;END IF;
 IF b.target THEN
  SELECT COALESCE(array_agg(DISTINCT c.action_id ORDER BY c.action_id),'{}') INTO permitted
  FROM jsonb_array_elements(b.sources)src
  CROSS JOIN LATERAL jsonb_array_elements_text(src->'actions')a(action)
  JOIN sys_iam_capabilities c ON c.action_id=a.action
  WHERE c.scope='workspace' AND c.runtime_enabled AND NOT(c.action_id=ANY(w.denied_actions))
   AND (c.action_id<>'query.write' OR b.data_role<>'readonly');
 END IF;
 RETURN QUERY SELECT w.id,w.name,b.data_role,w.allow_unknown_freshness,w.membership_version,
  b.legacy_caps,w.expires_at,w.authorization_epoch,w.config_version,w.permission_mode,
  w.revocation_sequence,w.denied_actions,b.target,b.profile_until,b.rollback_guard,b.sources,permitted;
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_bind_workspace(BIGINT,BIGINT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_bind_workspace(BIGINT,BIGINT) FROM PUBLIC;
