-- The navigation prefetch uses the existing directory helper directly. Its
-- subject lookup is uniformly selective; retain one parameterized plan from
-- the first call instead of custom planning on each new metadata connection.
-- Both detailed modes still read current memberships, deadlines and freezes.
ALTER FUNCTION dbhub_iam_workspace_directory(BIGINT,BOOLEAN) SET plan_cache_mode=force_generic_plan;
