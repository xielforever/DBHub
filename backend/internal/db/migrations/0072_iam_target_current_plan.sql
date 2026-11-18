-- This private specialization is called only after the request check has read
-- and verified a current TARGET identity in this same statement. Keep the
-- existing dependency/source/ban resolver verbatim, substituting that identity
-- rather than joining it again. It is never callable by the tenant runtime.
DO $migration$ DECLARE ns TEXT:=current_schema();body TEXT;previous TEXT;
BEGIN
 SELECT p.prosrc INTO body FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=ns AND p.proname='dbhub_iam_has_action' AND p.pronargs=2;
 previous:=body;
 body:=replace(body,'SELECT qualified,target,data_role,legacy_caps FROM dbhub_iam_identity(uid)',
  'SELECT true AS qualified,true AS target,admitted_role AS data_role,ARRAY[]::text[] AS legacy_caps');
 IF body IS NULL OR body=previous THEN RAISE EXCEPTION 'unexpected current action resolver';END IF;
 EXECUTE format('CREATE FUNCTION dbhub_iam_current_target_action(uid BIGINT,action TEXT,admitted_role TEXT) RETURNS BOOLEAN LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=%I,pg_temp AS %L',ns,body);
 SELECT p.prosrc INTO body FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname=ns AND p.proname='dbhub_iam_request_current' AND p.pronargs=0;
 previous:=body;
 body:=replace(body,'SELECT * INTO identity_row FROM dbhub_iam_identity(dbhub_actor())',
  'SELECT qualified,target,data_role,membership_version,config_version,authorization_epoch INTO identity_row FROM dbhub_iam_identity(dbhub_actor())');
 body:=replace(body,'RETURN dbhub_iam_has_action(dbhub_actor(),action);',
  'RETURN dbhub_iam_current_target_action(dbhub_actor(),action,identity_row.data_role);');
 IF body IS NULL OR body=previous OR position('RETURN dbhub_iam_current_target_action' in body)=0 THEN
  RAISE EXCEPTION 'unexpected request current resolver';END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION dbhub_iam_request_current() RETURNS BOOLEAN LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=%I,pg_temp AS %L',ns,body);
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_current_target_action(BIGINT,TEXT,TEXT) FROM PUBLIC;
