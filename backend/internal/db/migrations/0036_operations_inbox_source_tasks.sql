CREATE TABLE sys_notifications (
 id BIGSERIAL PRIMARY KEY,
 workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 recipient_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
 event_key VARCHAR(200) NOT NULL, kind VARCHAR(40) NOT NULL,
 title VARCHAR(160) NOT NULL, message VARCHAR(500) NOT NULL DEFAULT '',
 resource_type VARCHAR(30) NOT NULL, resource_id BIGINT NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), read_at TIMESTAMPTZ,
 UNIQUE(workspace_id,recipient_id,event_key)
);
CREATE INDEX notification_inbox ON sys_notifications(workspace_id,recipient_id,id DESC);
CREATE INDEX notification_unread ON sys_notifications(workspace_id,recipient_id,id DESC) WHERE read_at IS NULL;
ALTER TABLE sys_notifications ENABLE ROW LEVEL SECURITY;
CREATE POLICY notification_read ON sys_notifications FOR SELECT USING(workspace_id=dbhub_workspace() AND recipient_id=dbhub_actor() AND dbhub_scope_allowed());
CREATE POLICY notification_insert ON sys_notifications FOR INSERT WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND dbhub_member_role(recipient_id) IS NOT NULL);
CREATE POLICY notification_update ON sys_notifications FOR UPDATE USING(workspace_id=dbhub_workspace() AND recipient_id=dbhub_actor() AND dbhub_scope_allowed()) WITH CHECK(workspace_id=dbhub_workspace() AND recipient_id=dbhub_actor() AND dbhub_scope_allowed());

-- Invoker rights: events cannot leave the business transaction's workspace.
CREATE FUNCTION dbhub_notify(wid BIGINT, uid BIGINT, event TEXT, category TEXT, heading TEXT, body TEXT, resource TEXT, rid BIGINT) RETURNS VOID LANGUAGE SQL AS $$
 INSERT INTO sys_notifications(workspace_id,recipient_id,event_key,kind,title,message,resource_type,resource_id)
 SELECT wid,uid,event,category,heading,body,resource,rid
 WHERE uid IS NOT NULL AND EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id JOIN sys_workspaces w ON w.id=m.workspace_id WHERE m.workspace_id=wid AND m.user_id=uid AND m.active AND u.is_active AND w.enabled)
 ON CONFLICT(workspace_id,recipient_id,event_key) DO NOTHING
