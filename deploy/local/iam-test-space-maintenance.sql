\set ON_ERROR_STOP on
-- Only the disposable, unpublished test metadata database. Retain all tables,
-- failure scenes and receipts; let PostgreSQL recycle its own WAL normally.
DO $$BEGIN
 IF current_database()<>'iam_test' OR current_user<>'iam_test' OR NOT EXISTS(
 SELECT 1 FROM pg_namespace WHERE nspname='iam_acceptance') THEN
 RAISE EXCEPTION 'isolated acceptance database required';
 END IF;
END$$;
SELECT name,setting,unit FROM pg_settings WHERE name IN('max_wal_size','min_wal_size','checkpoint_timeout');
ALTER SYSTEM SET max_wal_size='128MB';
ALTER SYSTEM SET min_wal_size='32MB';
SELECT pg_reload_conf();
CHECKPOINT;
