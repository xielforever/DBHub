-- Reuse one current identity row inside this private request check. This is
-- statement-local evaluation, not an authorization cache. Retain the exact
-- scope, bridge, explicit prohibition and finite action dependency predicates.
CREATE OR REPLACE FUNCTION dbhub_iam_request_current() RETURNS BOOLEAN
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
DECLARE
 identity_row RECORD;
 action TEXT:=COALESCE(current_setting('dbhub.action_id',true),'');
 epoch BIGINT:=COALESCE(NULLIF(current_setting('dbhub.authorization_epoch',true),''),'0')::bigint;
 config BIGINT:=COALESCE(NULLIF(current_setting('dbhub.config_version',true),''),'0')::bigint;
BEGIN
 SELECT * INTO identity_row FROM dbhub_iam_identity(dbhub_actor());
 IF NOT FOUND OR identity_row.qualified IS NOT TRUE
  OR identity_row.data_role IS DISTINCT FROM current_setting('dbhub.actor_role',true)
  OR (dbhub_member_epoch()<>0 AND identity_row.membership_version IS DISTINCT FROM dbhub_member_epoch())
  OR (epoch<>0 AND identity_row.authorization_epoch IS DISTINCT FROM epoch)
  OR (config<>0 AND identity_row.config_version IS DISTINCT FROM config) THEN RETURN false;END IF;
 IF (identity_row.target OR EXISTS(SELECT 1 FROM sys_iam_revocations v
   WHERE v.workspace_id=dbhub_workspace() AND v.target_id=dbhub_actor() AND v.kind='action_revoke'))
  AND COALESCE(current_setting('dbhub.runtime_bridge',true),'')<>'phase1-bridge-2' THEN RETURN false;END IF;
 IF action<>'' AND dbhub_iam_action_denied(dbhub_actor(),action) THEN RETURN false;END IF;
 IF action='' OR NOT identity_row.target THEN RETURN true;END IF;
 RETURN dbhub_iam_has_action(dbhub_actor(),action);
END $body$;

-- Admission calls this only in a new statement after its advisory lock has
-- returned. Set transaction-local scope and check it in one round trip; final
-- delivery still calls the independent current check in a later statement.
CREATE FUNCTION dbhub_iam_admit_current(space BIGINT,uid BIGINT,data_role TEXT,
 member BIGINT,epoch BIGINT,config BIGINT,action TEXT,bridge TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $body$
BEGIN
 PERFORM set_config('dbhub.workspace_id',space::text,true),set_config('dbhub.actor_id',uid::text,true),
  set_config('dbhub.actor_role',data_role,true),set_config('dbhub.member_version',member::text,true),
  set_config('dbhub.authorization_epoch',epoch::text,true),set_config('dbhub.config_version',config::text,true),
  set_config('dbhub.action_id',action,true),set_config('dbhub.runtime_bridge',bridge,true);
 RETURN dbhub_iam_request_current();
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_request_current() SET search_path=%I,pg_temp',current_schema());
 EXECUTE format('ALTER FUNCTION dbhub_iam_admit_current(BIGINT,BIGINT,TEXT,BIGINT,BIGINT,BIGINT,TEXT,TEXT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_request_current(),dbhub_iam_admit_current(BIGINT,BIGINT,TEXT,BIGINT,BIGINT,BIGINT,TEXT,TEXT) FROM PUBLIC;
