CREATE TABLE sys_report_exports (
 id BIGSERIAL PRIMARY KEY,
 workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 user_id BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 requester_name TEXT NOT NULL,
 report_id BIGINT NOT NULL,
 version_id BIGINT NOT NULL,
 publication_no BIGINT NOT NULL,
 report_name TEXT NOT NULL,
 request_id UUID NOT NULL,
 format TEXT NOT NULL CHECK(format IN('csv','json','xlsx')),
 reason VARCHAR(500) NOT NULL,
 row_count INTEGER NOT NULL CHECK(row_count BETWEEN 0 AND 100000),
 snapshot_hash CHAR(64) NOT NULL,
 encrypted_payload TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN('pending','approved','rejected')),
 reviewed_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 reviewer_name TEXT NOT NULL DEFAULT '',
 review_comment VARCHAR(500) NOT NULL DEFAULT '',
 version BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL,
 reviewed_at TIMESTAMPTZ,
 UNIQUE(workspace_id,id), UNIQUE(workspace_id,user_id,request_id),
 FOREIGN KEY(workspace_id,report_id) REFERENCES sys_managed_reports(workspace_id,id),
 FOREIGN KEY(workspace_id,version_id) REFERENCES sys_report_versions(workspace_id,id),
 CHECK(expires_at>created_at),
 CHECK(status='pending' OR reviewer_name<>''),
 CHECK(reviewed_by IS NULL OR reviewed_by<>user_id)
);
CREATE INDEX report_exports_inbox ON sys_report_exports(workspace_id,user_id,id DESC);
CREATE INDEX report_exports_review ON sys_report_exports(workspace_id,status,id DESC);
ALTER TABLE sys_report_exports ENABLE ROW LEVEL SECURITY;
CREATE TRIGGER workspace_user_reference BEFORE INSERT OR UPDATE ON sys_report_exports
 FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_user_reference('user_id','reviewed_by');
CREATE TRIGGER workspace_immutable BEFORE UPDATE ON sys_report_exports
 FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_immutable();
CREATE POLICY report_export_workspace ON sys_report_exports
 USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed())
 WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_scope_allowed());

-- Only metadata enters notifications. Result rows and parameters remain encrypted.
CREATE FUNCTION dbhub_report_export_notice() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE member RECORD;
BEGIN
 IF TG_OP='INSERT' THEN
  FOR member IN SELECT user_id FROM sys_workspace_members
   WHERE workspace_id=NEW.workspace_id AND active AND role_code='admin' AND user_id<>NEW.user_id
  LOOP
   PERFORM dbhub_notify(NEW.workspace_id,member.user_id,'report-export:'||NEW.id,'todo','有报表导出待审批','请核对报表版本、数据范围及导出用途。','report_export',NEW.id);
  END LOOP;
 ELSIF NEW.status<>OLD.status THEN
  PERFORM dbhub_notify(NEW.workspace_id,NEW.user_id,'report-export-decision:'||NEW.id,'result','报表导出审批已处理','请在报表导出中查看审批结果。','report_export',NEW.id);
 END IF;
 RETURN NEW;
END $$;
-- The trigger inserts cross-recipient notices atomically with the authorized
-- export row. Limit definer rights to this trigger and pin its schema.
ALTER FUNCTION dbhub_report_export_notice() SECURITY DEFINER;
DO $migration$
BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_report_export_notice() SET search_path=%I,pg_temp',current_schema());
END $migration$;
CREATE TRIGGER report_export_notice AFTER INSERT OR UPDATE OF status ON sys_report_exports
 FOR EACH ROW EXECUTE FUNCTION dbhub_report_export_notice();
