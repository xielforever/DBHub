\set ON_ERROR_STOP on
-- Metadata only. No password hashes, credentials, tokens, business rows or
-- function bodies are exported. This refuses the live metadata database.
DO $$BEGIN
 IF current_database()<>'iam_test' OR NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='iam_acceptance')
 THEN RAISE EXCEPTION 'isolated IAM acceptance database required';END IF;
END$$;
SELECT jsonb_build_object('format','IAM_DATABASE_INVENTORY_V1','schema','iam_acceptance',
 'functions',(SELECT jsonb_agg(jsonb_build_object('name',p.proname,'arguments',pg_get_function_identity_arguments(p.oid),
 'security_definer',p.prosecdef,'settings',p.proconfig,'definition_sha256',encode(sha256(convert_to(pg_get_functiondef(p.oid),'UTF8')),'hex'),
 'private_identity',p.proname=ANY(ARRAY['dbhub_iam_identity','dbhub_iam_identity_context','dbhub_iam_identity_directory','dbhub_iam_workspace_directory','dbhub_iam_binding','dbhub_iam_bind_workspace']),
 'private_control',p.proname=ANY(ARRAY['dbhub_iam_request_current','dbhub_iam_admit_current','dbhub_iam_current_target_action','dbhub_iam_release_approved','dbhub_iam_control_allowed']),
 'runtime_invoker_read',p.proname IN('dbhub_connection_list','dbhub_inbox_todos'),
 'runtime_qualification_guard',p.proname IN('dbhub_iam_runtime_current','dbhub_iam_runtime_admit')) ORDER BY p.proname,pg_get_function_identity_arguments(p.oid))
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='iam_acceptance' AND p.prokind='f'),
 'policies',(SELECT jsonb_agg(jsonb_build_object('table',tablename,'name',policyname,'command',cmd,'using',qual,'check',with_check) ORDER BY tablename,policyname) FROM pg_policies WHERE schemaname='iam_acceptance'),
 'migrations',(SELECT jsonb_agg(version ORDER BY version) FROM iam_acceptance.schema_migrations),
 'isolation_markers',(SELECT count(*) FROM iam_acceptance.sys_iam_isolation_guard),
 'metadata_only',true,'live_database_accessed',false);
