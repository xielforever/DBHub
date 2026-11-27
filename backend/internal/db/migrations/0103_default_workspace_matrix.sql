-- New appointees hold an administrative identity independently of their data role.
-- Acquire the default workspace lock before the table lock used by the DDL.
SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:target:1',0));
ALTER TABLE sys_workspace_members ADD COLUMN matrix_admin BOOLEAN NOT NULL DEFAULT false;

DO $migration$
DECLARE
  cutover_request UUID := gen_random_uuid();
  converted INTEGER;
  granted INTEGER;
BEGIN
  IF NOT EXISTS(SELECT 1 FROM sys_iam_workspace_state WHERE workspace_id=1 AND NOT matrix_enabled) THEN
    RETURN;
  END IF;

  PERFORM set_config('dbhub.workspace_id','1',true);

  -- Select a complete built-in only when every one of its actions was already
  -- available. Frozen, disabled and expired members acquire no new roles.
  CREATE TEMP TABLE dbhub_default_matrix_cutover ON COMMIT DROP AS
    WITH qualifying_roles AS (
      SELECT m.user_id,m.expires_at,ARRAY(
        SELECT t.code FROM sys_iam_builtin_roles t
        WHERE dbhub_member_role(m.user_id) IS NOT NULL
          AND NOT EXISTS(SELECT 1 FROM unnest(t.actions) action
                         WHERE NOT dbhub_iam_legacy_action(m.user_id,action))
        ORDER BY t.display_order
      ) AS role_codes
      FROM sys_workspace_members m
      JOIN sys_iam_workspace_state s USING(workspace_id)
      LEFT JOIN sys_iam_principal_modes p USING(workspace_id,user_id)
      WHERE m.workspace_id=1 AND m.role_code<>'admin' AND NOT m.direct_role_managed
        AND NOT COALESCE(s.mode='COMPAT_ACTIVE' AND s.activation_ready AND p.mode='TARGET',false)
        AND NOT COALESCE(p.rollback_guard,false)
    )
    SELECT user_id,expires_at,CASE WHEN role_codes && ARRAY['business_it','business_supervisor']
      THEN array_remove(role_codes,'viewer') ELSE role_codes END AS role_codes
    FROM qualifying_roles;

  INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by)
    SELECT 1,t.role_id,1,t.display_name,t.actions,true,0 FROM sys_iam_builtin_roles t
    WHERE EXISTS(SELECT 1 FROM dbhub_default_matrix_cutover m WHERE t.code=ANY(m.role_codes))
    ON CONFLICT(workspace_id,role_id,revision) DO NOTHING;

  INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled)
    SELECT 1,t.role_id,1,t.display_name,true FROM sys_iam_builtin_roles t
    WHERE EXISTS(SELECT 1 FROM dbhub_default_matrix_cutover m WHERE t.code=ANY(m.role_codes))
    ON CONFLICT(workspace_id,role_id) DO NOTHING;

  INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,
                                  approved_members,valid_until,granted_by,proposal_id)
    SELECT 1,gen_random_uuid(),t.role_id,1,'user',m.user_id,ARRAY[m.user_id],
           COALESCE(m.expires_at,TIMESTAMPTZ '9999-12-31 00:00:00+00'),0,cutover_request
    FROM dbhub_default_matrix_cutover m JOIN sys_iam_builtin_roles t ON t.code=ANY(m.role_codes)
    WHERE NOT EXISTS(SELECT 1 FROM sys_iam_assignments a
                     WHERE a.workspace_id=1 AND a.subject_type='user' AND a.subject_id=m.user_id
                       AND a.role_id=t.role_id AND a.revoked_at IS NULL AND a.valid_until>now());
  GET DIAGNOSTICS granted=ROW_COUNT;

  UPDATE sys_workspace_members m SET direct_role_managed=true,version=m.version+1
    FROM dbhub_default_matrix_cutover migrated WHERE m.workspace_id=1 AND m.user_id=migrated.user_id;
  GET DIAGNOSTICS converted=ROW_COUNT;

  UPDATE sys_iam_workspace_state SET matrix_enabled=true,config_version=config_version+1,
    authorization_epoch=authorization_epoch+1 WHERE workspace_id=1;

  INSERT INTO sys_iam_events(workspace_id,actor_id,request_id,operation,payload)
    VALUES(1,0,cutover_request,'matrix.default_workspace.enable',
      jsonb_build_object('converted_members',converted,'created_assignments',granted));
END $migration$;
