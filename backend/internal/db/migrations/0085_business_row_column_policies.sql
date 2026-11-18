-- P2-03 stores versioned conditions for a fixed approved dataset view.
CREATE TABLE sys_business_row_column_policies (
    workspace_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    dataset_version BIGINT NOT NULL CHECK (dataset_version > 0),
    authority VARCHAR(128) NOT NULL CHECK (btrim(authority) <> ''),
    source_version VARCHAR(128) NOT NULL CHECK (btrim(source_version) <> ''),
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL CHECK (valid_until > valid_from),
    definition JSONB NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (workspace_id, dataset_key),
    FOREIGN KEY (workspace_id, dataset_key) REFERENCES sys_business_datasets(workspace_id, dataset_key)
);

ALTER TABLE sys_business_row_column_policies ENABLE ROW LEVEL SECURITY;
CREATE POLICY business_mapping_admin ON sys_business_row_column_policies
    USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed()
        AND dbhub_member_role(dbhub_actor()) = 'admin' AND dbhub_has_capability(dbhub_actor(), 'datasource_manage'));
