CREATE TABLE sys_iam_isolated_dashboard_delivery (
    workspace_id BIGINT NOT NULL, dashboard_id BIGINT NOT NULL,
    PRIMARY KEY(workspace_id,dashboard_id),
    FOREIGN KEY(workspace_id,dashboard_id) REFERENCES sys_iam_isolated_dashboard_reviews(workspace_id,dashboard_id)
);
ALTER TABLE sys_iam_isolated_dashboard_delivery ENABLE ROW LEVEL SECURITY;
CREATE POLICY dashboard_delivery_enrollment_read ON sys_iam_isolated_dashboard_delivery FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed());

CREATE TABLE sys_iam_dashboard_delivery_grants (
    workspace_id BIGINT NOT NULL, dataset_key TEXT NOT NULL, user_id BIGINT NOT NULL,
    action TEXT NOT NULL CHECK(action IN('dashboard.exportReview','dashboard.subscribeReview','dashboard.subscribeTeam','dashboard.authorize')),
    valid_until TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(workspace_id,dataset_key,user_id,action),
    FOREIGN KEY(workspace_id,dataset_key) REFERENCES sys_business_datasets(workspace_id,dataset_key),
    FOREIGN KEY(workspace_id,user_id) REFERENCES sys_workspace_members(workspace_id,user_id) ON DELETE CASCADE
);
ALTER TABLE sys_iam_dashboard_delivery_grants ENABLE ROW LEVEL SECURITY;

CREATE TABLE sys_dashboard_delivery_plans (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL, dashboard_id BIGINT NOT NULL, publication_no BIGINT NOT NULL,
    owner_id BIGINT NOT NULL, owner_member_version BIGINT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN('export','personal','team')),
    version BIGINT NOT NULL DEFAULT 1, status TEXT NOT NULL CHECK(status IN('pending','approved','rejected')),
    dataset_keys TEXT[] NOT NULL CHECK(cardinality(dataset_keys)>0),
    recipient_ids BIGINT[] NOT NULL CHECK(cardinality(recipient_ids) BETWEEN 1 AND 100),
    direct_ids BIGINT[] NOT NULL, group_ids BIGINT[] NOT NULL,
    request_payload TEXT NOT NULL, approval_payload TEXT NOT NULL DEFAULT '',
    reviewer_id BIGINT, basis_sha256 TEXT, review_until TIMESTAMPTZ, decided_at TIMESTAMPTZ,
    previous_id BIGINT, interval_minutes INTEGER, checked_slot TIMESTAMPTZ,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    UNIQUE(workspace_id,id), UNIQUE(workspace_id,previous_id),
    FOREIGN KEY(workspace_id,dashboard_id) REFERENCES sys_iam_isolated_dashboard_delivery(workspace_id,dashboard_id),
    FOREIGN KEY(workspace_id,previous_id) REFERENCES sys_dashboard_delivery_plans(workspace_id,id),
    CHECK ((kind='export' AND interval_minutes IS NULL) OR
           (kind<>'export' AND interval_minutes IS NOT NULL AND interval_minutes BETWEEN 5 AND 10080)),
    CHECK ((status='approved')=(approval_payload<>'' AND review_until IS NOT NULL AND decided_at IS NOT NULL)),
    CHECK (reviewer_id IS NULL OR reviewer_id<>owner_id AND NOT reviewer_id=ANY(recipient_ids))
);

CREATE TABLE sys_dashboard_recipient_access (
    workspace_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, user_id BIGINT NOT NULL,
    version BIGINT NOT NULL CHECK(version>0), member_version BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL, needs_review BOOLEAN NOT NULL DEFAULT false,
    changed_by BIGINT NOT NULL, reason VARCHAR(500) NOT NULL,
    PRIMARY KEY(workspace_id,plan_id,user_id),
    FOREIGN KEY(workspace_id,plan_id) REFERENCES sys_dashboard_delivery_plans(workspace_id,id),
    CHECK(NOT needs_review OR NOT enabled)
);

