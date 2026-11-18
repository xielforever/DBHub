-- Reuse prepared SQL plans for the checked scalar wrappers. SQL-language
-- SECURITY DEFINER wrappers rebuilt nested plans for each invocation; neither
-- membership, source, expiry nor revocation results are cached here.
DO $migration$ DECLARE ns TEXT:=current_schema();p RECORD;body TEXT;count_changed INT:=0;
BEGIN
 FOR p IN SELECT fn.*,pg_get_function_identity_arguments(fn.oid) AS args,pg_get_function_result(fn.oid) AS result
 FROM pg_proc fn JOIN pg_namespace n ON n.oid=fn.pronamespace JOIN pg_language l ON l.oid=fn.prolang
 WHERE n.nspname=ns AND l.lanname='sql' AND fn.proname=ANY(ARRAY[
 'dbhub_iam_is_target','dbhub_iam_qualified','dbhub_member_role','dbhub_iam_legacy_capability',
 'dbhub_iam_action_denied','dbhub_iam_action_basis','dbhub_iam_has_action',
 'dbhub_has_capability','dbhub_scope_allowed','dbhub_source_visible']) LOOP
  IF p.provolatile<>'s' OR p.proretset OR p.result NOT IN('boolean','text') THEN
   RAISE EXCEPTION 'unexpected checked wrapper properties: %',p.proname;
  END IF;
  body:='BEGIN RETURN ('||rtrim(p.prosrc,E'\n\r ;\t')||'); END;';
  EXECUTE format('CREATE OR REPLACE FUNCTION %I.%I(%s) RETURNS %s LANGUAGE plpgsql STABLE SECURITY %s SET search_path=%I,pg_temp AS %L',
   ns,p.proname,p.args,p.result,CASE WHEN p.prosecdef THEN 'DEFINER' ELSE 'INVOKER' END,ns,body);
  count_changed:=count_changed+1;
 END LOOP;
 IF count_changed<>10 THEN RAISE EXCEPTION 'incomplete checked wrapper inventory: %',count_changed;END IF;
 -- The private table-returning identity helper deliberately stays inline SQL
 -- with no PUBLIC execution, allowing checked wrappers to project few fields.
END $migration$;
