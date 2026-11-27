-- Reuse the checked inbox plan, never an authorization result. Every call
-- retains invoker RLS and the original per-object/audience predicates. For a
-- target principal, empty branches do not need a domain-operation grant; a
-- non-empty visible branch is checked before it can return any rows.
CREATE FUNCTION dbhub_inbox_todos(principal_uid BIGINT,legacy_reviewer BOOLEAN)
RETURNS TABLE(kind TEXT,id BIGINT,resource_id BIGINT,title TEXT,created_at TIMESTAMPTZ,expires_at TIMESTAMPTZ)
LANGUAGE plpgsql STABLE SECURITY INVOKER AS $body$
DECLARE legacy_allowed BOOLEAN;
BEGIN
 IF principal_uid IS DISTINCT FROM dbhub_actor() OR NOT COALESCE(dbhub_scope_allowed(),false) THEN RETURN;END IF;
 IF dbhub_iam_is_target(principal_uid) THEN
  IF EXISTS(SELECT 1 FROM (SELECT 'business_operation'::text AS kind,r.id,r.id AS resource_id,'业务操作待审批'::text AS title,r.created_at,r.expires_at
 FROM sys_operation_requests r WHERE r.user_id<>principal_uid AND r.status='pending' AND r.expires_at>now()) visible_pending) THEN
   IF dbhub_has_capability(principal_uid,'operation_review') THEN
    RETURN QUERY SELECT 'business_operation'::text AS kind,r.id,r.id AS resource_id,'业务操作待审批'::text AS title,r.created_at,r.expires_at
 FROM sys_operation_requests r WHERE r.user_id<>principal_uid AND r.status='pending' AND r.expires_at>now();
   END IF;
  END IF;
  IF EXISTS(SELECT 1 FROM (SELECT 'report_review'::text AS kind,rv.id,r.id AS resource_id,'报表待审批'::text AS title,v.created_at AS created_at,NULL::timestamptz AS expires_at
 FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id
 JOIN sys_managed_reports r ON r.id=v.report_id AND r.workspace_id=v.workspace_id
 WHERE true AND rv.status='pending') visible_pending) THEN
   IF dbhub_iam_has_action(principal_uid,'report.review') THEN
    RETURN QUERY SELECT 'report_review'::text AS kind,rv.id,r.id AS resource_id,'报表待审批'::text AS title,v.created_at AS created_at,NULL::timestamptz AS expires_at
 FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id
 JOIN sys_managed_reports r ON r.id=v.report_id AND r.workspace_id=v.workspace_id
 WHERE true AND rv.status='pending';
   END IF;
  END IF;
  IF EXISTS(SELECT 1 FROM (SELECT 'dashboard_review',rv.id,rv.dashboard_id,'仪表盘待审阅',v.created_at,rv.expires_at
 FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id
 JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id AND d.workspace_id=rv.workspace_id
 WHERE rv.reviewer_id=principal_uid AND rv.status='pending' AND rv.expires_at>now() AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch) visible_pending) THEN
   IF dbhub_iam_has_action(principal_uid,'dashboard.review') THEN
    RETURN QUERY SELECT 'dashboard_review',rv.id,rv.dashboard_id,'仪表盘待审阅',v.created_at,rv.expires_at
 FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id
 JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id AND d.workspace_id=rv.workspace_id
 WHERE rv.reviewer_id=principal_uid AND rv.status='pending' AND rv.expires_at>now() AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch;
   END IF;
  END IF;
  IF EXISTS(SELECT 1 FROM (SELECT 'access_request',a.id,COALESCE(a.dashboard_id,a.report_id),'报表权限申请',a.created_at,a.expires_at
 FROM sys_access_requests a WHERE true AND (a.status='pending' OR (a.status='processing' AND a.assigned_to=principal_uid)) AND a.expires_at>now()) visible_pending) THEN
   IF (dbhub_iam_has_action(principal_uid,'report.review') OR dbhub_iam_has_action(principal_uid,'dashboard.review')) THEN
    RETURN QUERY SELECT 'access_request',a.id,COALESCE(a.dashboard_id,a.report_id),'报表权限申请',a.created_at,a.expires_at
 FROM sys_access_requests a WHERE true AND (a.status='pending' OR (a.status='processing' AND a.assigned_to=principal_uid)) AND a.expires_at>now();
   END IF;
  END IF;
  IF EXISTS(SELECT 1 FROM (SELECT 'report_export',e.id,e.id,'报表导出待审批',e.created_at,e.expires_at
 FROM sys_report_exports e WHERE true AND e.status='pending' AND e.user_id<>principal_uid AND e.expires_at>now()
 AND EXISTS(SELECT 1 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id
 JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
 JOIN sys_report_accounts a ON a.id=rv.account_id AND a.enabled
 JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.enabled
 WHERE m.id=e.report_id AND m.publication_no=e.publication_no AND
 (rv.manifest->'user_ids' @> jsonb_build_array(principal_uid::bigint) OR EXISTS(
 SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=principal_uid)))) visible_pending) THEN
   IF dbhub_iam_has_action(principal_uid,'report.exportReview') THEN
    RETURN QUERY SELECT 'report_export',e.id,e.id,'报表导出待审批',e.created_at,e.expires_at
 FROM sys_report_exports e WHERE true AND e.status='pending' AND e.user_id<>principal_uid AND e.expires_at>now()
 AND EXISTS(SELECT 1 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id
 JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
 JOIN sys_report_accounts a ON a.id=rv.account_id AND a.enabled
 JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.enabled
 WHERE m.id=e.report_id AND m.publication_no=e.publication_no AND
 (rv.manifest->'user_ids' @> jsonb_build_array(principal_uid::bigint) OR EXISTS(
 SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=principal_uid)));
   END IF;
  END IF;
  IF EXISTS(SELECT 1 FROM (SELECT 'metric_incident',i.id,i.id,'业务指标待跟踪',i.opened_at,NULL::timestamptz
 FROM sys_metric_incidents i WHERE i.user_id=principal_uid AND i.status IN('open','acknowledged')
 AND EXISTS(SELECT 1 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
 WHERE m.id=i.report_id AND m.publication_no=i.publication_no AND
 (rv.manifest->'user_ids' @> jsonb_build_array(principal_uid::bigint) OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=principal_uid)))) visible_pending) THEN
   IF dbhub_iam_has_action(principal_uid,'report.view') THEN
    RETURN QUERY SELECT 'metric_incident',i.id,i.id,'业务指标待跟踪',i.opened_at,NULL::timestamptz
 FROM sys_metric_incidents i WHERE i.user_id=principal_uid AND i.status IN('open','acknowledged')
 AND EXISTS(SELECT 1 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
 WHERE m.id=i.report_id AND m.publication_no=i.publication_no AND
 (rv.manifest->'user_ids' @> jsonb_build_array(principal_uid::bigint) OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=principal_uid)));
   END IF;
  END IF;
 ELSE
  -- A direct invoker cannot turn a client-supplied legacy bool into a grant.
  legacy_allowed:=legacy_reviewer AND dbhub_has_capability(principal_uid,'report_review');
  RETURN QUERY 
 SELECT 'business_operation'::text AS kind,r.id,r.id AS resource_id,'业务操作待审批'::text AS title,r.created_at,r.expires_at
 FROM sys_operation_requests r WHERE dbhub_has_capability(principal_uid,'operation_review') AND r.user_id<>principal_uid AND r.status='pending' AND r.expires_at>now()
 UNION ALL
 SELECT 'report_review'::text AS kind,rv.id,r.id AS resource_id,'报表待审批'::text AS title,v.created_at AS created_at,NULL::timestamptz AS expires_at
 FROM sys_report_reviews rv JOIN sys_report_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id
 JOIN sys_managed_reports r ON r.id=v.report_id AND r.workspace_id=v.workspace_id
 WHERE legacy_allowed AND rv.status='pending'
 UNION ALL
 SELECT 'dashboard_review',rv.id,rv.dashboard_id,'仪表盘待审阅',v.created_at,rv.expires_at
 FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id
 JOIN sys_managed_dashboards d ON d.id=rv.dashboard_id AND d.workspace_id=rv.workspace_id
 WHERE rv.reviewer_id=principal_uid AND rv.status='pending' AND rv.expires_at>now() AND d.lifecycle='active' AND d.lifecycle_epoch=rv.lifecycle_epoch
 UNION ALL
 SELECT 'access_request',a.id,COALESCE(a.dashboard_id,a.report_id),'报表权限申请',a.created_at,a.expires_at
 FROM sys_access_requests a WHERE legacy_allowed AND (a.status='pending' OR (a.status='processing' AND a.assigned_to=principal_uid)) AND a.expires_at>now()
 UNION ALL
 SELECT 'report_export',e.id,e.id,'报表导出待审批',e.created_at,e.expires_at
 FROM sys_report_exports e WHERE legacy_allowed AND e.status='pending' AND e.user_id<>principal_uid AND e.expires_at>now()
 AND EXISTS(SELECT 1 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id
 JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
 JOIN sys_report_accounts a ON a.id=rv.account_id AND a.enabled
 JOIN sys_source_definitions d ON d.id=a.definition_revision_id AND d.enabled
 WHERE m.id=e.report_id AND m.publication_no=e.publication_no AND
 (rv.manifest->'user_ids' @> jsonb_build_array(principal_uid::bigint) OR EXISTS(
 SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=principal_uid)))
 UNION ALL
 SELECT 'metric_incident',i.id,i.id,'业务指标待跟踪',i.opened_at,NULL::timestamptz
 FROM sys_metric_incidents i WHERE i.user_id=principal_uid AND i.status IN('open','acknowledged')
 AND EXISTS(SELECT 1 FROM sys_managed_reports m JOIN sys_report_releases re ON re.id=m.active_release_id JOIN sys_report_reviews rv ON rv.id=re.review_id AND rv.status='approved'
 WHERE m.id=i.report_id AND m.publication_no=i.publication_no AND
 (rv.manifest->'user_ids' @> jsonb_build_array(principal_uid::bigint) OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(rv.manifest->'group_ids') g JOIN sys_user_group_members gm ON gm.group_id=g.value::bigint WHERE gm.user_id=principal_uid)));
 END IF;
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_inbox_todos(BIGINT,BOOLEAN) SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_inbox_todos(BIGINT,BOOLEAN) FROM PUBLIC;
