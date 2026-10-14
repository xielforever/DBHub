-- 0011_lineage.sql
-- 表级血缘缓存（M5 预留，Step27 后端解析落库，当前 handler 先按快照+历史+报表实时计算，缓存为未来优化）
-- 设计依据：docs/项目管控/数据资产与报表模块PRD.md §M5 与 docs/数据库设计 §2.10 规划（Step27 实现）

CREATE TABLE sys_meta_lineage (
    id              BIGSERIAL PRIMARY KEY,
    upstream_connection_id BIGINT NOT NULL REFERENCES sys_connections(id) ON DELETE CASCADE,
    upstream_database      VARCHAR(128) NOT NULL,
    upstream_schema        VARCHAR(128) NOT NULL DEFAULT '',
    upstream_table         VARCHAR(128) NOT NULL,
    downstream_connection_id BIGINT NOT NULL REFERENCES sys_connections(id) ON DELETE CASCADE,
    downstream_database    VARCHAR(128) NOT NULL,
    downstream_schema      VARCHAR(128) NOT NULL DEFAULT '',
    downstream_table       VARCHAR(128) NOT NULL,
    edge_kind       VARCHAR(16) NOT NULL CHECK (edge_kind IN ('fk','sql','report')),
    sql_fingerprint VARCHAR(64) NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_lineage_edge UNIQUE (upstream_connection_id, upstream_database, upstream_schema, upstream_table,
                                      downstream_connection_id, downstream_database, downstream_schema, downstream_table, edge_kind)
);

CREATE INDEX idx_lineage_upstream ON sys_meta_lineage(upstream_connection_id, upstream_database, upstream_table);
CREATE INDEX idx_lineage_downstream ON sys_meta_lineage(downstream_connection_id, downstream_database, downstream_table);
CREATE INDEX idx_lineage_kind ON sys_meta_lineage(edge_kind);

COMMENT ON TABLE sys_meta_lineage IS '表级血缘边缓存，外键/SQL/报表三类，Step27 实时计算为主，缓存为未来定时任务优化';
