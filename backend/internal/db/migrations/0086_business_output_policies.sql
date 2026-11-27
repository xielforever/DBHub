CREATE TABLE sys_business_output_policies (
    workspace_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    dataset_version BIGINT NOT NULL CHECK (dataset_version > 0),
    row_column_policy_version BIGINT NOT NULL CHECK (row_column_policy_version > 0),
    authority VARCHAR(128) NOT NULL CHECK (btrim(authority) <> ''),
    source_version VARCHAR(128) NOT NULL CHECK (btrim(source_version) <> ''),
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL CHECK (valid_until > valid_from),
    definition JSONB NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (workspace_id, dataset_key),
    FOREIGN KEY (workspace_id, dataset_key) REFERENCES sys_business_row_column_policies(workspace_id, dataset_key)
);

CREATE TABLE sys_business_reveal_requests (
    workspace_id BIGINT NOT NULL,
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    dataset_key VARCHAR(80) NOT NULL,
    requester_id BIGINT NOT NULL CHECK (requester_id > 0),
    identity_version BIGINT NOT NULL CHECK (identity_version > 0),
    dataset_version BIGINT NOT NULL CHECK (dataset_version > 0),
    scope_version BIGINT NOT NULL CHECK (scope_version > 0),
    policy_version BIGINT NOT NULL CHECK (policy_version > 0),
    output_version BIGINT NOT NULL CHECK (output_version > 0),
    purpose VARCHAR(128) NOT NULL CHECK (btrim(purpose) <> ''),
    records JSONB NOT NULL CHECK (jsonb_typeof(records) = 'array' AND jsonb_array_length(records) BETWEEN 1 AND 100),
    columns JSONB NOT NULL CHECK (jsonb_typeof(columns) = 'array' AND jsonb_array_length(columns) BETWEEN 1 AND 100),
    requested_until TIMESTAMPTZ NOT NULL,
    approved_until TIMESTAMPTZ,
    reviewer_id BIGINT CHECK (reviewer_id > 0 AND reviewer_id <> requester_id),
    reviewer_scope_version BIGINT CHECK (reviewer_scope_version > 0),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'revoked')),
    CHECK (state <> 'approved' OR (reviewer_id IS NOT NULL AND reviewer_scope_version IS NOT NULL AND approved_until IS NOT NULL)),
    PRIMARY KEY (workspace_id, id),
    FOREIGN KEY (workspace_id, dataset_key) REFERENCES sys_business_output_policies(workspace_id, dataset_key)
);
CREATE INDEX business_reveal_requests_dataset_idx ON sys_business_reveal_requests (workspace_id, dataset_key);

DO $migration$
DECLARE relation TEXT;
BEGIN
    FOREACH relation IN ARRAY ARRAY['sys_business_output_policies', 'sys_business_reveal_requests'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', relation);
        EXECUTE format('CREATE POLICY business_mapping_admin ON %I
            USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed()
                AND dbhub_member_role(dbhub_actor()) = ''admin'' AND dbhub_has_capability(dbhub_actor(), ''datasource_manage''))', relation);
    END LOOP;
END $migration$;
