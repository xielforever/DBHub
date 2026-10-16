-- 请求登记独立于目标数据库事务；回执不保存 SQL、参数或结果行。
-- 已登记的编号不得因超时、进程退出或缓存过期而重新取得执行权。
CREATE TABLE sys_query_executions (
    user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    request_id UUID NOT NULL,
    request_hash CHAR(64) NOT NULL,
    connection_id BIGINT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','succeeded','failed','rejected','unknown')),
    transactional BOOLEAN NOT NULL DEFAULT FALSE,
    kind VARCHAR(10) NOT NULL DEFAULT '',
    affected_rows BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, request_id)
);
