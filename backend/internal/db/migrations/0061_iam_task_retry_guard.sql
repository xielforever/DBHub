-- Creating a child with a fresh ID must not downgrade an opaque future task.
-- This is also enforced for an older process that has no Go version check.
DO $migration$ DECLARE ns TEXT:=current_schema();body TEXT;previous TEXT;
BEGIN
 SELECT fn.prosrc INTO body FROM pg_proc fn JOIN pg_namespace n ON n.oid=fn.pronamespace
 WHERE n.nspname=ns AND fn.proname='dbhub_iam_task_version' AND fn.pronargs=0;
 previous:=body;
 body:=replace(body,'wid:=NEW.workspace_id;', $guard$
  IF TG_TABLE_NAME='sys_source_tasks' AND TG_OP='INSERT' THEN
   IF NEW.parent_id IS NOT NULL AND EXISTS(SELECT 1 FROM sys_source_tasks original
    WHERE original.id=NEW.parent_id AND original.permission_bridge NOT IN('legacy-frozen-v1','phase1-bridge-2')) THEN
    RAISE EXCEPTION 'unsupported task cannot be retried with a different bridge' USING ERRCODE='0A000';
   END IF;
  END IF;
  wid:=NEW.workspace_id;$guard$);
 IF body IS NULL OR body=previous THEN RAISE EXCEPTION 'unexpected task version guard';END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION dbhub_iam_task_version() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS %L',ns,body);
END $migration$;
