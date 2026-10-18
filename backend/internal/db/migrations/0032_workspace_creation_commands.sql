-- Platform identity commands are not tenant data and cannot be read by the
-- workspace runtime role. A lost response must not create another organization.
CREATE TABLE sys_workspace_creation_commands (
 actor_id BIGINT NOT NULL, request_id UUID NOT NULL, request_hash CHAR(64) NOT NULL,
 result JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_id,request_id)
);
ALTER TABLE sys_workspace_creation_commands ENABLE ROW LEVEL SECURITY;
