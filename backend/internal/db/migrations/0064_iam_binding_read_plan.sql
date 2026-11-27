-- Private current-identity/source read. Reuses the query plan and one metadata
-- round-trip; it never stores a permission result or changes a data identity.
CREATE FUNCTION dbhub_iam_binding(principal_uid BIGINT)
RETURNS TABLE(target BOOLEAN,data_role TEXT,profile_until TIMESTAMPTZ,
 rollback_guard BOOLEAN,legacy_caps TEXT[],sources JSONB)
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
BEGIN
 RETURN QUERY SELECT i.target,CASE WHEN i.qualified THEN i.data_role ELSE '' END,
  i.profile_until,i.rollback_guard,i.legacy_caps,
  CASE WHEN i.target THEN (
   SELECT COALESCE(jsonb_agg(jsonb_build_object('id',a.assignment_id,'kind',a.subject_type,
    'role_id',a.role_id,'role_name',h.display_name,'revision',h.revision,
    'group_id',CASE WHEN a.subject_type='group' THEN a.subject_id ELSE 0 END,
    'group_version',COALESCE(g.version,0),'actions',r.actions,'expires_at',a.valid_until)
    ORDER BY a.assignment_id),'[]'::jsonb)
   FROM sys_iam_assignments a JOIN sys_iam_role_heads h USING(workspace_id,role_id)
   JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision
   LEFT JOIN sys_iam_group_versions g ON a.subject_type='group' AND g.workspace_id=a.workspace_id AND g.group_id=a.subject_id
   WHERE a.workspace_id=dbhub_workspace() AND a.approved_members @> ARRAY[principal_uid]
    AND a.revoked_at IS NULL AND a.role_revision=h.revision AND h.enabled AND r.enabled
    AND a.valid_from<=statement_timestamp() AND a.valid_until>statement_timestamp()
    AND (a.subject_type='user' AND a.subject_id=principal_uid OR a.subject_type='group' AND EXISTS(
     SELECT 1 FROM sys_user_group_members gm WHERE gm.workspace_id=a.workspace_id
      AND gm.group_id=a.subject_id AND gm.user_id=principal_uid))) ELSE '[]'::jsonb END
 FROM dbhub_iam_identity(principal_uid) i;
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_binding(BIGINT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_binding(BIGINT) FROM PUBLIC;
