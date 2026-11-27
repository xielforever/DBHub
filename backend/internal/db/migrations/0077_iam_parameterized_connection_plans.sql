-- The direct binding and single-row personal connection plans have uniform
-- selectivity across principals. Reuse their parameterized plans immediately,
-- avoiding a custom-plan series on every new connection. Other helpers retain
-- automatic selection; no qualification result survives a statement.
ALTER FUNCTION dbhub_iam_bind_workspace(BIGINT,BIGINT) SET plan_cache_mode=force_generic_plan;
ALTER FUNCTION dbhub_connection_list(TEXT,TEXT,BIGINT) SET plan_cache_mode=force_generic_plan;
