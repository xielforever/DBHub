-- A generic governance route may be entered through another allowed action.
-- Its domain operation still needs its own exact cap and dependency closure.
-- This private predicate supplements the finite delegation; it grants nothing
-- on its own and never supplies a wildcard for unknown/future capabilities.
CREATE FUNCTION dbhub_iam_control_allowed(principal_uid BIGINT,capability TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
BEGIN
 IF dbhub_iam_is_target(principal_uid) THEN
  RETURN dbhub_iam_has_action(principal_uid,capability);
 END IF;
 RETURN COALESCE(dbhub_member_role(principal_uid)='admin' AND
  EXISTS(SELECT 1 FROM sys_iam_capabilities c WHERE c.action_id=capability AND c.scope='workspace' AND c.runtime_enabled)
  AND NOT EXISTS(
   WITH RECURSIVE needed(id,path,cycle) AS(
    SELECT capability,ARRAY[capability],false
    UNION ALL
    SELECT dep,n.path||dep,dep=ANY(n.path) FROM needed n
    JOIN sys_iam_capabilities c ON c.action_id=n.id
    CROSS JOIN LATERAL unnest(c.dependencies)dep WHERE NOT n.cycle
   )
   SELECT 1 FROM needed n LEFT JOIN sys_iam_capabilities c ON c.action_id=n.id
   WHERE n.cycle OR c.action_id IS NULL OR c.scope<>'workspace' OR NOT c.runtime_enabled
    OR dbhub_iam_action_denied(principal_uid,n.id)
  ),false);
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_control_allowed(BIGINT,TEXT) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_control_allowed(BIGINT,TEXT) FROM PUBLIC;
