CREATE POLICY formal_export_file_owner_read ON sys_report_export_files FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND expires_at>statement_timestamp()
        AND EXISTS(SELECT 1 FROM sys_report_exports e WHERE e.workspace_id=sys_report_export_files.workspace_id
            AND e.id=export_id AND e.user_id=dbhub_actor() AND e.generation_member_version=dbhub_member_epoch()
            AND e.status='approved' AND e.generation_request<>''));

CREATE TABLE sys_report_export_credentials (
    digest CHAR(64) PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    export_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    member_version BIGINT NOT NULL,
    session_digest CHAR(64) NOT NULL,
    file_sha256 CHAR(64) NOT NULL,
    audience_stamp CHAR(64) NOT NULL,
    approval_version BIGINT NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    redeemed_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id,export_id) REFERENCES sys_report_export_files(workspace_id,export_id),
    CHECK (expires_at>issued_at AND expires_at<=issued_at+interval '10 minutes')
);
ALTER TABLE sys_report_export_credentials ENABLE ROW LEVEL SECURITY;
CREATE POLICY formal_export_credential_owner ON sys_report_export_credentials
    USING (workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND member_version=dbhub_member_epoch() AND dbhub_scope_allowed())
    WITH CHECK (workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND member_version=dbhub_member_epoch() AND dbhub_scope_allowed());

CREATE TABLE sys_iam_isolated_delivery_authorizers (
    workspace_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    user_id BIGINT NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id,dataset_key,user_id),
    FOREIGN KEY (workspace_id,dataset_key) REFERENCES sys_business_datasets(workspace_id,dataset_key),
    FOREIGN KEY (workspace_id,user_id) REFERENCES sys_workspace_members(workspace_id,user_id) ON DELETE CASCADE
);
ALTER TABLE sys_iam_isolated_delivery_authorizers ENABLE ROW LEVEL SECURITY;

CREATE TABLE sys_report_team_recipient_access (
    workspace_id BIGINT NOT NULL,
    plan_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    version BIGINT NOT NULL CHECK (version>0),
    member_version BIGINT NOT NULL CHECK (member_version>0),
    enabled BOOLEAN NOT NULL,
    needs_review BOOLEAN NOT NULL DEFAULT false CHECK (NOT needs_review OR NOT enabled),
    changed_by BIGINT NOT NULL,
    reason VARCHAR(500) NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (workspace_id,plan_id,user_id),
    FOREIGN KEY (workspace_id,plan_id) REFERENCES sys_report_team_subscriptions(workspace_id,id)
);
ALTER TABLE sys_report_team_recipient_access ENABLE ROW LEVEL SECURITY;

