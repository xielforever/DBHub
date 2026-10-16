-- 恢复执行状态时保留实际数据库名称，避免用户核实了错误的数据库。
ALTER TABLE sys_query_executions ADD COLUMN database_name TEXT NOT NULL DEFAULT '';
