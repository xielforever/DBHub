CREATE TABLE sys_iam_isolated_subscription_reviews (
    workspace_id BIGINT NOT NULL,
    report_id BIGINT NOT NULL,
    PRIMARY KEY (workspace_id,report_id),
    FOREIGN KEY (workspace_id,report_id) REFERENCES sys_iam_isolated_report_reviews(workspace_id,report_id)
);
ALTER TABLE sys_iam_isolated_subscription_reviews ENABLE ROW LEVEL SECURITY;
CREATE POLICY isolated_subscription_review_scope ON sys_iam_isolated_subscription_reviews FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed());

ALTER TABLE sys_report_subscriptions
    ADD COLUMN review_stamp TEXT NOT NULL DEFAULT '' CHECK (review_stamp='' OR review_stamp ~ '^[0-9a-f]{64}$'),
    ADD COLUMN review_until TIMESTAMPTZ,
    ADD CONSTRAINT personal_subscription_review_expiry CHECK (review_stamp<>'' OR review_until IS NULL);

CREATE TABLE sys_iam_isolated_subscription_grants (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    user_id BIGINT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('create','review')),
    valid_until TIMESTAMPTZ NOT NULL,
    UNIQUE (workspace_id,dataset_key,user_id,action),
    FOREIGN KEY (workspace_id,dataset_key) REFERENCES sys_business_datasets(workspace_id,dataset_key),
    FOREIGN KEY (workspace_id,user_id) REFERENCES sys_workspace_members(workspace_id,user_id) ON DELETE CASCADE
);
ALTER TABLE sys_iam_isolated_subscription_grants ENABLE ROW LEVEL SECURITY;

DO $migration$
DECLARE ns TEXT := current_schema();
DECLARE relation TEXT;
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_isolated_subscription_grant(p_report BIGINT,p_action TEXT) RETURNS BIGINT
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT g.id FROM sys_iam_isolated_subscription_reviews s
        JOIN sys_iam_isolated_business_reports b USING(workspace_id,report_id)
        JOIN sys_iam_isolation_guard i ON i.isolation_id=b.isolation_id AND i.database_name=current_database()
        JOIN sys_iam_isolated_subscription_grants g ON g.workspace_id=b.workspace_id AND g.dataset_key=b.dataset_key
        WHERE s.workspace_id=dbhub_workspace() AND s.report_id=p_report AND g.user_id=dbhub_actor() AND g.action=p_action
        AND g.valid_until>statement_timestamp() AND dbhub_scope_allowed()
        AND b.isolation_id=current_setting('dbhub.isolated_subscription_key',true)
        AND NOT dbhub_iam_action_denied(dbhub_actor(),CASE g.action WHEN 'create' THEN 'report.subscribeTeam' ELSE 'report.subscribeReview' END)
        $body$$f$, ns);
    EXECUTE format($f$CREATE FUNCTION dbhub_isolated_subscription_review_dataset() RETURNS TEXT
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT b.dataset_key::text FROM sys_iam_isolated_business_reports b
        WHERE b.workspace_id=dbhub_workspace()
        AND b.report_id::text=current_setting('dbhub.isolated_subscription_report',true)
        AND dbhub_isolated_subscription_grant(b.report_id,'review') IS NOT NULL
        $body$$f$, ns);
    FOREACH relation IN ARRAY ARRAY['sys_business_datasets','sys_business_identity_datasets',
        'sys_business_organization_scopes','sys_business_organization_scope_entries',
        'sys_business_row_column_policies','sys_business_output_policies'] LOOP
        EXECUTE format('CREATE POLICY isolated_subscription_review_dataset ON %I FOR SELECT
            USING (workspace_id=dbhub_workspace() AND dataset_key=dbhub_isolated_subscription_review_dataset())', relation);
    END LOOP;
END $migration$;

CREATE POLICY isolated_subscription_review_identity ON sys_business_identities FOR SELECT
    USING (workspace_id=dbhub_workspace() AND EXISTS (
        SELECT 1 FROM sys_business_identity_datasets b
        WHERE b.workspace_id=sys_business_identities.workspace_id AND b.user_id=sys_business_identities.user_id
        AND b.dataset_key=dbhub_isolated_subscription_review_dataset()));

CREATE TABLE sys_report_team_subscriptions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    owner_id BIGINT NOT NULL,
    owner_member_version BIGINT NOT NULL,
    report_id BIGINT NOT NULL,
    publication_no BIGINT NOT NULL CHECK (publication_no>0),
    previous_id BIGINT,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version>0),
    encrypted_definition TEXT NOT NULL,
    user_ids BIGINT[] NOT NULL,
    group_ids BIGINT[] NOT NULL,
    recipient_ids BIGINT[] NOT NULL CHECK (cardinality(recipient_ids)>0),
    owner_stamp TEXT NOT NULL,
    shape_stamp TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reviewer_id BIGINT,
    review_grant_id BIGINT,
    basis_sha256 TEXT,
    encrypted_approval TEXT,
    comment TEXT NOT NULL DEFAULT '',
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    decided_at TIMESTAMPTZ,
    review_until TIMESTAMPTZ,
    UNIQUE (workspace_id,id),
    UNIQUE (workspace_id,previous_id),
    FOREIGN KEY (workspace_id,report_id) REFERENCES sys_iam_isolated_subscription_reviews(workspace_id,report_id),
    FOREIGN KEY (workspace_id,previous_id) REFERENCES sys_report_team_subscriptions(workspace_id,id),
    CHECK (reviewer_id IS NULL OR reviewer_id<>owner_id AND NOT reviewer_id=ANY(recipient_ids)),
    CHECK ((status='pending' AND num_nonnulls(reviewer_id,review_grant_id,basis_sha256,decided_at)=0)
        OR (status<>'pending' AND num_nonnulls(reviewer_id,review_grant_id,basis_sha256,decided_at)=4)),
    CHECK ((status='approved' AND review_until=decided_at+interval '30 days' AND encrypted_approval IS NOT NULL)
        OR (status<>'approved' AND review_until IS NULL AND encrypted_approval IS NULL))
);
ALTER TABLE sys_report_team_subscriptions ENABLE ROW LEVEL SECURITY;
CREATE POLICY isolated_team_subscription_read ON sys_report_team_subscriptions FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND
        (owner_id=dbhub_actor() OR dbhub_isolated_subscription_grant(report_id,'review') IS NOT NULL));