DO $migration$
DECLARE ns TEXT := current_schema();
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_team_delivery_authorizer(pid BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND NOT dbhub_iam_action_denied(dbhub_actor(),'report.authorize') AND EXISTS (
            SELECT 1 FROM sys_report_team_subscriptions p
            JOIN sys_iam_isolated_business_reports b ON b.workspace_id=p.workspace_id AND b.report_id=p.report_id
            JOIN sys_iam_isolation_guard i ON i.isolation_id=b.isolation_id AND i.database_name=current_database()
            JOIN sys_iam_isolated_delivery_authorizers a ON a.workspace_id=b.workspace_id AND a.dataset_key=b.dataset_key
            WHERE p.workspace_id=dbhub_workspace() AND p.id=pid AND b.isolation_id=current_setting('dbhub.isolated_subscription_key',true)
            AND a.user_id=dbhub_actor() AND a.valid_until>statement_timestamp()
            AND dbhub_actor()<>p.owner_id AND NOT dbhub_actor()=ANY(p.recipient_ids))
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_team_delivery_owner(pid BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND EXISTS (
            SELECT 1 FROM sys_report_team_subscriptions p WHERE p.workspace_id=dbhub_workspace()
            AND p.id=pid AND p.owner_id=dbhub_actor())
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_team_delivery_selected(pid BIGINT,uid BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND dbhub_member_role(uid) IS NOT NULL AND EXISTS (
            SELECT 1 FROM sys_report_team_subscriptions p
            JOIN sys_managed_reports m ON m.workspace_id=p.workspace_id AND m.id=p.report_id AND m.publication_no=p.publication_no
            JOIN sys_report_releases re ON re.id=m.active_release_id
            JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
            WHERE p.workspace_id=dbhub_workspace() AND p.id=pid
            AND uid=ANY(p.recipient_ids) AND (uid=ANY(p.user_ids) OR EXISTS(
                SELECT 1 FROM sys_user_group_members gm WHERE gm.user_id=uid AND gm.group_id=ANY(p.group_ids)))
            AND (rv.manifest->'user_ids' @> jsonb_build_array(uid) OR EXISTS(
                SELECT 1 FROM sys_user_group_members gm WHERE gm.user_id=uid
                AND rv.manifest->'group_ids' @> jsonb_build_array(gm.group_id)))
            AND dbhub_iam_task_allowed(p.workspace_id,uid,'report.view'))
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_team_delivery_live(pid BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND EXISTS (
            SELECT 1 FROM sys_report_team_subscriptions p
            JOIN sys_managed_reports m ON m.workspace_id=p.workspace_id AND m.id=p.report_id AND m.publication_no=p.publication_no
            JOIN sys_report_releases re ON re.id=m.active_release_id
            JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
            JOIN sys_iam_isolated_business_reports b ON b.workspace_id=p.workspace_id AND b.report_id=p.report_id
            JOIN sys_iam_isolation_guard i ON i.isolation_id=b.isolation_id AND i.database_name=current_database()
            JOIN sys_iam_isolated_subscription_grants g ON g.workspace_id=b.workspace_id AND g.dataset_key=b.dataset_key
            JOIN sys_workspace_members member ON member.workspace_id=p.workspace_id AND member.user_id=p.owner_id
            WHERE p.workspace_id=dbhub_workspace() AND p.id=pid
            AND b.isolation_id=current_setting('dbhub.isolated_subscription_key',true)
            AND p.status='approved' AND p.review_until>statement_timestamp()
            AND NOT EXISTS(SELECT 1 FROM sys_report_team_subscriptions later WHERE later.workspace_id=p.workspace_id AND later.previous_id=p.id)
            AND member.version=p.owner_member_version AND dbhub_member_role(p.owner_id) IS NOT NULL
            AND g.user_id=p.owner_id AND g.action='create' AND g.valid_until>statement_timestamp()
            AND NOT dbhub_iam_action_denied(p.owner_id,'report.subscribeTeam')
            AND dbhub_iam_task_allowed(p.workspace_id,p.owner_id,'report.view')
            AND (rv.manifest->'user_ids' @> jsonb_build_array(p.owner_id) OR EXISTS (
                SELECT 1 FROM sys_user_group_members gm WHERE gm.user_id=p.owner_id
                AND rv.manifest->'group_ids' @> jsonb_build_array(gm.group_id))))
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_team_delivery_recipient(pid BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND EXISTS (
            SELECT 1 FROM sys_report_team_recipient_access a WHERE a.workspace_id=dbhub_workspace()
            AND a.plan_id=pid AND a.user_id=dbhub_actor() AND a.enabled AND a.member_version=dbhub_member_epoch())
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_team_delivery_owner_scope(pid BIGINT,sv BIGINT,iv BIGINT,dv BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_team_delivery_live(pid) AND EXISTS (
            SELECT 1 FROM sys_report_team_subscriptions p
            JOIN sys_iam_isolated_business_reports b ON b.workspace_id=p.workspace_id AND b.report_id=p.report_id
            JOIN sys_business_organization_scopes o ON o.workspace_id=p.workspace_id AND o.user_id=p.owner_id AND o.dataset_key=b.dataset_key
            JOIN sys_business_identities i ON i.workspace_id=o.workspace_id AND i.user_id=o.user_id AND i.version=o.identity_version
            JOIN sys_business_identity_datasets d ON d.workspace_id=o.workspace_id AND d.user_id=o.user_id AND d.dataset_key=o.dataset_key
            WHERE p.workspace_id=dbhub_workspace() AND p.id=pid AND o.version=sv AND o.identity_version=iv
            AND o.dataset_version=dv AND d.dataset_version=dv
            AND o.valid_from<=statement_timestamp() AND o.valid_until>statement_timestamp()
            AND i.valid_from<=statement_timestamp() AND i.valid_until>statement_timestamp())
        $body$$f$,ns);
END $migration$;

CREATE POLICY team_recipient_access_read ON sys_report_team_recipient_access FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND
        (user_id=dbhub_actor() OR dbhub_team_delivery_authorizer(plan_id) OR dbhub_team_delivery_owner(plan_id)));
CREATE POLICY team_recipient_access_insert ON sys_report_team_recipient_access FOR INSERT
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_team_delivery_authorizer(plan_id)
        AND dbhub_team_delivery_selected(plan_id,user_id) AND dbhub_team_delivery_live(plan_id) AND changed_by=dbhub_actor());
CREATE POLICY team_recipient_access_update ON sys_report_team_recipient_access FOR UPDATE
    USING (workspace_id=dbhub_workspace() AND dbhub_team_delivery_authorizer(plan_id))
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_team_delivery_authorizer(plan_id) AND changed_by=dbhub_actor()
        AND (NOT enabled OR dbhub_team_delivery_selected(plan_id,user_id) AND dbhub_team_delivery_live(plan_id)));
CREATE POLICY isolated_team_delivery_context ON sys_report_team_subscriptions FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed()
        AND (dbhub_team_delivery_recipient(id) OR dbhub_team_delivery_authorizer(id)));

CREATE TABLE sys_report_team_periods (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    plan_id BIGINT NOT NULL,
    plan_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(workspace_id,plan_id,id),
    FOREIGN KEY (workspace_id,plan_id) REFERENCES sys_report_team_subscriptions(workspace_id,id)
);
ALTER TABLE sys_report_team_periods ENABLE ROW LEVEL SECURITY;
CREATE POLICY team_period_owner ON sys_report_team_periods
    USING (workspace_id=dbhub_workspace() AND dbhub_team_delivery_owner(plan_id))
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_team_delivery_owner(plan_id) AND dbhub_team_delivery_live(plan_id));

