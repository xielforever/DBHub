-- Revision history and stop-impact checks count bindings by immutable revision.
-- PostgreSQL does not automatically index referencing foreign-key columns.
CREATE INDEX connection_definition_revision ON sys_connections(definition_revision_id);
