-- 0007_meta_assets.sql
-- 数据资产模块（M1）：连接环境标记 + 元数据快照 + 人工标注（业务元数据）。
-- 设计依据：docs/界面设计/数据资产与报表模块设计方案.md §4.1 / §4.2。

-- 连接所属环境：dev 开发 / test 测试 / prod 生产（资产树徽标、M6 高危拦截地基）
ALTER TABLE sys_connections
    ADD COLUMN environment VARCHAR(10) NOT NULL DEFAULT 'dev'
        CHECK (environment IN ('dev', 'test', 'prod'));

-- 元数据采集快照（「同步字典」产物；人工标注不入本表，重新采集不覆盖人工内容）
CREATE TABLE sys_meta_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    connection_id   BIGINT NOT NULL REFERENCES sys_connections(id) ON DELETE CASCADE,
    database_name   VARCHAR(128) NOT NULL,
    schema_name     VARCHAR(128) NOT NULL DEFAULT '',
    table_name      VARCHAR(128) NOT NULL,
    table_type      VARCHAR(16)  NOT NULL DEFAULT 'table',     -- table | view
    table_comment   VARCHAR(512) NOT NULL DEFAULT '',          -- 来源库原生注释
    estimated_rows  BIGINT NOT NULL DEFAULT 0,
    data_bytes      BIGINT NOT NULL DEFAULT 0,
    raw_columns     JSONB NOT NULL DEFAULT '[]'::jsonb,         -- 列结构快照
    raw_indexes     JSONB NOT NULL DEFAULT '[]'::jsonb,
    raw_keys        JSONB NOT NULL DEFAULT '[]'::jsonb,        -- 主键/外键/唯一键
    ddl_text        TEXT NOT NULL DEFAULT '',
    synced_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_meta_snapshot_obj
        UNIQUE (connection_id, database_name, schema_name, table_name)
);

CREATE INDEX idx_meta_snap_conn ON sys_meta_snapshots(connection_id);
CREATE INDEX idx_meta_snap_name ON sys_meta_snapshots(table_name);

-- 人工标注：表级与列级共表，column_name 为空串表示表级标注
CREATE TABLE sys_asset_annotations (
    id              BIGSERIAL PRIMARY KEY,
    connection_id   BIGINT NOT NULL REFERENCES sys_connections(id) ON DELETE CASCADE,
    database_name   VARCHAR(128) NOT NULL,
    schema_name     VARCHAR(128) NOT NULL DEFAULT '',
    table_name      VARCHAR(128) NOT NULL,
    column_name     VARCHAR(128) NOT NULL DEFAULT '',
    owner_user_id   BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
    business_desc   VARCHAR(1000) NOT NULL DEFAULT '',
    tags            TEXT[] NOT NULL DEFAULT '{}',
    sensitivity     VARCHAR(16) NOT NULL DEFAULT 'normal'      -- normal | sensitive | confidential
        CHECK (sensitivity IN ('normal', 'sensitive', 'confidential')),
    starred_by      BIGINT[] NOT NULL DEFAULT '{}',
    updated_by      BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_asset_annotation_obj
        UNIQUE (connection_id, database_name, schema_name, table_name, column_name)
);

CREATE INDEX idx_asset_anno_owner ON sys_asset_annotations(owner_user_id);

CREATE TRIGGER trg_sys_asset_annotations_updated
    BEFORE UPDATE ON sys_asset_annotations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
