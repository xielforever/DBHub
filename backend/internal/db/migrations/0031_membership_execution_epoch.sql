-- An old request must not regain authority when a membership is revoked and
-- later restored to the same role while its query/transaction is still alive.
CREATE FUNCTION dbhub_member_epoch() RETURNS BIGINT LANGUAGE SQL STABLE AS $$
 SELECT COALESCE(NULLIF(current_setting('dbhub.member_version',true),''),'0')::bigint
$$;
DO $migration$
DECLARE ns TEXT:=current_schema();
BEGIN
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_scope_allowed() RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT COALESCE(dbhub_member_role(dbhub_actor())=current_setting('dbhub.actor_role',true),false)
 AND EXISTS(SELECT 1 FROM sys_workspace_members m WHERE m.workspace_id=dbhub_workspace() AND m.user_id=dbhub_actor()
 AND (dbhub_member_epoch()=0 OR m.version=dbhub_member_epoch()))
 $body$$f$,ns);
END $migration$;
ALTER TABLE sys_dashboard_refreshes ADD COLUMN member_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sys_dashboard_refreshes ALTER COLUMN member_version SET DEFAULT dbhub_member_epoch();
