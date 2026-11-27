CREATE TABLE sys_iam_isolated_business_reports (
    workspace_id BIGINT NOT NULL,
    report_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    isolation_id TEXT NOT NULL REFERENCES sys_iam_isolation_guard(isolation_id),
    PRIMARY KEY (workspace_id, report_id),
    FOREIGN KEY (workspace_id, report_id) REFERENCES sys_managed_reports(workspace_id, id),
    FOREIGN KEY (workspace_id, dataset_key) REFERENCES sys_business_datasets(workspace_id, dataset_key)
);
ALTER TABLE sys_iam_isolated_business_reports ENABLE ROW LEVEL SECURITY;
CREATE POLICY isolated_business_report_scope ON sys_iam_isolated_business_reports FOR SELECT
    USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed());

ALTER TABLE sys_report_exports
    ADD COLUMN business_policy_stamp TEXT NOT NULL DEFAULT ''
    CHECK (business_policy_stamp = '' OR business_policy_stamp ~ '^[0-9a-f]{64}$');

-- Runtime callers can read classification for a bound report and only their
-- own identity and organization scope. This does not grant policy editing.
CREATE FUNCTION dbhub_isolated_business_report_access(p_dataset TEXT) RETURNS BOOLEAN
LANGUAGE SQL STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM sys_iam_isolated_business_reports b
        JOIN sys_iam_isolation_guard i ON i.isolation_id = b.isolation_id AND i.database_name = current_database()
        JOIN sys_managed_reports m ON m.workspace_id = b.workspace_id AND m.id = b.report_id
        LEFT JOIN sys_report_releases re ON re.id = m.active_release_id
        LEFT JOIN sys_report_reviews rv ON rv.id = re.review_id AND rv.status = 'approved'
        WHERE b.workspace_id = dbhub_workspace()
          AND b.report_id::text = current_setting('dbhub.isolated_business_report_id', true)
          AND b.isolation_id = current_setting('dbhub.isolated_business_report_key', true)
          AND (p_dataset IS NULL OR b.dataset_key = p_dataset)
          AND dbhub_scope_allowed()
          AND (dbhub_has_capability(dbhub_actor(), 'report_review')
               OR rv.manifest->'user_ids' @> jsonb_build_array(dbhub_actor())
               OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g
                          JOIN sys_user_group_members gm ON gm.group_id = g.value::bigint
                          WHERE gm.user_id = dbhub_actor()))
    )
$$;

CREATE POLICY isolated_report_identity_self ON sys_business_identities FOR SELECT
    USING (workspace_id = dbhub_workspace() AND user_id = dbhub_actor()
        AND dbhub_isolated_business_report_access(NULL));

DO $migration$
DECLARE relation TEXT;
BEGIN
    EXECUTE format('ALTER FUNCTION dbhub_isolated_business_report_access(TEXT) SET search_path = %I, pg_temp',
        current_schema());
    FOREACH relation IN ARRAY ARRAY['sys_business_datasets', 'sys_business_row_column_policies', 'sys_business_output_policies'] LOOP
        EXECUTE format('CREATE POLICY isolated_report_dataset ON %I FOR SELECT
            USING (workspace_id = dbhub_workspace() AND dbhub_isolated_business_report_access(dataset_key))', relation);
    END LOOP;
    FOREACH relation IN ARRAY ARRAY['sys_business_identity_datasets', 'sys_business_organization_scopes', 'sys_business_organization_scope_entries'] LOOP
        EXECUTE format('CREATE POLICY isolated_report_scope_self ON %I FOR SELECT
            USING (workspace_id = dbhub_workspace() AND user_id = dbhub_actor()
                AND dbhub_isolated_business_report_access(dataset_key))', relation);
    END LOOP;
END $migration$;