CREATE TABLE sys_report_team_deliveries (
    id UUID PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    plan_id BIGINT NOT NULL,
    period_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    grant_version BIGINT NOT NULL,
    member_version BIGINT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','skipped','failed','interrupted')),
    worker_token UUID,
    lease_until TIMESTAMPTZ,
    encrypted_snapshot TEXT NOT NULL DEFAULT '',
    snapshot_sha256 TEXT NOT NULL DEFAULT '',
    audience_stamp TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (workspace_id,period_id,user_id),
    FOREIGN KEY (workspace_id,plan_id,period_id) REFERENCES sys_report_team_periods(workspace_id,plan_id,id),
    CHECK ((status='running')=(worker_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK ((status='succeeded')=(encrypted_snapshot<>'' AND snapshot_sha256<>'' AND audience_stamp<>'' AND expires_at IS NOT NULL))
);
CREATE INDEX team_delivery_queue ON sys_report_team_deliveries(created_at,id) WHERE status='queued';
CREATE INDEX team_delivery_leases ON sys_report_team_deliveries(lease_until) WHERE status='running';
CREATE INDEX team_delivery_recipient ON sys_report_team_deliveries(workspace_id,user_id,created_at DESC);
ALTER TABLE sys_report_team_deliveries ENABLE ROW LEVEL SECURITY;
CREATE POLICY team_delivery_read ON sys_report_team_deliveries FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed()
        AND user_id=dbhub_actor());
CREATE POLICY team_delivery_enqueue ON sys_report_team_deliveries FOR INSERT
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_team_delivery_owner(plan_id) AND dbhub_team_delivery_live(plan_id)
        AND status IN ('queued','skipped'));
CREATE POLICY team_delivery_complete ON sys_report_team_deliveries FOR UPDATE
    USING (workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND status='running' AND dbhub_scope_allowed())
    WITH CHECK (workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND status='succeeded'
        AND dbhub_team_delivery_recipient(plan_id) AND dbhub_team_delivery_live(plan_id)
        AND dbhub_team_delivery_selected(plan_id,user_id));

DO $migration$
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_team_period_status(pid BIGINT,period BIGINT)
        RETURNS TABLE(id TEXT,plan_id BIGINT,period_id BIGINT,user_id BIGINT,status TEXT)
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT d.id::text,d.plan_id,d.period_id,d.user_id,d.status FROM sys_report_team_deliveries d
        WHERE d.workspace_id=dbhub_workspace() AND d.plan_id=pid AND d.period_id=period
        AND dbhub_team_delivery_owner(pid) ORDER BY d.user_id,d.id
        $body$$f$, current_schema());
END $migration$;
