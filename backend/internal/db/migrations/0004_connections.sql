-- 0004_connections.sql
-- 目标数据库连接配置（password 使用 AES-256-GCM 加密存储）

CREATE TABLE sys_connections (
    id                  BIGSERIAL PRIMARY KEY,
    user_id             BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    ssh_tunnel_id       BIGINT REFERENCES sys_ssh_tunnels(id) ON DELETE SET NULL,

    name                VARCHAR(100) NOT NULL,
    type                VARCHAR(20)  NOT NULL CHECK (type IN ('mysql', 'postgres', 'redis', 'mongo')),

    -- 连接详情
    host                VARCHAR(255) NOT NULL,
    port                INTEGER      NOT NULL,
    database            VARCHAR(100),
    username            VARCHAR(100),
    password            TEXT,                                -- 加密存储

    -- 高级选项
    ssl_mode            VARCHAR(20)  NOT NULL DEFAULT 'require',
    connection_timeout  INTEGER      NOT NULL DEFAULT 10,

    -- UI 颜色标记
    color_label         VARCHAR(20),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_conn_user ON sys_connections(user_id);

CREATE TRIGGER trg_sys_connections_updated
    BEFORE UPDATE ON sys_connections
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
