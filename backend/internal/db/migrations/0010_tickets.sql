-- 0010_tickets.sql
-- 工单中心（Step26）：变更工单，支持 DDL/DML/索引三类，含血缘联动预填与审批流
-- 设计依据：docs/数据库设计/数据库模型设计.md §2.10 与 PRD M6 扩展

CREATE TABLE sys_tickets (
    id              BIGSERIAL PRIMARY KEY,
    title           VARCHAR(128) NOT NULL,
    type            VARCHAR(16)  NOT NULL CHECK (type IN ('ddl', 'dml', 'index')),
    table_name      VARCHAR(128) NOT NULL,
    database_name   VARCHAR(128) NOT NULL DEFAULT '',
    connection_id   BIGINT REFERENCES sys_connections(id) ON DELETE SET NULL,
    sql_text        TEXT NOT NULL,
    risk            VARCHAR(16)  NOT NULL CHECK (risk IN ('low', 'medium', 'high')),
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'rejected', 'executed')),
    requester_id    BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    reason          TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tickets_status ON sys_tickets(status);
CREATE INDEX idx_tickets_requester ON sys_tickets(requester_id);
CREATE INDEX idx_tickets_table ON sys_tickets(table_name);
CREATE INDEX idx_tickets_created ON sys_tickets(created_at DESC);

CREATE TRIGGER trg_sys_tickets_updated
    BEFORE UPDATE ON sys_tickets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE sys_tickets IS '变更工单，支持血缘一键建单与审批流';
COMMENT ON COLUMN sys_tickets.type IS 'ddl|dml|index';
COMMENT ON COLUMN sys_tickets.risk IS 'low|medium|high';
COMMENT ON COLUMN sys_tickets.status IS 'pending|approved|rejected|executed';
