-- A bounded Boolean guard lets the existing lease pool use the same restricted
-- role as tenant reads. No identity rows, sources, secrets or mutation helpers
-- become callable by that role; the private implementations remain private.
CREATE FUNCTION dbhub_iam_runtime_current() RETURNS BOOLEAN
LANGUAGE plpgsql STABLE SECURITY DEFINER AS $body$
BEGIN RETURN dbhub_iam_request_current();END $body$;
CREATE FUNCTION dbhub_iam_runtime_admit(space BIGINT,uid BIGINT,data_role TEXT,
 member BIGINT,epoch BIGINT,config BIGINT,action TEXT,bridge TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS $body$
BEGIN
 PERFORM set_config('dbhub.workspace_id',space::text,true),set_config('dbhub.actor_id',uid::text,true),
  set_config('dbhub.actor_role',data_role,true),set_config('dbhub.member_version',member::text,true),
  set_config('dbhub.authorization_epoch',epoch::text,true),set_config('dbhub.config_version',config::text,true),
  set_config('dbhub.action_id',action,true),set_config('dbhub.runtime_bridge',bridge,true);
 RETURN dbhub_iam_runtime_current();
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_runtime_current() SET search_path=%I,pg_temp',current_schema());
 EXECUTE format('ALTER FUNCTION dbhub_iam_runtime_admit(BIGINT,BIGINT,TEXT,BIGINT,BIGINT,BIGINT,TEXT,TEXT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_runtime_current(),dbhub_iam_runtime_admit(BIGINT,BIGINT,TEXT,BIGINT,BIGINT,BIGINT,TEXT,TEXT) FROM PUBLIC;
