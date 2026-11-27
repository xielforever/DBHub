ALTER TABLE sys_managed_reports
    ADD COLUMN business_owner_id BIGINT REFERENCES sys_users(id),
    ADD COLUMN business_owner_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT report_business_owner_version CHECK (
        (business_owner_id IS NULL AND business_owner_version = 0) OR
        (business_owner_id IS NOT NULL AND business_owner_version > 0)
    );

ALTER TABLE sys_report_versions
    ADD COLUMN proposed_business_owner_id BIGINT CHECK (proposed_business_owner_id > 0);

ALTER TABLE sys_report_validations
    ADD COLUMN business_owner_version BIGINT CHECK (business_owner_version >= 0);

DO $migration$
DECLARE ns TEXT := current_schema();
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_report_owner_mode() RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND EXISTS (
            SELECT 1 FROM sys_iam_workspace_state
            WHERE workspace_id=dbhub_workspace() AND matrix_enabled)
        $body$$f$, ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_report_business_supervisor(uid BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_iam_builtin_role(uid,'business_supervisor') AND dbhub_iam_has_action(uid,'report.review')
        $body$$f$, ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_report_platform_admin(uid BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND EXISTS (
            SELECT 1 FROM sys_users u JOIN sys_users p ON p.id=COALESCE(u.workspace_account_id,u.id)
            WHERE u.id=uid AND u.is_active AND p.is_active AND p.is_platform_admin)
        $body$$f$, ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_report_lock_platform_admin() RETURNS BOOLEAN
        LANGUAGE SQL VOLATILE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT COALESCE((SELECT p.is_active AND p.is_platform_admin
            FROM sys_users u JOIN sys_users p ON p.id=COALESCE(u.workspace_account_id,u.id)
            WHERE u.id=dbhub_actor() AND u.is_active AND dbhub_scope_allowed() FOR SHARE OF p),false)
        $body$$f$, ns);
END $migration$;
