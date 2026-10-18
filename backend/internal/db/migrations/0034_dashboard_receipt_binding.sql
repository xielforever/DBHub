-- Receipts outlive refresh context cleanup. Keep only a private binding, never
-- SQL, credentials or rows, so a generic receipt route cannot bypass its parent.
ALTER TABLE sys_query_executions ADD COLUMN dashboard_refresh_id UUID;
ALTER TABLE sys_query_executions ADD COLUMN dashboard_tile_key TEXT NOT NULL DEFAULT '';
CREATE FUNCTION dbhub_execution_dashboard_binding() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.dashboard_refresh_id IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM sys_dashboard_refreshes f JOIN sys_dashboard_refresh_tiles t ON t.refresh_id=f.id
 WHERE f.workspace_id=NEW.workspace_id AND f.id=NEW.dashboard_refresh_id AND f.user_id=NEW.user_id
 AND t.tile_key=NEW.dashboard_tile_key AND t.execution_id=NEW.request_id) THEN
 RAISE EXCEPTION 'invalid workspace execution binding' USING ERRCODE='23503'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER execution_dashboard_binding BEFORE INSERT ON sys_query_executions FOR EACH ROW EXECUTE FUNCTION dbhub_execution_dashboard_binding();
