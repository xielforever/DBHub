-- 0008_reports.sql
-- 报表中心与仪表盘（M3/M4）：报表、仪表盘、分享令牌。
-- 设计依据：docs/界面设计/数据资产与报表模块设计方案.md §4.3-4.5

-- 报表：一张图表 = 一次保存的查询 + 图表配置
CREATE TABLE sys_reports (
    id              BIGSERIAL PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    description     VARCHAR(512) NOT NULL DEFAULT '',
    connection_id   BIGINT NOT NULL REFERENCES sys_connections(id) ON DELETE CASCADE,
    database_name   VARCHAR(128) NOT NULL DEFAULT '',
    sql_text        TEXT NOT NULL,
    chart_type      VARCHAR(16) NOT NULL DEFAULT 'table'
        CHECK (chart_type IN ('table','bar','line','pie','metric')),
    chart_config    JSONB NOT NULL DEFAULT '{}'::jsonb,
    visibility      VARCHAR(16) NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private','shared')),
    owner_user_id   BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    starred_by      BIGINT[] NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_reports_owner ON sys_reports(owner_user_id);
CREATE INDEX idx_reports_conn ON sys_reports(connection_id);
CREATE INDEX idx_reports_visibility ON sys_reports(visibility);

CREATE TRIGGER trg_sys_reports_updated
    BEFORE UPDATE ON sys_reports
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- 仪表盘：报表的有序组合（M4 预留，M3 建表）
CREATE TABLE sys_dashboards (
    id              BIGSERIAL PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    description     VARCHAR(512) NOT NULL DEFAULT '',
    visibility      VARCHAR(16) NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private','shared')),
    layout          JSONB NOT NULL DEFAULT '[]'::jsonb,
    owner_user_id   BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    starred_by      BIGINT[] NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_dashboards_owner ON sys_dashboards(owner_user_id);
CREATE TRIGGER trg_sys_dashboards_updated
    BEFORE UPDATE ON sys_dashboards
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- 分享令牌：只存哈希，支持报表与仪表盘（M4）
CREATE TABLE sys_share_tokens (
    id              BIGSERIAL PRIMARY KEY,
    subject_type    VARCHAR(16) NOT NULL CHECK (subject_type IN ('report','dashboard')),
    subject_id      BIGINT NOT NULL,
    token_hash      VARCHAR(128) NOT NULL UNIQUE,
    created_by      BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
    expire_at       TIMESTAMPTZ,
    access_count    INT NOT NULL DEFAULT 0,
    revoked         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_share_subject ON sys_share_tokens(subject_type, subject_id);
CREATE INDEX idx_share_created_by ON sys_share_tokens(created_by);
