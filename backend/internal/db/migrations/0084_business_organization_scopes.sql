-- P2-02 stores complete tuples, scoped to one identity and one dataset.
CREATE TABLE sys_business_organization_scopes (
    workspace_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    identity_version BIGINT NOT NULL CHECK (identity_version > 0),
    dataset_version BIGINT NOT NULL CHECK (dataset_version > 0),
    authority VARCHAR(128) NOT NULL CHECK (btrim(authority) <> ''),
    source_version VARCHAR(128) NOT NULL CHECK (btrim(source_version) <> ''),
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL CHECK (valid_until > valid_from),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (workspace_id, user_id, dataset_key),
    FOREIGN KEY (workspace_id, user_id) REFERENCES sys_business_identities(workspace_id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, dataset_key) REFERENCES sys_business_datasets(workspace_id, dataset_key)
);
CREATE INDEX business_organization_scopes_dataset_idx ON sys_business_organization_scopes (workspace_id, dataset_key);

CREATE TABLE sys_business_organization_scope_entries (
    workspace_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    department_key VARCHAR(128) NOT NULL CHECK (btrim(department_key) = department_key),
    warehouse_key VARCHAR(128) NOT NULL CHECK (btrim(warehouse_key) = warehouse_key),
    transport_org_key VARCHAR(128) NOT NULL CHECK (btrim(transport_org_key) = transport_org_key),
    CHECK (department_key <> '' OR warehouse_key <> '' OR transport_org_key <> ''),
    PRIMARY KEY (workspace_id, user_id, dataset_key, department_key, warehouse_key, transport_org_key),
    FOREIGN KEY (workspace_id, user_id, dataset_key)
        REFERENCES sys_business_organization_scopes(workspace_id, user_id, dataset_key) ON DELETE CASCADE
);

DO $migration$
DECLARE relation TEXT;
BEGIN
    FOREACH relation IN ARRAY ARRAY['sys_business_organization_scopes', 'sys_business_organization_scope_entries'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', relation);
        EXECUTE format('CREATE POLICY business_mapping_admin ON %I
            USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed()
                AND dbhub_member_role(dbhub_actor()) = ''admin'' AND dbhub_has_capability(dbhub_actor(), ''datasource_manage''))', relation);
    END LOOP;
END $migration$;
