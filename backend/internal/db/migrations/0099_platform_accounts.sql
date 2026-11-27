-- Standard account enrollment is explicit and creates no initial membership.
ALTER TABLE sys_users ADD COLUMN platform_account_managed BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE sys_users ADD COLUMN platform_account_version BIGINT NOT NULL DEFAULT 1 CHECK (platform_account_version > 0);

DROP TRIGGER user_initial_workspace ON sys_users;
CREATE TRIGGER user_initial_workspace AFTER INSERT ON sys_users
    FOR EACH ROW WHEN (NEW.workspace_account_id IS NULL AND NOT NEW.platform_account_managed)
    EXECUTE FUNCTION dbhub_join_initial_workspace();

CREATE TABLE sys_platform_account_credentials (
    user_id BIGINT PRIMARY KEY REFERENCES sys_users(id) ON DELETE CASCADE,
    token_hash CHAR(64) NOT NULL UNIQUE,
    password_version CHAR(64) NOT NULL,
    purpose TEXT NOT NULL CHECK (purpose IN ('activate','reset')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at),
    used_at TIMESTAMPTZ
);
CREATE INDEX platform_account_credential_expiry ON sys_platform_account_credentials (expires_at);
ALTER TABLE sys_platform_account_credentials ENABLE ROW LEVEL SECURITY;

CREATE INDEX workspace_creation_result_id ON sys_workspace_creation_commands ((result->>'id'), created_at);
