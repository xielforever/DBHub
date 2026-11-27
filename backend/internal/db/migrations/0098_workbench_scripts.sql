CREATE TABLE sys_script_folders (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    parent_id BIGINT,
    name VARCHAR(200) NOT NULL CHECK (btrim(name) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id,user_id,id),
    FOREIGN KEY (workspace_id,user_id) REFERENCES sys_workspace_members(workspace_id,user_id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id,user_id,parent_id) REFERENCES sys_script_folders(workspace_id,user_id,id)
);
CREATE UNIQUE INDEX script_folder_siblings
    ON sys_script_folders (workspace_id,user_id,COALESCE(parent_id,0),lower(name));
ALTER TABLE sys_script_folders ENABLE ROW LEVEL SECURITY;
CREATE POLICY script_folder_owner ON sys_script_folders
    USING (workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND dbhub_scope_allowed())
    WITH CHECK (workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND dbhub_scope_allowed());

ALTER TABLE sys_sql_snippets ADD COLUMN script_type TEXT;
DELETE FROM sys_sql_snippets WHERE script_type IS NULL;
ALTER TABLE sys_sql_snippets
    ALTER COLUMN script_type SET NOT NULL,
    ADD CONSTRAINT workbench_script_type CHECK (script_type IN ('mysql','postgres','redis')),
    DROP COLUMN visibility,
    ADD COLUMN folder_id BIGINT,
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN bound_connection_id BIGINT,
    ADD COLUMN binding_signature TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT workbench_script_identity UNIQUE(workspace_id,user_id,id),
    ADD FOREIGN KEY (workspace_id,user_id,folder_id) REFERENCES sys_script_folders(workspace_id,user_id,id);
CREATE INDEX workbench_script_by_folder ON sys_sql_snippets(workspace_id,user_id,folder_id,updated_at DESC,id DESC);

CREATE TABLE sys_script_read_grants (
    workspace_id BIGINT NOT NULL,
    owner_user_id BIGINT NOT NULL,
    script_id BIGINT NOT NULL,
    reader_user_id BIGINT NOT NULL,
    reader_version BIGINT NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id,script_id,reader_user_id),
    CHECK (owner_user_id <> reader_user_id),
    FOREIGN KEY (workspace_id,owner_user_id,script_id)
        REFERENCES sys_sql_snippets(workspace_id,user_id,id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id,reader_user_id)
        REFERENCES sys_workspace_members(workspace_id,user_id) ON DELETE CASCADE
);
CREATE INDEX script_grants_by_reader ON sys_script_read_grants(workspace_id,reader_user_id,script_id);
ALTER TABLE sys_script_read_grants ENABLE ROW LEVEL SECURITY;
CREATE POLICY script_grants_owner ON sys_script_read_grants
    USING (workspace_id=dbhub_workspace() AND owner_user_id=dbhub_actor() AND dbhub_scope_allowed())
    WITH CHECK (workspace_id=dbhub_workspace() AND owner_user_id=dbhub_actor() AND dbhub_scope_allowed());
CREATE POLICY script_grants_read ON sys_script_read_grants FOR SELECT
    USING (workspace_id=dbhub_workspace() AND reader_user_id=dbhub_actor()
        AND reader_version=dbhub_member_epoch() AND dbhub_scope_allowed());

DROP POLICY workspace_scope ON sys_sql_snippets;
CREATE POLICY workbench_script_owner ON sys_sql_snippets
    USING (workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND dbhub_scope_allowed())
    WITH CHECK (workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND dbhub_scope_allowed());
CREATE POLICY workbench_script_read ON sys_sql_snippets FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND dbhub_member_role(user_id) IS NOT NULL AND EXISTS (
        SELECT 1 FROM sys_script_read_grants g
        WHERE g.workspace_id=sys_sql_snippets.workspace_id AND g.script_id=sys_sql_snippets.id
            AND g.reader_user_id=dbhub_actor() AND g.reader_version=dbhub_member_epoch()
    ));