CREATE TABLE sys_dashboard_deliveries (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, user_id BIGINT NOT NULL,
    grant_version BIGINT NOT NULL, member_version BIGINT NOT NULL, slot TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK(status IN('queued','running','succeeded','skipped','failed','interrupted')),
    worker_token UUID, lease_until TIMESTAMPTZ,
    payload TEXT NOT NULL DEFAULT '', evidence TEXT NOT NULL DEFAULT '', sha256 TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ, payload_purged_at TIMESTAMPTZ,
    UNIQUE(workspace_id,id), UNIQUE(workspace_id,plan_id,user_id,slot),
    FOREIGN KEY(workspace_id,plan_id) REFERENCES sys_dashboard_delivery_plans(workspace_id,id),
    CHECK ((status='running')=(worker_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK ((status='succeeded')=(evidence<>'' AND sha256<>'' AND expires_at IS NOT NULL)),
    CHECK (status<>'succeeded' OR (payload<>'' AND payload_purged_at IS NULL) OR
           (payload='' AND payload_purged_at>=expires_at))
);
CREATE INDEX dashboard_delivery_queue ON sys_dashboard_deliveries(slot,id) WHERE status='queued';
CREATE INDEX dashboard_delivery_expiry ON sys_dashboard_deliveries(expires_at,id) WHERE payload<>'';
CREATE INDEX dashboard_delivery_recipient ON sys_dashboard_deliveries(workspace_id,user_id,id DESC);
CREATE INDEX dashboard_delivery_due ON sys_dashboard_delivery_plans(review_until,id) WHERE status='approved' AND kind<>'export';

CREATE TABLE sys_dashboard_delivery_credentials (
    digest CHAR(64) PRIMARY KEY, workspace_id BIGINT NOT NULL, delivery_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL, member_version BIGINT NOT NULL, session_digest CHAR(64) NOT NULL,
    file_sha256 TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL, redeemed_at TIMESTAMPTZ,
    FOREIGN KEY(workspace_id,delivery_id) REFERENCES sys_dashboard_deliveries(workspace_id,id)
);
CREATE INDEX dashboard_delivery_credential_expiry ON sys_dashboard_delivery_credentials(expires_at,digest);

CREATE TABLE sys_dashboard_delivery_alerts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, delivery_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL, active BOOLEAN NOT NULL, encrypted_value TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(), disposition VARCHAR(500),
    disposed_at TIMESTAMPTZ,
    UNIQUE(workspace_id,delivery_id),
    FOREIGN KEY(workspace_id,plan_id) REFERENCES sys_dashboard_delivery_plans(workspace_id,id),
    FOREIGN KEY(workspace_id,delivery_id) REFERENCES sys_dashboard_deliveries(workspace_id,id)
);
CREATE INDEX dashboard_delivery_alert_history ON sys_dashboard_delivery_alerts(workspace_id,plan_id,id DESC);

DO $migration$
DECLARE ns TEXT:=current_schema(); relation TEXT;
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_delivery_granted(keys TEXT[],uid BIGINT,wanted TEXT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND dbhub_member_role(uid) IS NOT NULL AND cardinality(keys)>0
           AND NOT dbhub_iam_action_denied(uid,wanted) AND NOT EXISTS(
               SELECT 1 FROM unnest(keys) dataset(key) WHERE NOT EXISTS(
                   SELECT 1 FROM sys_iam_dashboard_delivery_grants g WHERE g.workspace_id=dbhub_workspace()
                   AND g.dataset_key=dataset.key AND g.user_id=uid AND g.action=wanted AND g.valid_until>statement_timestamp()))
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_delivery_open(pid BIGINT,uid BIGINT,purpose TEXT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND EXISTS(SELECT 1 FROM sys_dashboard_delivery_plans p
        WHERE p.workspace_id=dbhub_workspace() AND p.id=pid AND CASE purpose
            WHEN 'owner' THEN p.owner_id=uid
            WHEN 'recipient' THEN uid=ANY(p.recipient_ids) AND (uid=ANY(p.direct_ids) OR EXISTS(
                SELECT 1 FROM sys_user_group_members gm WHERE gm.user_id=uid AND gm.group_id=ANY(p.group_ids)))
            WHEN 'authorize' THEN p.kind='team' AND uid<>p.owner_id AND NOT uid=ANY(p.recipient_ids)
                AND dbhub_dashboard_delivery_granted(p.dataset_keys,uid,'dashboard.authorize')
            WHEN 'review' THEN p.kind<>'personal' AND uid<>p.owner_id AND NOT uid=ANY(p.recipient_ids)
                AND dbhub_dashboard_delivery_granted(p.dataset_keys,uid,CASE p.kind WHEN 'export' THEN 'dashboard.exportReview' ELSE 'dashboard.subscribeReview' END)
            ELSE false END)
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_delivery_live(pid BIGINT,executing BOOLEAN) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND EXISTS(SELECT 1 FROM sys_dashboard_delivery_plans p
            JOIN sys_managed_dashboards d ON d.workspace_id=p.workspace_id AND d.id=p.dashboard_id AND d.lifecycle='active'
            JOIN sys_dashboard_releases re ON re.id=d.active_release_id
            JOIN sys_dashboard_reviews rv ON rv.id=re.review_id AND rv.status='approved'
            JOIN sys_dashboard_versions v ON v.id=rv.version_id
            JOIN sys_iam_isolated_dashboard_reviews b ON b.workspace_id=p.workspace_id AND b.dashboard_id=p.dashboard_id
            JOIN sys_iam_isolation_guard i ON i.isolation_id=b.isolation_id AND i.database_name=current_database()
            JOIN sys_workspace_members m ON m.workspace_id=p.workspace_id AND m.user_id=p.owner_id AND m.version=p.owner_member_version
            WHERE p.workspace_id=dbhub_workspace() AND p.id=pid AND p.status='approved'
            AND b.isolation_id=current_setting('dbhub.dashboard_delivery_isolation',true)
            AND dbhub_iam_task_allowed(p.workspace_id,p.owner_id,'dashboard.view')
            AND NOT dbhub_iam_action_denied(p.owner_id,CASE p.kind WHEN 'export' THEN 'dashboard.export'
                  WHEN 'personal' THEN 'dashboard.subscribe' ELSE 'dashboard.subscribeTeam' END)
            AND (v.definition->'user_ids' @> jsonb_build_array(p.owner_id) OR EXISTS(
                SELECT 1 FROM sys_user_group_members gm WHERE gm.user_id=p.owner_id AND v.definition->'group_ids' @> jsonb_build_array(gm.group_id)))
            AND (p.kind<>'team' OR dbhub_dashboard_delivery_granted(p.dataset_keys,p.owner_id,'dashboard.subscribeTeam'))
            AND (NOT executing OR p.review_until>statement_timestamp() AND d.publication_no=p.publication_no AND NOT EXISTS(
                SELECT 1 FROM sys_dashboard_delivery_plans n WHERE n.workspace_id=p.workspace_id AND n.previous_id=p.id)))
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_delivery_review_dataset(key TEXT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT EXISTS(SELECT 1 FROM sys_dashboard_delivery_plans p JOIN sys_iam_isolated_dashboard_reviews b
            ON b.workspace_id=p.workspace_id AND b.dashboard_id=p.dashboard_id JOIN sys_iam_isolation_guard i
            ON i.isolation_id=b.isolation_id AND i.database_name=current_database()
            WHERE p.workspace_id=dbhub_workspace() AND p.id::text=current_setting('dbhub.dashboard_delivery_review',true)
            AND b.isolation_id=current_setting('dbhub.dashboard_delivery_isolation',true) AND p.status='pending'
            AND key=ANY(p.dataset_keys) AND dbhub_dashboard_delivery_open(p.id,dbhub_actor(),'review'))
        $body$$f$,ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_delivery_owner_scope(pid BIGINT,rid BIGINT,key TEXT,sv BIGINT,iv BIGINT,dv BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT EXISTS(SELECT 1 FROM sys_dashboard_delivery_plans p
            JOIN sys_business_organization_scopes o ON o.workspace_id=p.workspace_id AND o.user_id=p.owner_id AND o.dataset_key=key
            JOIN sys_business_identities i ON i.workspace_id=o.workspace_id AND i.user_id=o.user_id AND i.version=o.identity_version
            JOIN sys_business_identity_datasets b ON b.workspace_id=o.workspace_id AND b.user_id=o.user_id AND b.dataset_key=o.dataset_key
            JOIN sys_managed_reports r ON r.workspace_id=p.workspace_id AND r.id=rid AND r.active_release_id IS NOT NULL
            JOIN sys_report_releases re ON re.id=r.active_release_id
            JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
            WHERE p.workspace_id=dbhub_workspace() AND p.id=pid AND o.version=sv AND o.identity_version=iv
              AND key=ANY(p.dataset_keys) AND (p.owner_id=dbhub_actor() OR
                dbhub_dashboard_delivery_open(pid,dbhub_actor(),'recipient') OR dbhub_dashboard_delivery_open(pid,dbhub_actor(),'review'))
              AND o.dataset_version=dv AND b.dataset_version=dv AND o.valid_from<=statement_timestamp() AND o.valid_until>statement_timestamp()
              AND i.valid_from<=statement_timestamp() AND i.valid_until>statement_timestamp()
              AND dbhub_iam_task_allowed(p.workspace_id,p.owner_id,'report.view')
              AND (rv.manifest->'user_ids' @> jsonb_build_array(p.owner_id) OR EXISTS(
                SELECT 1 FROM sys_user_group_members gm WHERE gm.user_id=p.owner_id AND rv.manifest->'group_ids' @> jsonb_build_array(gm.group_id))))
        $body$$f$,ns);
    FOREACH relation IN ARRAY ARRAY['sys_business_datasets','sys_business_identity_datasets',
        'sys_business_organization_scopes','sys_business_organization_scope_entries','sys_business_row_column_policies'] LOOP
        EXECUTE format('CREATE POLICY dashboard_delivery_review_dataset ON %I FOR SELECT USING
            (workspace_id=dbhub_workspace() AND dbhub_dashboard_delivery_review_dataset(dataset_key))',relation);
    END LOOP;
END $migration$;

CREATE POLICY dashboard_delivery_review_identity ON sys_business_identities FOR SELECT
    USING(workspace_id=dbhub_workspace() AND EXISTS(SELECT 1 FROM sys_business_identity_datasets b
        WHERE b.workspace_id=sys_business_identities.workspace_id AND b.user_id=sys_business_identities.user_id
        AND dbhub_dashboard_delivery_review_dataset(b.dataset_key)));

ALTER TABLE sys_dashboard_delivery_plans ENABLE ROW LEVEL SECURITY;
CREATE POLICY dashboard_delivery_plan_read ON sys_dashboard_delivery_plans FOR SELECT
    USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND (owner_id=dbhub_actor()
        OR dbhub_actor()=ANY(recipient_ids) OR dbhub_dashboard_delivery_open(id,dbhub_actor(),'review')
        OR dbhub_dashboard_delivery_open(id,dbhub_actor(),'authorize')));
CREATE POLICY dashboard_delivery_plan_submit ON sys_dashboard_delivery_plans FOR INSERT
    WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND owner_id=dbhub_actor()
        AND owner_member_version=dbhub_member_epoch() AND (status='pending' OR kind='personal' AND status='approved'));
CREATE POLICY dashboard_delivery_plan_review ON sys_dashboard_delivery_plans FOR UPDATE
    USING(workspace_id=dbhub_workspace() AND status='pending' AND dbhub_dashboard_delivery_open(id,dbhub_actor(),'review'))
    WITH CHECK(workspace_id=dbhub_workspace() AND status IN('approved','rejected') AND reviewer_id=dbhub_actor()
        AND dbhub_dashboard_delivery_open(id,dbhub_actor(),'review'));

ALTER TABLE sys_dashboard_recipient_access ENABLE ROW LEVEL SECURITY;
CREATE POLICY dashboard_delivery_access_read ON sys_dashboard_recipient_access FOR SELECT
    USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND (user_id=dbhub_actor()
       OR dbhub_dashboard_delivery_open(plan_id,dbhub_actor(),'authorize') OR dbhub_dashboard_delivery_open(plan_id,dbhub_actor(),'owner')));
CREATE POLICY dashboard_delivery_access_write ON sys_dashboard_recipient_access FOR ALL
    USING(workspace_id=dbhub_workspace() AND dbhub_dashboard_delivery_open(plan_id,dbhub_actor(),'authorize'))
    WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_dashboard_delivery_open(plan_id,dbhub_actor(),'authorize')
       AND changed_by=dbhub_actor() AND (NOT enabled OR dbhub_dashboard_delivery_open(plan_id,user_id,'recipient')));

ALTER TABLE sys_dashboard_deliveries ENABLE ROW LEVEL SECURITY;
CREATE POLICY dashboard_delivery_read ON sys_dashboard_deliveries FOR SELECT
    USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND user_id=dbhub_actor());
