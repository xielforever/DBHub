-- 0005_query_history.sql
-- 查询历史（对应 ER 图中的 sys_query_history，便于工作台回看与审计联动）

CREATE TABLE sys_query_history (
    id               BIGSERIAL PRIMARY KEY,
    connection_id    BIGINT NOT NULL REFERENCES sys_connections(id) ON DELETE CASCADE,
    user_id          BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    database_name    VARCHAR(100),
    sql_text         TEXT   NOT NULL,
    status           SMALLINT NOT NULL DEFAULT 1,          -- 1: 成功 0: 失败
    affected_rows    BIGINT,
    execution_time_ms INTEGER,
    error_message    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_query_history_conn ON sys_query_history(connection_id);
CREATE INDEX idx_query_history_user_time ON sys_query_history(user_id, created_at DESC);
