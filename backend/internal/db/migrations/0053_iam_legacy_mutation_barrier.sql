-- Legacy clients still use their original endpoints. Their membership/group
-- writes must nevertheless wait for admitted TARGET operations before ack.
DO $migration$ DECLARE ns TEXT:=current_schema();p RECORD;using_expr TEXT;check_expr TEXT;fn TEXT;
BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_legacy_mutation_barrier() RETURNS TRIGGER
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 DECLARE wid BIGINT;uid BIGINT;
 BEGIN
  IF TG_TABLE_NAME='sys_users' THEN
   IF (NEW.is_active,NEW.auth_version) IS NOT DISTINCT FROM (OLD.is_active,OLD.auth_version) THEN RETURN NEW;END IF;
   FOR wid IN SELECT workspace_id FROM sys_workspace_members WHERE user_id=NEW.id ORDER BY workspace_id LOOP
    PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:target:'||wid,0));
   END LOOP;
  ELSE
   wid:=CASE WHEN TG_OP='DELETE' THEN OLD.workspace_id ELSE NEW.workspace_id END;
   PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:target:'||wid,0));
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;
 END $body$$f$,ns);
 CREATE TRIGGER iam_legacy_member_barrier BEFORE INSERT OR UPDATE OR DELETE ON sys_workspace_members FOR EACH ROW EXECUTE FUNCTION dbhub_iam_legacy_mutation_barrier();
 CREATE TRIGGER iam_legacy_group_barrier BEFORE INSERT OR DELETE ON sys_user_group_members FOR EACH ROW EXECUTE FUNCTION dbhub_iam_legacy_mutation_barrier();
 CREATE TRIGGER iam_legacy_identity_barrier BEFORE UPDATE ON sys_users FOR EACH ROW EXECUTE FUNCTION dbhub_iam_legacy_mutation_barrier();
 -- These functions have no row arguments and are stable within one statement.
 -- InitPlans evaluate the identical current qualification once, retaining the
 -- original row/object predicates and post-execution qualification checks.
 FOR p IN SELECT c.relname,n.nspname,pol.polname,pg_get_expr(pol.polqual,pol.polrelid) AS u,pg_get_expr(pol.polwithcheck,pol.polrelid) AS c
 FROM pg_policy pol JOIN pg_class c ON c.oid=pol.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=ns LOOP
  using_expr:=p.u;check_expr:=p.c;
  FOREACH fn IN ARRAY ARRAY['dbhub_scope_allowed','dbhub_workspace','dbhub_actor','dbhub_member_epoch'] LOOP
   using_expr:=replace(using_expr,fn||'()', '(SELECT '||quote_ident(ns)||'.'||fn||'())');
   check_expr:=replace(check_expr,fn||'()', '(SELECT '||quote_ident(ns)||'.'||fn||'())');
  END LOOP;
  EXECUTE format('ALTER POLICY %I ON %I.%I%s%s',p.polname,ns,p.relname,
   CASE WHEN using_expr IS NULL THEN '' ELSE ' USING ('||using_expr||')' END,
   CASE WHEN check_expr IS NULL THEN '' ELSE ' WITH CHECK ('||check_expr||')' END);
 END LOOP;
END $migration$;
