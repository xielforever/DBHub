-- Future task formats remain durable but cannot be claimed, approved or
-- completed by an executor that understands only the two registered bridges.
-- Neither a rollback nor an old worker may relabel the original task version.
DO $migration$ DECLARE ns TEXT:=current_schema();body TEXT;previous TEXT;
BEGIN
 SELECT p.prosrc INTO body FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=ns AND p.proname='dbhub_iam_task_version' AND p.pronargs=0;
 previous:=body;
 body:=replace(body,'wid:=NEW.workspace_id;', $guard$
  IF TG_OP='UPDATE' AND NEW.permission_bridge IS DISTINCT FROM OLD.permission_bridge THEN
   RAISE EXCEPTION 'task permission bridge is immutable' USING ERRCODE='0A000';
  END IF;
  IF NEW.permission_bridge NOT IN('legacy-frozen-v1','phase1-bridge-2')
   AND NEW.status IN('running','sending','succeeded','sent','approved') THEN
   RAISE EXCEPTION 'unsupported task permission bridge' USING ERRCODE='0A000';
  END IF;
  wid:=NEW.workspace_id;$guard$);
 IF body IS NULL OR body=previous THEN RAISE EXCEPTION 'unexpected task bridge guard definition';END IF;
 previous:=body;
 body:=replace(body,'IF TG_OP=''INSERT'' AND selected THEN',
  'IF TG_OP=''INSERT'' AND selected AND NEW.permission_bridge IN(''legacy-frozen-v1'',''phase1-bridge-2'') THEN');
 IF body=previous THEN RAISE EXCEPTION 'unexpected task bridge insert definition';END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION dbhub_iam_task_version() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS %L',ns,body);
END $migration$;
