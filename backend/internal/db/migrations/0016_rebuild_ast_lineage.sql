-- 旧版本由正则与 SELECT 共现生成的 SQL 边无法证明数据流向。
-- 仅清理可从成功查询历史重建的派生缓存；外键和人工资产均保留。
DELETE FROM sys_meta_lineage WHERE edge_kind='sql';
