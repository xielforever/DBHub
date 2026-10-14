-- 0012_lineage_enhance.sql
-- 增强血缘缓存：支持定时解析 sys_query_history（pg_query_go/vitess）落库，供 GET /assets/lineage 查询
-- 在 0011 基础上扩展 edge_kind、溯源与审计字段

-- 扩展 edge_kind 支持 dashboard（报表下游的仪表盘）
ALTER TABLE sys_meta_lineage DROP CONSTRAINT IF EXISTS uq_lineage_edge;
ALTER TABLE sys_meta_lineage DROP CONSTRAINT IF EXISTS sys_meta_lineage_edge_kind_check;
ALTER TABLE sys_meta_lineage ADD CONSTRAINT sys_meta_lineage_edge_kind_check CHECK (edge_kind IN ('fk','sql','report','dashboard'));

-- 溯源：触发该边的历史记录 ID（可空，FK 解析时为 NULL）
ALTER TABLE sys_meta_lineage ADD COLUMN IF NOT EXISTS source_history_id BIGINT;
-- 首次解析时间
ALTER TABLE sys_meta_lineage ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
-- 解析指纹已存在 sql_fingerprint，保留
-- 扩展唯一约束，加入指纹避免重复，兼容历史数据
CREATE UNIQUE INDEX IF NOT EXISTS uq_lineage_edge_v2 ON sys_meta_lineage(
  upstream_connection_id, upstream_database, upstream_schema, upstream_table,
  downstream_connection_id, downstream_database, downstream_schema, downstream_table,
  edge_kind, sql_fingerprint
);

-- 加速定时任务增量扫描
CREATE INDEX IF NOT EXISTS idx_lineage_source_history ON sys_meta_lineage(source_history_id);
CREATE INDEX IF NOT EXISTS idx_lineage_created ON sys_meta_lineage(created_at);

-- 统一 updated_at 行为
ALTER TABLE sys_meta_lineage ALTER COLUMN updated_at SET DEFAULT NOW();

COMMENT ON TABLE sys_meta_lineage IS '表级血缘边缓存：fk（外键）、sql（pg_query_go/vitess 解析 sys_query_history）、report/dashboard（报表依赖），定时任务增量落库，供 GET /api/v1/assets/lineage 优先查询';
COMMENT ON COLUMN sys_meta_lineage.source_history_id IS '溯源 sys_query_history.id，FK 解析时为 NULL';
