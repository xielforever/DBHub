\set ON_ERROR_STOP on
SELECT jsonb_build_object('format','A52_SCHEMA_PROOF_V1','schemas',jsonb_object_agg(ns,proof)) FROM (
 SELECT ns,jsonb_build_object(
  'functions_sha256',(SELECT encode(sha256(convert_to(COALESCE(string_agg(pg_get_functiondef(p.oid),'' ORDER BY p.proname,pg_get_function_identity_arguments(p.oid)),''),'UTF8')),'hex') FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=ns AND p.prokind='f'),
  'policies_sha256',(SELECT encode(sha256(convert_to(COALESCE(string_agg(row::text,'' ORDER BY row::text),''),'UTF8')),'hex') FROM (SELECT to_jsonb(p) AS row FROM pg_policies p WHERE schemaname=ns)x),
  'tables',(SELECT count(*) FROM pg_tables WHERE schemaname=ns)) AS proof
 FROM unnest(ARRAY['iam_perf_baseline','iam_perf_candidate_v2']) ns
)x;
SELECT 'baseline',version FROM iam_perf_baseline.schema_migrations ORDER BY version;
SELECT 'candidate',version FROM iam_perf_candidate_v2.schema_migrations ORDER BY version;
