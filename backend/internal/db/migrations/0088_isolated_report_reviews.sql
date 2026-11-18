CREATE TABLE sys_iam_isolated_report_reviews (
    workspace_id BIGINT NOT NULL,
    report_id BIGINT NOT NULL,
    PRIMARY KEY (workspace_id, report_id),
    FOREIGN KEY (workspace_id, report_id)
        REFERENCES sys_iam_isolated_business_reports(workspace_id, report_id)
);

CREATE TABLE sys_iam_isolated_report_data_reviewers (
    workspace_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    user_id BIGINT NOT NULL,
    grant_id BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    valid_until TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id, dataset_key, user_id),
    FOREIGN KEY (workspace_id, dataset_key) REFERENCES sys_business_datasets(workspace_id, dataset_key),
    FOREIGN KEY (workspace_id, user_id) REFERENCES sys_workspace_members(workspace_id, user_id) ON DELETE CASCADE
);

ALTER TABLE sys_iam_isolated_report_reviews ENABLE ROW LEVEL SECURITY;
CREATE POLICY isolated_report_enrollment_read ON sys_iam_isolated_report_reviews FOR SELECT
    USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed());
ALTER TABLE sys_iam_isolated_report_data_reviewers ENABLE ROW LEVEL SECURITY;
-- An extension or regrant gets a fresh identity. Old signatures cannot revive.
CREATE TRIGGER isolated_report_data_grant_immutable BEFORE UPDATE ON sys_iam_isolated_report_data_reviewers
    FOR EACH ROW EXECUTE FUNCTION dbhub_report_history_actor_null('user_id');

ALTER TABLE sys_report_validations ADD COLUMN business_policy_stamp TEXT NOT NULL DEFAULT '';
CREATE TABLE sys_report_review_checks (
    workspace_id BIGINT NOT NULL,
    review_id BIGINT NOT NULL,
    boundary JSONB NOT NULL CHECK (jsonb_typeof(boundary) = 'object'),
    business_reviewer_id BIGINT NOT NULL CHECK (business_reviewer_id > 0),
    validation_id UUID NOT NULL REFERENCES sys_report_validations(id),
    business_comment VARCHAR(1000) NOT NULL,
    business_signed_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    business_stamp TEXT NOT NULL,
    requires_data BOOLEAN NOT NULL,
    data_reviewer_id BIGINT,
    data_grant_id BIGINT,
    data_stamp TEXT,
    data_comment VARCHAR(1000),
    data_signed_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, review_id),
    FOREIGN KEY (workspace_id, review_id) REFERENCES sys_report_reviews(workspace_id, id),
    CHECK ((data_reviewer_id IS NULL AND data_grant_id IS NULL AND data_stamp IS NULL AND data_signed_at IS NULL)
        OR (requires_data AND data_reviewer_id IS NOT NULL AND data_grant_id IS NOT NULL AND data_stamp IS NOT NULL AND data_signed_at IS NOT NULL))
);
ALTER TABLE sys_report_review_checks ENABLE ROW LEVEL SECURITY;
CREATE POLICY report_review_check_workspace ON sys_report_review_checks
    USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed());

DO $migration$
DECLARE ns TEXT := current_schema();
DECLARE relation TEXT;
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_isolated_report_review_dataset() RETURNS TEXT
        LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT b.dataset_key::text
        FROM sys_report_reviews rv
        JOIN sys_report_versions v ON v.workspace_id=rv.workspace_id AND v.id=rv.version_id
        JOIN sys_iam_isolated_report_reviews s ON s.workspace_id=v.workspace_id AND s.report_id=v.report_id
        JOIN sys_iam_isolated_business_reports b ON b.workspace_id=s.workspace_id AND b.report_id=s.report_id
        JOIN sys_iam_isolation_guard i ON i.isolation_id=b.isolation_id AND i.database_name=current_database()
        WHERE rv.workspace_id=dbhub_workspace() AND dbhub_scope_allowed()
          AND rv.id::text=current_setting('dbhub.isolated_report_review_id', true)
          AND b.report_id::text=current_setting('dbhub.isolated_business_report_id', true)
          AND b.isolation_id=current_setting('dbhub.isolated_business_report_key', true)
          AND (dbhub_has_capability(dbhub_actor(),'report_review') OR EXISTS (
              SELECT 1 FROM sys_iam_isolated_report_data_reviewers d
              WHERE d.workspace_id=b.workspace_id AND d.dataset_key=b.dataset_key
                AND d.user_id=dbhub_actor() AND d.valid_until>statement_timestamp()))
        $body$$f$, ns);
    FOREACH relation IN ARRAY ARRAY['sys_business_datasets', 'sys_business_identity_datasets',
        'sys_business_organization_scopes', 'sys_business_organization_scope_entries',
        'sys_business_row_column_policies', 'sys_business_output_policies', 'sys_iam_isolated_report_data_reviewers'] LOOP
        EXECUTE format('CREATE POLICY isolated_report_review_dataset ON %I FOR SELECT
            USING (workspace_id = dbhub_workspace() AND dataset_key = dbhub_isolated_report_review_dataset())', relation);
    END LOOP;
END $migration$;

CREATE POLICY isolated_report_review_identity ON sys_business_identities FOR SELECT
    USING (workspace_id = dbhub_workspace() AND EXISTS (
        SELECT 1 FROM sys_business_identity_datasets b WHERE b.workspace_id = sys_business_identities.workspace_id
            AND b.user_id = sys_business_identities.user_id AND b.dataset_key = dbhub_isolated_report_review_dataset()));