$$;
CREATE FUNCTION dbhub_business_notification() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE uid BIGINT; rid BIGINT; owner BIGINT; event TEXT; heading TEXT; body TEXT; resource TEXT; audience JSONB; vwid BIGINT:=NEW.workspace_id;
BEGIN
 IF TG_TABLE_NAME='sys_report_reviews' THEN
  SELECT v.report_id,r.owner_id INTO rid,owner FROM sys_report_versions v JOIN sys_managed_reports r ON r.id=v.report_id AND r.workspace_id=v.workspace_id WHERE v.workspace_id=vwid AND v.id=NEW.version_id;
  resource:='report'; event:='report-review:'||NEW.id||':'||NEW.status;
  IF TG_OP='INSERT' THEN
   FOR uid IN SELECT user_id FROM sys_workspace_members WHERE workspace_id=vwid AND active AND role_code='admin' LOOP
    PERFORM dbhub_notify(vwid,uid,event,'approval','有报表待审批','请审阅冻结版本后处理。',resource,rid);
   END LOOP;
  ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
   PERFORM dbhub_notify(vwid,owner,event,'decision','报表审批状态已更新',CASE NEW.status WHEN 'approved' THEN '审批已通过，正式发布后生效。' WHEN 'rejected' THEN '审批未通过，请查看审阅意见。' ELSE '本次审批已结束。' END,resource,rid);
  END IF;
 ELSIF TG_TABLE_NAME='sys_dashboard_reviews' THEN
  rid:=NEW.dashboard_id; resource:='dashboard';
  event:='dashboard-review:'||NEW.id||':'||NEW.status||':'||NEW.reviewer_id||':'||NEW.assignment_version;
  IF TG_OP='INSERT' OR (NEW.status='pending' AND NEW.reviewer_id IS DISTINCT FROM OLD.reviewer_id) THEN
   PERFORM dbhub_notify(vwid,NEW.reviewer_id,event,'approval','有仪表盘待审阅','请审阅冻结版本及依赖报表。',resource,rid);
  ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
   SELECT owner_id INTO owner FROM sys_managed_dashboards WHERE workspace_id=vwid AND id=rid;
   PERFORM dbhub_notify(vwid,owner,event,'decision','仪表盘审批状态已更新',CASE NEW.status WHEN 'approved' THEN '审阅已通过，正式发布后生效。' WHEN 'rejected' THEN '审阅未通过，请查看意见。' ELSE '本次审阅已结束。' END,resource,rid);
  END IF;
 ELSIF TG_TABLE_NAME='sys_access_requests' THEN
  resource:=CASE WHEN NEW.dashboard_id IS NULL THEN 'report_access' ELSE 'dashboard_access' END;
  rid:=COALESCE(NEW.dashboard_id,NEW.report_id); event:='access:'||NEW.id||':'||NEW.version||':'||NEW.status;
  IF TG_OP='INSERT' THEN
   FOR uid IN SELECT user_id FROM sys_workspace_members WHERE workspace_id=vwid AND active AND role_code='admin' LOOP
    PERFORM dbhub_notify(vwid,uid,event,'access','有报表权限申请待处理','请检查申请与报表受众。',resource,rid);
   END LOOP;
  ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
   PERFORM dbhub_notify(vwid,NEW.requester_id,event,'access','权限申请状态已更新',CASE NEW.status WHEN 'fulfilled' THEN '授权已随正式版本生效。' WHEN 'approved_pending_release' THEN '申请已批准，等待正式发布。' WHEN 'rejected' THEN '申请未通过，请查看处理结果。' ELSE '请查看申请的最新状态。' END,resource,rid);
  END IF;
 ELSIF TG_TABLE_NAME IN ('sys_report_releases','sys_dashboard_releases') THEN
  IF TG_TABLE_NAME='sys_report_releases' THEN
   rid:=NEW.report_id; resource:='report'; SELECT owner_id INTO owner FROM sys_managed_reports WHERE workspace_id=vwid AND id=rid;
   SELECT rv.manifest INTO audience FROM sys_report_reviews rv WHERE rv.workspace_id=vwid AND rv.id=COALESCE(NEW.review_id,(SELECT re.review_id FROM sys_managed_reports r JOIN sys_report_releases re ON re.id=r.active_release_id AND re.workspace_id=r.workspace_id WHERE r.workspace_id=vwid AND r.id=rid));
  ELSE
   rid:=NEW.dashboard_id; resource:='dashboard'; SELECT owner_id INTO owner FROM sys_managed_dashboards WHERE workspace_id=vwid AND id=rid;
   SELECT v.definition INTO audience FROM sys_dashboard_reviews rv JOIN sys_dashboard_versions v ON v.id=rv.version_id AND v.workspace_id=rv.workspace_id WHERE rv.workspace_id=vwid AND rv.id=COALESCE(NEW.review_id,(SELECT re.review_id FROM sys_managed_dashboards d JOIN sys_dashboard_releases re ON re.id=d.active_release_id AND re.workspace_id=d.workspace_id WHERE d.workspace_id=vwid AND d.id=rid));
  END IF;
  body:=CASE NEW.kind WHEN 'unpublish' THEN '已下线，正式查询入口已关闭。' WHEN 'archive' THEN '已归档。' WHEN 'rollback' THEN '已回滚至获批版本。' ELSE '获批版本已正式发布。' END;
  PERFORM dbhub_notify(vwid,owner,TG_TABLE_NAME||':'||NEW.id,'release','正式发布状态已更新',body,resource,rid);
  FOR uid IN SELECT m.user_id FROM sys_workspace_members m WHERE m.workspace_id=vwid AND m.active AND m.user_id<>owner AND
   (EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(audience->'user_ids','null'::jsonb),'[]'::jsonb)) a WHERE a.value::bigint=m.user_id)
    OR EXISTS(SELECT 1 FROM sys_user_group_members gm WHERE gm.workspace_id=vwid AND gm.user_id=m.user_id AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(audience->'group_ids','null'::jsonb),'[]'::jsonb)) a WHERE a.value::bigint=gm.group_id)))
  LOOP
   PERFORM dbhub_notify(vwid,uid,TG_TABLE_NAME||':'||NEW.id,'release','正式发布状态已更新',body,'published_'||resource,rid);
  END LOOP;
 END IF;
 RETURN NEW;
