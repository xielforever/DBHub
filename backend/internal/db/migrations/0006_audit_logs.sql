-- 0006_audit_logs.sql
-- 操作审计日志（按月范围分区 + DEFAULT 分区兜底，避免跨期写入失败）

CREATE TABLE sys_audit_logs (
    id              BIGSERIAL,
    user_id         BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
    connection_id   BIGINT REFERENCES sys_connections(id) ON DELETE SET NULL,

    trace_id        VARCHAR(64),                          -- 请求追踪 ID
    action          VARCHAR(50) NOT NULL,                 -- 例如 QUERY / CONNECT / DELETE
    resource_type   VARCHAR(50),                          -- 例如 DATABASE / CONNECTION
    resource_name   VARCHAR(255),

    ip_address      INET,
    user_agent      TEXT,

    status          SMALLINT NOT NULL DEFAULT 1,          -- 1: 成功 0: 失败
    error_message   TEXT,

    duration_ms     INTEGER,                              -- 执行耗时
    params          JSONB,                                -- 操作参数快照（敏感字段须脱敏）

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_audit_logs_user_time ON sys_audit_logs(user_id, created_at DESC);
CREATE INDEX idx_audit_logs_action_time ON sys_audit_logs(action, created_at DESC);

-- 2026 年各月分区（后续通过迁移按月扩展，DEFAULT 分区兜底）
CREATE TABLE sys_audit_logs_y2026m01 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
CREATE TABLE sys_audit_logs_y2026m02 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-02-01') TO ('2026-03-01');
CREATE TABLE sys_audit_logs_y2026m03 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-03-01') TO ('2026-04-01');
CREATE TABLE sys_audit_logs_y2026m04 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE sys_audit_logs_y2026m05 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE sys_audit_logs_y2026m06 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
CREATE TABLE sys_audit_logs_y2026m07 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
CREATE TABLE sys_audit_logs_y2026m08 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
CREATE TABLE sys_audit_logs_y2026m09 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
CREATE TABLE sys_audit_logs_y2026m10 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-10-01') TO ('2026-11-01');
CREATE TABLE sys_audit_logs_y2026m11 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-11-01') TO ('2026-12-01');
CREATE TABLE sys_audit_logs_y2026m12 PARTITION OF sys_audit_logs
    FOR VALUES FROM ('2026-12-01') TO ('2027-01-01');

CREATE TABLE sys_audit_logs_default PARTITION OF sys_audit_logs DEFAULT;
