CREATE TABLE sys_sql_snippets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    name VARCHAR(200) NOT NULL,
    sql_text TEXT NOT NULL,
    database_name VARCHAR(200) NOT NULL DEFAULT '',
    connection_id BIGINT REFERENCES sys_connections(id) ON DELETE SET NULL,
    visibility VARCHAR(20) NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','shared')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sql_snippets_user_visibility ON sys_sql_snippets(user_id,visibility);

CREATE TABLE sys_query_policies (
    id BIGSERIAL PRIMARY KEY,
    connection_id BIGINT NOT NULL REFERENCES sys_connections(id) ON DELETE CASCADE,
    database_name TEXT NOT NULL DEFAULT '',
    schema_name TEXT NOT NULL DEFAULT '',
    table_name TEXT NOT NULL,
    role_code VARCHAR(30) NOT NULL REFERENCES sys_roles(code),
    column_permissions JSONB NOT NULL DEFAULT '{}'::jsonb,
    row_conditions JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_by BIGINT NOT NULL REFERENCES sys_users(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(connection_id,database_name,schema_name,table_name,role_code)
);

CREATE TABLE sys_query_settings (
    user_id BIGINT PRIMARY KEY REFERENCES sys_users(id) ON DELETE CASCADE,
    timeout_ms INTEGER NOT NULL DEFAULT 30000 CHECK (timeout_ms BETWEEN 1000 AND 120000),
    max_rows INTEGER NOT NULL DEFAULT 1000 CHECK (max_rows BETWEEN 1 AND 10000),
    block_unsafe_write BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE sys_ssh_tunnels ADD COLUMN IF NOT EXISTS host_key TEXT NOT NULL DEFAULT '';
ALTER TABLE sys_tickets ADD COLUMN IF NOT EXISTS executed_by BIGINT REFERENCES sys_users(id);
ALTER TABLE sys_tickets ADD COLUMN IF NOT EXISTS executed_at TIMESTAMPTZ;
ALTER TABLE sys_tickets ADD COLUMN IF NOT EXISTS execution_result JSONB;
ALTER TABLE sys_tickets ADD COLUMN IF NOT EXISTS execution_error TEXT;
