-- An old process may still serve legacy principals, but cannot execute a
-- selected target principal through its old role-only SQL path.
DO $migration$ DECLARE ns TEXT:=current_schema();t TEXT;
BEGIN
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_scope_allowed() RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT COALESCE(dbhub_member_role(dbhub_actor())=current_setting('dbhub.actor_role',true),false)
 AND EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_iam_workspace_state s USING(workspace_id)
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=dbhub_actor()
 AND (dbhub_member_epoch()=0 OR m.version=dbhub_member_epoch())
 AND (COALESCE(NULLIF(current_setting('dbhub.authorization_epoch',true),''),'0')::bigint=0 OR s.authorization_epoch=current_setting('dbhub.authorization_epoch',true)::bigint)
 AND (COALESCE(NULLIF(current_setting('dbhub.config_version',true),''),'0')::bigint=0 OR s.config_version=current_setting('dbhub.config_version',true)::bigint))
 AND (NOT dbhub_iam_is_target(dbhub_actor()) AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=dbhub_workspace() AND v.target_id=dbhub_actor() AND v.kind='action_revoke')
 OR COALESCE(current_setting('dbhub.runtime_bridge',true),'')='phase1-bridge-2')
 AND (COALESCE(current_setting('dbhub.action_id',true),'')='' OR NOT dbhub_iam_action_denied(dbhub_actor(),current_setting('dbhub.action_id',true)))
 $body$$f$,ns);
 FOREACH t IN ARRAY ARRAY['sys_source_tasks','sys_report_subscription_runs','sys_webhook_deliveries','sys_query_executions','sys_report_exports'] LOOP
 EXECUTE format('ALTER TABLE %I ADD COLUMN permission_bridge TEXT NOT NULL DEFAULT ''legacy-frozen-v1''',t);
 EXECUTE format('ALTER TABLE %I ADD COLUMN permission_config BIGINT NOT NULL DEFAULT 0',t);
 EXECUTE format('ALTER TABLE %I ADD COLUMN permission_epoch BIGINT NOT NULL DEFAULT 0',t);
 END LOOP;
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_task_version() RETURNS TRIGGER
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 DECLARE uid BIGINT;wid BIGINT;previous TEXT;selected BOOLEAN;
 BEGIN
  wid:=NEW.workspace_id;uid:=COALESCE((to_jsonb(NEW)->>'actor_id')::bigint,(to_jsonb(NEW)->>'user_id')::bigint);
  previous:=current_setting('dbhub.workspace_id',true);PERFORM set_config('dbhub.workspace_id',wid::text,true);
  selected:=dbhub_iam_is_target(uid);
  IF selected AND COALESCE(current_setting('dbhub.runtime_bridge',true),'')<>'phase1-bridge-2' THEN
   RAISE EXCEPTION 'task requires current IAM execution bridge' USING ERRCODE='0A000';
  END IF;
  IF TG_OP='INSERT' AND selected THEN
   NEW.permission_bridge:='phase1-bridge-2';
   SELECT config_version,authorization_epoch INTO NEW.permission_config,NEW.permission_epoch FROM sys_iam_workspace_state WHERE workspace_id=wid;
  ELSIF TG_OP='UPDATE' AND OLD.permission_bridge='phase1-bridge-2' AND COALESCE(current_setting('dbhub.runtime_bridge',true),'')<>'phase1-bridge-2' THEN
   RAISE EXCEPTION 'old worker cannot process a new permission task' USING ERRCODE='0A000';
  END IF;
  PERFORM set_config('dbhub.workspace_id',COALESCE(previous,''),true);
  RETURN NEW;
 END $body$$f$,ns);
 FOREACH t IN ARRAY ARRAY['sys_source_tasks','sys_report_subscription_runs','sys_webhook_deliveries','sys_query_executions','sys_report_exports'] LOOP
 EXECUTE format('CREATE TRIGGER iam_task_version BEFORE INSERT OR UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION dbhub_iam_task_version()',t);
 END LOOP;
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_identity_changed() RETURNS TRIGGER
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 BEGIN
 IF (NEW.is_active,NEW.auth_version) IS DISTINCT FROM (OLD.is_active,OLD.auth_version) THEN
  UPDATE sys_workspace_members SET version=version+1 WHERE user_id=NEW.id;
  UPDATE sys_iam_workspace_state SET authorization_epoch=authorization_epoch+1 WHERE workspace_id IN(SELECT workspace_id FROM sys_workspace_members WHERE user_id=NEW.id);
 END IF;RETURN NEW;
 END $body$$f$,ns);
 CREATE TRIGGER iam_identity_changed AFTER UPDATE ON sys_users FOR EACH ROW EXECUTE FUNCTION dbhub_iam_identity_changed();
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_workspace_changed() RETURNS TRIGGER
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 BEGIN
 IF NEW.enabled IS DISTINCT FROM OLD.enabled THEN
  UPDATE sys_iam_workspace_state SET authorization_epoch=authorization_epoch+1 WHERE workspace_id=NEW.id;
 END IF;RETURN NEW;
 END $body$$f$,ns);
 CREATE TRIGGER iam_workspace_changed AFTER UPDATE ON sys_workspaces FOR EACH ROW EXECUTE FUNCTION dbhub_iam_workspace_changed();
END $migration$;
