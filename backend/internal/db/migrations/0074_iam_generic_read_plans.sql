-- These checked read helpers reuse parameterized plans across principals.
-- Avoid the initial custom-plan series on every newly opened pool connection.
-- Only execution plans are reused: rows, clocks, sources and prohibitions are
-- evaluated again in every statement. Leave the inline identity helper alone.
DO $migration$ DECLARE p RECORD;changed INT:=0;ns TEXT:=current_schema();
BEGIN
 FOR p IN SELECT f.proname,pg_get_function_identity_arguments(f.oid) AS args,l.lanname,f.prosecdef
 FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace JOIN pg_language l ON l.oid=f.prolang
 WHERE n.nspname=ns AND f.proname=ANY(ARRAY[
  'dbhub_iam_workspace_directory','dbhub_iam_binding','dbhub_iam_bind_workspace',
  'dbhub_iam_request_current','dbhub_iam_admit_current','dbhub_iam_current_target_action',
  'dbhub_iam_identity_directory','dbhub_connection_list','dbhub_inbox_todos']) LOOP
  IF p.lanname<>'plpgsql' OR p.prosecdef THEN RAISE EXCEPTION 'unexpected current read helper: %',p.proname;END IF;
  EXECUTE format('ALTER FUNCTION %I.%I(%s) SET plan_cache_mode=force_generic_plan',ns,p.proname,p.args);
  changed:=changed+1;
 END LOOP;
 IF changed<>9 THEN RAISE EXCEPTION 'incomplete current read plan inventory: %',changed;END IF;
END $migration$;
