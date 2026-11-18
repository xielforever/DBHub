-- Reuse metadata read plans while retaining the simple wire protocol used by
-- existing proxies. Only plans are reused: membership, expiry, restrictions and
-- recovery eligibility are read again for every statement. This private helper
-- runs with the caller's privileges and is never callable by tenant runtimes.
CREATE FUNCTION dbhub_iam_workspace_directory(subject_id BIGINT, detailed BOOLEAN)
RETURNS TABLE(id BIGINT,name TEXT,role_code TEXT,allow_unknown_freshness BOOLEAN,
 membership_version BIGINT,capabilities TEXT[],expires_at TIMESTAMPTZ,
 authorization_epoch BIGINT,config_version BIGINT,permission_mode TEXT,
 revocation_sequence BIGINT,denied_actions TEXT[],has_recovery BOOLEAN)
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
BEGIN
 IF detailed THEN
  RETURN QUERY SELECT w.id,w.name::TEXT,m.role_code::TEXT,w.allow_unknown_freshness,
   m.version,m.capabilities,m.expires_at,s.authorization_epoch,s.config_version,s.mode::TEXT,
   COALESCE((SELECT max(v.sequence) FROM sys_iam_revocations v WHERE v.workspace_id=w.id),0),
   ARRAY(SELECT DISTINCT v.action_id FROM sys_iam_revocations v
    WHERE v.workspace_id=w.id AND v.target_id=m.user_id AND v.kind='action_revoke'
    AND NOT EXISTS(SELECT 1 FROM sys_iam_action_releases x
     JOIN sys_iam_proposals p USING(workspace_id,proposal_id)
     WHERE x.workspace_id=v.workspace_id AND x.user_id=v.target_id
     AND x.action_id=v.action_id AND x.revocation_sequence>=v.sequence
     AND x.valid_until>statement_timestamp() AND p.state='applied') ORDER BY v.action_id),
   EXISTS(SELECT 1 FROM sys_iam_recovery_grants g WHERE g.user_id=subject_id AND g.valid_until>statement_timestamp())
  FROM sys_workspaces w JOIN sys_workspace_members m ON m.workspace_id=w.id
  JOIN sys_users u ON u.id=m.user_id LEFT JOIN sys_iam_workspace_state s ON s.workspace_id=w.id
  WHERE m.user_id=subject_id AND m.active AND u.is_active AND w.enabled
   AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())
   AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=w.id
    AND v.target_id=m.user_id AND v.kind='member_freeze') ORDER BY w.id;
 ELSE
  RETURN QUERY SELECT w.id,w.name::TEXT,m.role_code::TEXT,w.allow_unknown_freshness,
   m.version,m.capabilities,m.expires_at,s.authorization_epoch,s.config_version,s.mode::TEXT,
   0::BIGINT,'{}'::TEXT[],
   EXISTS(SELECT 1 FROM sys_iam_recovery_grants g WHERE g.user_id=subject_id AND g.valid_until>statement_timestamp())
  FROM sys_workspaces w JOIN sys_workspace_members m ON m.workspace_id=w.id
  JOIN sys_users u ON u.id=m.user_id LEFT JOIN sys_iam_workspace_state s ON s.workspace_id=w.id
  WHERE m.user_id=subject_id AND m.active AND u.is_active AND w.enabled
   AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())
   AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=w.id
    AND v.target_id=m.user_id AND v.kind='member_freeze') ORDER BY w.id;
 END IF;
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_workspace_directory(BIGINT,BOOLEAN) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_workspace_directory(BIGINT,BOOLEAN) FROM PUBLIC;
