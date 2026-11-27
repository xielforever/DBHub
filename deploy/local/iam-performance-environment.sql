\set ON_ERROR_STOP on
DO $$BEGIN IF current_database()<>'iam_test' OR current_user<>'iam_test' THEN RAISE EXCEPTION 'isolated performance database required';END IF;END$$;
SELECT jsonb_build_object('format','A52_POSTGRES_SETTINGS_V1','version',version(),
 'settings',(SELECT jsonb_object_agg(name,setting||COALESCE(unit,'')) FROM pg_settings WHERE name=ANY(ARRAY['shared_buffers','work_mem','maintenance_work_mem','max_connections','max_parallel_workers','max_parallel_workers_per_gather','jit','max_wal_size','min_wal_size','server_encoding','TimeZone'])),
 'wal_tuning_note','After run 1, isolated tmpfs WAL changed from max/min 1024/80MB to 128/32MB; both builds share this setting. No schema or failure evidence removed.',
 'scope','both isolated builds, not the live PostgreSQL');
