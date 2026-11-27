-- Install the scoped capability upgrade without activating any workspace.
ALTER TABLE sys_iam_workspace_state
 ADD COLUMN dashboard_matrix_enabled BOOLEAN NOT NULL DEFAULT false,
 ADD CONSTRAINT iam_dashboard_matrix_requires_matrix
 CHECK (NOT dashboard_matrix_enabled OR matrix_enabled);

CREATE FUNCTION dbhub_iam_dashboard_matrix_enabled() RETURNS BOOLEAN
LANGUAGE SQL STABLE SECURITY DEFINER AS $body$
 SELECT COALESCE((
  SELECT s.dashboard_matrix_enabled FROM sys_iam_workspace_state s
  WHERE s.workspace_id=dbhub_workspace() AND dbhub_iam_qualified(dbhub_actor())
 ),false)
$body$;

-- Activation is an explicit metadata-owner operation. Repeated calls leave
-- the current revision, generations and audit trail alone.
CREATE FUNCTION dbhub_iam_enable_dashboard_matrix(workspace_key BIGINT) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY INVOKER AS $body$
DECLARE
 active BOOLEAN;
 template RECORD;
 next_revision BIGINT;
 activation_id UUID:=gen_random_uuid();
 moved INTEGER;
 assignments INTEGER:=0;
 revisions JSONB:='{}';
BEGIN
 IF workspace_key IS NULL OR workspace_key<=0 THEN
  RAISE EXCEPTION 'A business workspace is required' USING ERRCODE='22023';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:target:'||workspace_key,0));
 PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:'||workspace_key,0));
 SELECT s.dashboard_matrix_enabled INTO active
 FROM sys_iam_workspace_state s JOIN sys_workspaces w ON w.id=s.workspace_id
 WHERE s.workspace_id=workspace_key AND s.matrix_enabled AND w.enabled FOR UPDATE OF s FOR SHARE OF w;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'An enabled matrix workspace is required' USING ERRCODE='22023';
 END IF;
 IF active THEN
  RETURN false;
 END IF;

 FOR template IN
  SELECT t.code,t.role_id,t.display_name,h.revision AS old_revision,
         COALESCE(h.enabled,true) AS head_enabled,COALESCE(r.enabled,true) AS revision_enabled,
         COALESCE(r.name,t.display_name) AS name,COALESCE(r.actions,t.actions) AS actions,extra.actions AS extra
  FROM sys_iam_builtin_roles t
  JOIN (VALUES
   ('business_it',ARRAY['dashboard.page','dashboard.view','dashboard.design','dashboard.submit']),
   ('business_supervisor',ARRAY['dashboard.page','dashboard.view','dashboard.review','dashboard.publish']),
   ('viewer',ARRAY['dashboard.page','dashboard.view'])
  ) extra(code,actions) ON extra.code=t.code
  LEFT JOIN sys_iam_role_heads h ON h.workspace_id=workspace_key AND h.role_id=t.role_id
  LEFT JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision
  ORDER BY t.display_order
 LOOP
  SELECT COALESCE(max(r.revision),0)+1 INTO next_revision
  FROM sys_iam_role_revisions r WHERE r.workspace_id=workspace_key AND r.role_id=template.role_id;

  INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,proposal_id,created_by)
  VALUES(workspace_key,template.role_id,next_revision,template.name,
    template.actions||ARRAY(SELECT a.action FROM unnest(template.extra) a(action) WHERE NOT(a.action=ANY(template.actions))),
    template.revision_enabled,activation_id,0);
  INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled)
  VALUES(workspace_key,template.role_id,next_revision,template.display_name,template.head_enabled)
  ON CONFLICT(workspace_id,role_id) DO UPDATE SET
   revision=EXCLUDED.revision,version=sys_iam_role_heads.version+1;

  -- Carry forward only live sources attached to the prior head. A frozen
  -- member's independent eligibility restriction remains in force.
  UPDATE sys_iam_assignments SET role_revision=next_revision,version=version+1
  WHERE workspace_id=workspace_key AND role_id=template.role_id AND role_revision=template.old_revision
    AND revoked_at IS NULL AND valid_until>statement_timestamp();
  GET DIAGNOSTICS moved=ROW_COUNT;
  assignments:=assignments+moved;
  revisions:=revisions||jsonb_build_object(template.code,next_revision);
 END LOOP;

 IF NOT (revisions ?& ARRAY['business_it','business_supervisor','viewer']) THEN
  RAISE EXCEPTION 'Incomplete dashboard role templates';
 END IF;
 UPDATE sys_iam_workspace_state SET dashboard_matrix_enabled=true,
  config_version=config_version+1,authorization_epoch=authorization_epoch+1 WHERE workspace_id=workspace_key;
 INSERT INTO sys_iam_events(workspace_id,actor_id,request_id,operation,payload)
 VALUES(workspace_key,0,activation_id,'matrix.dashboard.enable',
  jsonb_build_object('role_revisions',revisions,'updated_assignments',assignments));
 RETURN true;
END;
$body$;

DO $migration$
DECLARE
 ns TEXT:=current_schema();
BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_dashboard_matrix_enabled() SET search_path=%I,pg_temp',ns);
 EXECUTE format('ALTER FUNCTION dbhub_iam_enable_dashboard_matrix(BIGINT) SET search_path=%I,pg_temp',ns);
END;
$migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_enable_dashboard_matrix(BIGINT) FROM PUBLIC;