CREATE POLICY isolated_team_subscription_submit ON sys_report_team_subscriptions FOR INSERT
    WITH CHECK (workspace_id=dbhub_workspace() AND owner_id=dbhub_actor() AND dbhub_scope_allowed()
        AND owner_member_version=dbhub_member_epoch() AND status='pending'
        AND dbhub_isolated_subscription_grant(report_id,'create') IS NOT NULL);
CREATE POLICY isolated_team_subscription_decide ON sys_report_team_subscriptions FOR UPDATE
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND status='pending'
        AND owner_id<>dbhub_actor() AND NOT dbhub_actor()=ANY(recipient_ids)
        AND dbhub_isolated_subscription_grant(report_id,'review') IS NOT NULL)
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND status<>'pending'
        AND reviewer_id=dbhub_actor() AND dbhub_isolated_subscription_grant(report_id,'review')=review_grant_id);

DO $migration$
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_isolated_subscription_owner_current(p_plan BIGINT) RETURNS BOOLEAN
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT EXISTS (SELECT 1 FROM sys_report_team_subscriptions s
        JOIN sys_iam_isolated_business_reports b ON b.workspace_id=s.workspace_id AND b.report_id=s.report_id
        JOIN sys_iam_isolated_subscription_grants g ON g.workspace_id=s.workspace_id AND g.dataset_key=b.dataset_key
            AND g.user_id=s.owner_id AND g.action='create'
        JOIN sys_workspace_members m ON m.workspace_id=s.workspace_id AND m.user_id=s.owner_id
        WHERE s.workspace_id=dbhub_workspace() AND s.id=p_plan AND m.version=s.owner_member_version
        AND g.valid_until>statement_timestamp() AND dbhub_member_role(s.owner_id) IS NOT NULL
        AND NOT dbhub_iam_action_denied(s.owner_id,'report.subscribeTeam')
        AND dbhub_isolated_subscription_grant(s.report_id,'review') IS NOT NULL)
        $body$$f$, current_schema());
END $migration$;
