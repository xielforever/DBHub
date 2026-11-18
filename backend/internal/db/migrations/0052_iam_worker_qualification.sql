-- Global schedulers must rebind the original workspace for each owner before
-- generating work. This changes no original workflow, file or receipt version.
DO $migration$ DECLARE ns TEXT:=current_schema();
BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_task_allowed(wid BIGINT,uid BIGINT,action TEXT) RETURNS BOOLEAN
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 DECLARE previous TEXT;allowed BOOLEAN;
 BEGIN
  previous:=current_setting('dbhub.workspace_id',true);
  PERFORM set_config('dbhub.workspace_id',wid::text,true);
  allowed:=dbhub_iam_qualified(uid) AND NOT dbhub_iam_action_denied(uid,action)
   AND (NOT dbhub_iam_is_target(uid) OR dbhub_iam_has_action(uid,action));
  PERFORM set_config('dbhub.workspace_id',COALESCE(previous,''),true);
  RETURN COALESCE(allowed,false);
 END $body$$f$,ns);
END $migration$;