END $$;
-- Only the trigger has definer rights: cross-recipient inserts must not require
-- broadening notification SELECT policies. Its fixed search path and NEW's
-- workspace keep writes bound to the original, already authorized domain row.
ALTER FUNCTION dbhub_business_notification() SECURITY DEFINER;
DO $migration$
BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_business_notification() SET search_path=%I,pg_temp',current_schema());
END $migration$;
CREATE TRIGGER report_notification AFTER INSERT OR UPDATE ON sys_report_reviews FOR EACH ROW EXECUTE FUNCTION dbhub_business_notification();
CREATE TRIGGER dashboard_notification AFTER INSERT OR UPDATE ON sys_dashboard_reviews FOR EACH ROW EXECUTE FUNCTION dbhub_business_notification();
CREATE TRIGGER access_notification AFTER INSERT OR UPDATE ON sys_access_requests FOR EACH ROW EXECUTE FUNCTION dbhub_business_notification();
CREATE TRIGGER report_release_notification AFTER INSERT ON sys_report_releases FOR EACH ROW EXECUTE FUNCTION dbhub_business_notification();
CREATE TRIGGER dashboard_release_notification AFTER INSERT ON sys_dashboard_releases FOR EACH ROW EXECUTE FUNCTION dbhub_business_notification();

CREATE TABLE sys_source_schedules (
 id BIGSERIAL PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 actor_id BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 connection_id BIGINT, kind TEXT NOT NULL CHECK(kind IN ('check','collect')),
 interval_minutes INTEGER NOT NULL CHECK(interval_minutes BETWEEN 5 AND 10080),
 enabled BOOLEAN NOT NULL DEFAULT true, member_version BIGINT NOT NULL, config_updated_at TIMESTAMPTZ NOT NULL,
 version BIGINT NOT NULL DEFAULT 1, next_run_at TIMESTAMPTZ NOT NULL, last_run_at TIMESTAMPTZ,
 last_status TEXT NOT NULL DEFAULT '', last_outcome TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id), UNIQUE(workspace_id,actor_id,connection_id,kind),
 CHECK(kind<>'collect' OR interval_minutes>=60),
 FOREIGN KEY(workspace_id,connection_id) REFERENCES sys_connections(workspace_id,id) ON DELETE SET NULL(connection_id)
);
CREATE TABLE sys_source_tasks (
 id UUID PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 actor_id BIGINT REFERENCES sys_users(id) ON DELETE SET NULL, connection_id BIGINT,
 connection_name VARCHAR(100) NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('check','collect')),
 status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','cancel_requested','succeeded','failed','cancelled','interrupted','stale','skipped','unknown')),
 progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100), phase VARCHAR(200) NOT NULL DEFAULT '等待执行',
 request_id UUID NOT NULL, request_hash CHAR(64) NOT NULL,
 parent_id UUID REFERENCES sys_source_tasks(id), schedule_id BIGINT,
 member_version BIGINT NOT NULL, config_updated_at TIMESTAMPTZ NOT NULL,
 worker_token UUID, lease_until TIMESTAMPTZ, result JSONB NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
 UNIQUE(workspace_id,id), UNIQUE(workspace_id,actor_id,request_id),
 FOREIGN KEY(workspace_id,connection_id) REFERENCES sys_connections(workspace_id,id) ON DELETE SET NULL(connection_id),
 FOREIGN KEY(workspace_id,schedule_id) REFERENCES sys_source_schedules(workspace_id,id) ON DELETE SET NULL(schedule_id),
 FOREIGN KEY(workspace_id,parent_id) REFERENCES sys_source_tasks(workspace_id,id)
);
CREATE UNIQUE INDEX source_task_active ON sys_source_tasks(workspace_id,connection_id,kind) WHERE status IN ('queued','running','cancel_requested');
CREATE INDEX source_task_queue ON sys_source_tasks(created_at) WHERE status='queued';
CREATE INDEX source_task_inbox ON sys_source_tasks(workspace_id,actor_id,created_at DESC);
CREATE INDEX source_schedule_due ON sys_source_schedules(next_run_at) WHERE enabled;
CREATE TABLE sys_source_check_history (
 id BIGSERIAL PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 actor_id BIGINT REFERENCES sys_users(id) ON DELETE SET NULL, connection_id BIGINT,
 task_id UUID UNIQUE, config_updated_at TIMESTAMPTZ NOT NULL,
 result JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,connection_id) REFERENCES sys_connections(workspace_id,id) ON DELETE SET NULL(connection_id),
 FOREIGN KEY(workspace_id,task_id) REFERENCES sys_source_tasks(workspace_id,id)
);
CREATE INDEX source_check_history_recent ON sys_source_check_history(workspace_id,actor_id,created_at DESC);
DO $migration$
DECLARE ns TEXT:=current_schema();
BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_source_queue_capacity() RETURNS BOOLEAN LANGUAGE SQL VOLATILE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_scope_allowed() AND (SELECT count(*) FROM sys_source_tasks WHERE status IN ('queued','running','cancel_requested'))<200
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_source_workspace_capacity() RETURNS BOOLEAN LANGUAGE SQL VOLATILE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_scope_allowed() AND (SELECT count(*) FROM sys_source_tasks WHERE workspace_id=dbhub_workspace() AND status IN ('queued','running','cancel_requested'))<100
 $body$$f$,ns);
