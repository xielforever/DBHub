-- Permit PostgreSQL to project only the identity fields a checked operation
-- needs. The private implementation has no direct runtime EXECUTE privilege;
-- public authorization wrappers keep SECURITY DEFINER and their fixed paths.
DO $migration$ DECLARE ns TEXT:=current_schema();body TEXT;t TEXT;
BEGIN
 SELECT p.prosrc INTO body FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=ns AND p.proname='dbhub_iam_identity' AND p.pronargs=1;
 IF body IS NULL THEN RAISE EXCEPTION 'current identity implementation required';END IF;
 FOREACH t IN ARRAY ARRAY['sys_workspace_members','sys_users','sys_workspaces','sys_iam_workspace_state','sys_iam_principal_modes','sys_iam_revocations'] LOOP
  body:=replace(body,t,format('%I.%I',ns,t));
 END LOOP;
 body:=replace(body,'dbhub_workspace()',format('%I.dbhub_workspace()',ns));
 EXECUTE format('CREATE OR REPLACE FUNCTION dbhub_iam_identity(uid BIGINT) RETURNS TABLE(qualified BOOLEAN,target BOOLEAN,data_role TEXT,legacy_caps TEXT[],membership_version BIGINT,config_version BIGINT,authorization_epoch BIGINT,profile_until TIMESTAMPTZ,rollback_guard BOOLEAN) LANGUAGE SQL STABLE SECURITY INVOKER ROWS 1 AS %L',body);
 ALTER FUNCTION dbhub_iam_identity(BIGINT) RESET ALL;
 REVOKE EXECUTE ON FUNCTION dbhub_iam_identity(BIGINT) FROM PUBLIC;
END $migration$;
