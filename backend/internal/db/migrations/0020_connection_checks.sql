-- 最近一次针对已保存配置的连接检查；与配置更新时间绑定，修改后自动失效。
CREATE TABLE sys_connection_checks (
    connection_id BIGINT PRIMARY KEY REFERENCES sys_connections(id) ON DELETE CASCADE,
    config_updated_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    result JSONB NOT NULL
);