END $migration$;
DO $migration$
DECLARE t TEXT;
BEGIN
 FOREACH t IN ARRAY ARRAY['sys_source_tasks','sys_source_schedules','sys_source_check_history'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY actor_scope ON %I USING(workspace_id=dbhub_workspace() AND actor_id=dbhub_actor() AND dbhub_scope_allowed()) WITH CHECK(workspace_id=dbhub_workspace() AND actor_id=dbhub_actor() AND dbhub_scope_allowed())',t);
  EXECUTE format('CREATE TRIGGER workspace_immutable BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_immutable()',t);
 END LOOP;
END $migration$;
CREATE FUNCTION dbhub_source_task_notification() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE previous TEXT; heading TEXT;
BEGIN
 IF OLD.status IS DISTINCT FROM NEW.status AND NEW.status NOT IN ('queued','running','cancel_requested') THEN
  heading:=CASE NEW.kind WHEN 'check' THEN '数据源检查已结束' ELSE '结构采集已结束' END;
  IF NEW.schedule_id IS NOT NULL THEN
   SELECT last_outcome INTO previous FROM sys_source_schedules WHERE workspace_id=NEW.workspace_id AND id=NEW.schedule_id FOR UPDATE;
   UPDATE sys_source_schedules SET last_outcome=NEW.status,last_status=NEW.phase,updated_at=now() WHERE workspace_id=NEW.workspace_id AND id=NEW.schedule_id;
   IF previous=NEW.status OR (COALESCE(previous,'')='' AND NEW.status='succeeded') THEN RETURN NEW; END IF;
   IF NEW.status='succeeded' THEN heading:='数据源定时任务已恢复'; END IF;
  END IF;
  PERFORM dbhub_notify(NEW.workspace_id,NEW.actor_id,'source-task:'||NEW.id||':'||NEW.status,'task',heading,NEW.phase,'source_tasks',0);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER source_task_notification AFTER UPDATE ON sys_source_tasks FOR EACH ROW EXECUTE FUNCTION dbhub_source_task_notification();

-- Runtime has no policies for platform maintenance tables. Only tightly scoped
-- repository maintenance methods and the opt-in backup process access them.
CREATE TABLE sys_operations (
 name VARCHAR(40) PRIMARY KEY, status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed')),
 started_at TIMESTAMPTZ NOT NULL DEFAULT now(), finished_at TIMESTAMPTZ, last_success_at TIMESTAMPTZ,
 details JSONB NOT NULL DEFAULT '{}', updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE sys_operations ENABLE ROW LEVEL SECURITY;
CREATE TABLE sys_operation_alerts (
 code VARCHAR(60) PRIMARY KEY, active BOOLEAN NOT NULL DEFAULT false, epoch BIGINT NOT NULL DEFAULT 0,
 observed_at TIMESTAMPTZ NOT NULL DEFAULT now(), changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE sys_operation_alerts ENABLE ROW LEVEL SECURITY;
