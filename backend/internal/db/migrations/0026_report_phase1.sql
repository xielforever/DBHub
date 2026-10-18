-- A report owns a query definition, never another user's credentials. Keep the
-- immutable target revision when the original personal binding is removed.
ALTER TABLE sys_reports ADD COLUMN definition_revision_id BIGINT REFERENCES sys_source_definitions(id);
UPDATE sys_reports r SET definition_revision_id=c.definition_revision_id
 FROM sys_connections c WHERE c.id=r.connection_id;
ALTER TABLE sys_reports ALTER COLUMN definition_revision_id SET NOT NULL;
CREATE INDEX report_definition_revision ON sys_reports(definition_revision_id);
ALTER TABLE sys_reports DROP CONSTRAINT sys_reports_connection_id_fkey;
ALTER TABLE sys_reports ALTER COLUMN connection_id DROP NOT NULL;
ALTER TABLE sys_reports ADD FOREIGN KEY(connection_id) REFERENCES sys_connections(id) ON DELETE SET NULL;
ALTER TABLE sys_reports ADD COLUMN version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE sys_reports ADD COLUMN creation_key UUID;
ALTER TABLE sys_reports ADD COLUMN creation_hash TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX report_creation_key ON sys_reports(owner_user_id,creation_key) WHERE creation_key IS NOT NULL;
