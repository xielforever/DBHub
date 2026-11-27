-- P2-01 stores explicit mapping metadata. Existing execution paths do not consult it.
CREATE TABLE sys_business_identities (
    workspace_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    member_version BIGINT NOT NULL CHECK (member_version > 0),
    authority VARCHAR(128) NOT NULL CHECK (btrim(authority) <> ''),
    business_key VARCHAR(128) NOT NULL CHECK (btrim(business_key) <> ''),
    source_version VARCHAR(128) NOT NULL CHECK (btrim(source_version) <> ''),
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL CHECK (valid_until > valid_from),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (workspace_id, user_id),
    UNIQUE (workspace_id, authority, business_key),
    FOREIGN KEY (workspace_id, user_id) REFERENCES sys_workspace_members(workspace_id, user_id) ON DELETE CASCADE
);

CREATE TABLE sys_business_datasets (
    workspace_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL CHECK (dataset_key ~ '^[a-z0-9][a-z0-9._-]{0,79}$'),
    account_id BIGINT NOT NULL,
    account_version BIGINT NOT NULL CHECK (account_version > 0),
    definition_revision_id BIGINT NOT NULL,
    authority VARCHAR(128) NOT NULL CHECK (btrim(authority) <> ''),
    source_version VARCHAR(128) NOT NULL CHECK (btrim(source_version) <> ''),
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ NOT NULL CHECK (valid_until > valid_from),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (workspace_id, dataset_key),
    FOREIGN KEY (workspace_id, account_id) REFERENCES sys_report_accounts(workspace_id, id)
);
CREATE INDEX business_datasets_account_idx ON sys_business_datasets (workspace_id, account_id);

CREATE TABLE sys_business_identity_datasets (
    workspace_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    dataset_key VARCHAR(80) NOT NULL,
    dataset_version BIGINT NOT NULL CHECK (dataset_version > 0),
    PRIMARY KEY (workspace_id, user_id, dataset_key),
    FOREIGN KEY (workspace_id, user_id) REFERENCES sys_business_identities(workspace_id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, dataset_key) REFERENCES sys_business_datasets(workspace_id, dataset_key)
);
CREATE INDEX business_identity_datasets_dataset_idx ON sys_business_identity_datasets (workspace_id, dataset_key);

DO $migration$
DECLARE relation TEXT;
BEGIN
    FOREACH relation IN ARRAY ARRAY['sys_business_identities', 'sys_business_datasets', 'sys_business_identity_datasets'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', relation);
        EXECUTE format('CREATE POLICY business_mapping_admin ON %I
            USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed()
                AND dbhub_member_role(dbhub_actor()) = ''admin'' AND dbhub_has_capability(dbhub_actor(), ''datasource_manage''))', relation);
    END LOOP;
END $migration$;
