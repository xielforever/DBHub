-- This migration installs the workflow checks without enabling a workspace.
ALTER TABLE sys_dashboard_reviews ADD COLUMN business_review_authority TEXT;

DO $migration$
DECLARE ns TEXT := current_schema();
BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_reviewer_eligible(uid BIGINT) RETURNS BOOLEAN
 LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 BEGIN
 RETURN CASE WHEN dbhub_iam_dashboard_matrix_enabled() THEN
    dbhub_iam_has_action(uid,'dashboard.review') AND dbhub_iam_has_action(uid,'dashboard.publish')
   ELSE dbhub_iam_can_review(uid,'dashboard') END;
 END;
 $body$$f$,ns);

 EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_review_authority(uid BIGINT) RETURNS TEXT
 LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 BEGIN
 RETURN (SELECT md5(jsonb_build_array(uid,i.membership_version,p.platform_account_version,i.target,
    (SELECT COALESCE(jsonb_agg(s ORDER BY s->>'id'),'[]'::jsonb)
     FROM jsonb_array_elements(b.sources) AS s
     WHERE (s->'actions') ?| ARRAY['dashboard.review','dashboard.publish']))::text)
 FROM dbhub_iam_identity(uid) i CROSS JOIN LATERAL dbhub_iam_binding(uid) b
 JOIN sys_users u ON u.id=uid JOIN sys_users p ON p.id=COALESCE(u.workspace_account_id,u.id)
 WHERE i.qualified AND dbhub_iam_dashboard_matrix_enabled() AND dbhub_dashboard_reviewer_eligible(uid));
 END;
 $body$$f$,ns);

 EXECUTE format($f$CREATE FUNCTION dbhub_dashboard_workflow_admin() RETURNS BOOLEAN
 LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 BEGIN
 RETURN dbhub_iam_qualified(dbhub_actor()) AND EXISTS (
   SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
   JOIN sys_users p ON p.id=COALESCE(u.workspace_account_id,u.id)
   WHERE m.workspace_id=dbhub_workspace() AND m.user_id=dbhub_actor() AND p.is_active
    AND (m.role_code='admin' OR m.matrix_admin OR p.is_platform_admin));
 END;
 $body$$f$,ns);
END $migration$;