CREATE POLICY dashboard_delivery_finish ON sys_dashboard_deliveries FOR UPDATE
    USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND user_id=dbhub_actor() AND status='running')
    WITH CHECK(workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND status='succeeded'
       AND dbhub_dashboard_delivery_live(plan_id,true));

ALTER TABLE sys_dashboard_delivery_credentials ENABLE ROW LEVEL SECURITY;
CREATE POLICY dashboard_delivery_credential_owner ON sys_dashboard_delivery_credentials
    USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND user_id=dbhub_actor() AND member_version=dbhub_member_epoch())
    WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND user_id=dbhub_actor() AND member_version=dbhub_member_epoch());

ALTER TABLE sys_dashboard_delivery_alerts ENABLE ROW LEVEL SECURITY;
CREATE POLICY dashboard_delivery_alert_owner ON sys_dashboard_delivery_alerts
    USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND user_id=dbhub_actor())
    WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND user_id=dbhub_actor());

DO $migration$
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_delivery_enqueue(pid BIGINT,p_slot TIMESTAMPTZ) RETURNS VOID
        LANGUAGE SQL VOLATILE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        INSERT INTO sys_dashboard_deliveries(workspace_id,plan_id,user_id,grant_version,member_version,slot,status)
        SELECT p.workspace_id,p.id,uid,COALESCE(a.version,0),COALESCE(m.version,0),p_slot,
            CASE WHEN dbhub_dashboard_delivery_open(p.id,uid,'recipient') AND m.active AND
                (p.kind<>'team' OR a.enabled AND a.member_version=m.version) THEN 'queued' ELSE 'skipped' END
        FROM sys_dashboard_delivery_plans p CROSS JOIN LATERAL unnest(p.recipient_ids) roster(uid)
        LEFT JOIN sys_dashboard_recipient_access a ON a.workspace_id=p.workspace_id AND a.plan_id=p.id AND a.user_id=uid
        LEFT JOIN sys_workspace_members m ON m.workspace_id=p.workspace_id AND m.user_id=uid
        WHERE p.workspace_id=dbhub_workspace() AND p.id=pid AND dbhub_dashboard_delivery_live(pid,true)
          AND ((p.kind='export' AND p_slot=p.decided_at AND (p.reviewer_id=dbhub_actor() AND dbhub_dashboard_delivery_open(pid,dbhub_actor(),'review')))
            OR (p.kind<>'export' AND p.owner_id=dbhub_actor() AND p_slot>p.decided_at
              AND p_slot=date_bin(make_interval(mins=>p.interval_minutes),statement_timestamp(),p.decided_at)))
        ON CONFLICT(workspace_id,plan_id,user_id,slot) DO NOTHING
        $body$$f$,current_schema());
    EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_delivery_statuses(pid BIGINT)
        RETURNS TABLE(id BIGINT,user_id BIGINT,slot TIMESTAMPTZ,status TEXT,expires_at TIMESTAMPTZ)
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT d.id,d.user_id,d.slot,d.status,d.expires_at FROM sys_dashboard_deliveries d
        WHERE d.workspace_id=dbhub_workspace() AND d.plan_id=pid AND dbhub_dashboard_delivery_open(pid,dbhub_actor(),'owner')
        ORDER BY d.id DESC LIMIT 100
        $body$$f$,current_schema());
END $migration$;
