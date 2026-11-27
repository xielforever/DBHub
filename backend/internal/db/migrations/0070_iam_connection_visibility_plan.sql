-- The personal-connection list already has the current source-definition row
-- under its original RLS. Evaluate that row's audience without fetching the
-- same definition and checking its scope a second time. All family/grant/group
-- relations retain invoker RLS; the member/data floor and legacy responsibility
-- predicates are unchanged and execute in this statement, never from a cache.
DO $migration$
DECLARE ns TEXT:=current_schema();body TEXT;previous TEXT;
BEGIN
 SELECT p.prosrc INTO STRICT body FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=ns AND p.proname='dbhub_connection_list' AND p.pronargs=3;
 previous:=body;
 body:=replace(body,'NOT dbhub_source_visible(sd.id,c.user_id)',
 $predicate$NOT ((SELECT dbhub_member_role(principal_uid) IS NOT NULL) AND EXISTS(
  SELECT 1 FROM sys_source_families f WHERE f.family_id=sd.family_id AND (
   (sd.visibility='private' AND sd.owner_id=principal_uid) OR
   (sd.visibility='global' AND (
    (SELECT dbhub_iam_legacy_capability(principal_uid,'datasource_manage')) OR
    f.access_mode='global' OR EXISTS(
     SELECT 1 FROM sys_source_grants g WHERE g.family_id=f.family_id AND
      (g.user_id=principal_uid OR EXISTS(
       SELECT 1 FROM sys_user_group_members m WHERE m.group_id=g.group_id AND m.user_id=principal_uid))))))))$predicate$);
 IF body=previous THEN RAISE EXCEPTION 'unexpected connection visibility read';END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION dbhub_connection_list(dbtype TEXT,keyword TEXT,principal_uid BIGINT) RETURNS %s LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=%I,pg_temp AS %L',
  pg_get_function_result((quote_ident(ns)||'.dbhub_connection_list(text,text,bigint)')::regprocedure),ns,body);
END $migration$;
