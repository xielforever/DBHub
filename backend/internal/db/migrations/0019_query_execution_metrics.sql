-- 指标仅扫描最近的未完成/未知回执，避免随成功历史增长扫描全表。
CREATE INDEX idx_query_executions_unfinished_metrics ON sys_query_executions(created_at,status,updated_at)
    WHERE status IN ('queued','running','unknown');
