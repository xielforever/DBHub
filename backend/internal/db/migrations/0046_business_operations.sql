CREATE TABLE sys_operation_accounts(
 id BIGSERIAL PRIMARY KEY,workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 name VARCHAR(100) NOT NULL,definition_revision_id BIGINT NOT NULL,username VARCHAR(100) NOT NULL,password TEXT NOT NULL,
 installation_id UUID NOT NULL,enabled BOOLEAN NOT NULL DEFAULT true,version BIGINT NOT NULL DEFAULT 1,
 UNIQUE(workspace_id,id),FOREIGN KEY(workspace_id,definition_revision_id) REFERENCES sys_source_definitions(workspace_id,id)
);
CREATE TABLE sys_operation_templates(
 id BIGSERIAL PRIMARY KEY,workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 account_id BIGINT NOT NULL,kind TEXT NOT NULL CHECK(kind IN('member_points_adjust','member_coupon_issue','warehouse_stock_adjust','transport_status_advance')),
 user_ids BIGINT[] NOT NULL DEFAULT '{}',group_ids BIGINT[] NOT NULL DEFAULT '{}',enabled BOOLEAN NOT NULL DEFAULT true,version BIGINT NOT NULL DEFAULT 1,
 UNIQUE(workspace_id,id),UNIQUE(workspace_id,account_id,kind),FOREIGN KEY(workspace_id,account_id) REFERENCES sys_operation_accounts(workspace_id,id)
);
CREATE TABLE sys_operation_previews(
 id UUID PRIMARY KEY,workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace(),user_id BIGINT NOT NULL REFERENCES sys_users(id),
 member_version BIGINT NOT NULL DEFAULT dbhub_member_epoch(),template_id BIGINT NOT NULL,template_version BIGINT NOT NULL,account_version BIGINT NOT NULL,
 encrypted_preview TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '5 minutes',
 UNIQUE(workspace_id,id,user_id),FOREIGN KEY(workspace_id,template_id) REFERENCES sys_operation_templates(workspace_id,id)
);
CREATE TABLE sys_operation_requests(
 id BIGSERIAL PRIMARY KEY,operation_id UUID NOT NULL UNIQUE,workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace(),user_id BIGINT NOT NULL,
 preview_id UUID NOT NULL UNIQUE,template_id BIGINT NOT NULL,kind TEXT NOT NULL,member_version BIGINT NOT NULL,
 template_version BIGINT NOT NULL,account_version BIGINT NOT NULL,encrypted_preview TEXT NOT NULL,reason VARCHAR(1000) NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN('pending','approved','rejected','executing','executed','stale','failed','unknown')),
 version BIGINT NOT NULL DEFAULT 1,reviewer_id BIGINT REFERENCES sys_users(id),reviewer_member_version BIGINT,review_comment VARCHAR(1000) NOT NULL DEFAULT '',
 encrypted_receipt TEXT NOT NULL DEFAULT '',receipt_sha256 TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '30 minutes',execution_started_at TIMESTAMPTZ,finished_at TIMESTAMPTZ,
 UNIQUE(workspace_id,id),FOREIGN KEY(workspace_id,preview_id,user_id) REFERENCES sys_operation_previews(workspace_id,id,user_id),
 FOREIGN KEY(workspace_id,template_id) REFERENCES sys_operation_templates(workspace_id,id)
);
CREATE INDEX operation_request_owner ON sys_operation_requests(workspace_id,user_id,id DESC);
CREATE INDEX operation_request_pending ON sys_operation_requests(workspace_id,id) WHERE status='pending';
CREATE TABLE sys_operation_validations(
 id UUID PRIMARY KEY,workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace(),request_id BIGINT NOT NULL,
 reviewer_id BIGINT NOT NULL,request_version BIGINT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,request_id) REFERENCES sys_operation_requests(workspace_id,id)
);
DO $migration$ DECLARE item TEXT;BEGIN
 FOREACH item IN ARRAY ARRAY['sys_operation_accounts','sys_operation_templates','sys_operation_previews','sys_operation_requests','sys_operation_validations'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',item);
  EXECUTE format('CREATE POLICY workspace_scope ON %I USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed()) WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_scope_allowed())',item);
  EXECUTE format('CREATE TRIGGER workspace_immutable BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_immutable()',item);
 END LOOP;
END $migration$;
CREATE FUNCTION dbhub_operation_notification() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER AS $$
DECLARE uid BIGINT;BEGIN
 IF TG_OP='INSERT' THEN
  FOR uid IN SELECT user_id FROM sys_workspace_members WHERE workspace_id=NEW.workspace_id AND active AND user_id<>NEW.user_id AND dbhub_has_capability(user_id,'operation_review') LOOP
   PERFORM dbhub_notify(NEW.workspace_id,uid,'operation-pending:'||NEW.id,'approval','业务操作待审批','请独立复核变更前后值及业务原因。','business_operation',NEW.id);
  END LOOP;
 ELSIF NEW.status IS DISTINCT FROM OLD.status AND NEW.status IN('approved','rejected','executed') THEN
  PERFORM dbhub_notify(NEW.workspace_id,NEW.user_id,'operation-status:'||NEW.id||':'||NEW.status,'business_operation',
   CASE WHEN NEW.status='executed' THEN '业务操作已执行' ELSE '业务操作审批已更新' END,
   CASE WHEN NEW.status='executed' THEN '目标库回执已核实并保存，请查看执行结果。' ELSE '请查看审批意见；审批通过后由申请人执行。' END,'business_operation',NEW.id);
 END IF;RETURN NEW;
END $$;
DO $migration$ BEGIN EXECUTE format('ALTER FUNCTION dbhub_operation_notification() SET search_path=%I,pg_temp',current_schema());END $migration$;
CREATE TRIGGER operation_notification AFTER INSERT OR UPDATE OF status ON sys_operation_requests FOR EACH ROW EXECUTE FUNCTION dbhub_operation_notification();
-- This narrowly-scoped helper linearizes capability/member revocation against
-- an execution. It exposes no credentials and cannot alter membership.
DO $migration$ DECLARE ns TEXT:=current_schema();BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_operation_lock_member(uid BIGINT,expected BIGINT,reviewer BOOLEAN) RETURNS BOOLEAN
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 DECLARE valid BOOLEAN;BEGIN
  PERFORM 1 FROM sys_workspaces WHERE id=dbhub_workspace() AND enabled FOR SHARE;
  PERFORM 1 FROM sys_users WHERE id=uid AND is_active FOR SHARE;
  SELECT m.active AND m.version=expected AND (NOT reviewer OR m.role_code='admin' OR 'operation_review'=ANY(m.capabilities)) INTO valid
  FROM sys_workspace_members m WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid FOR SHARE;
  RETURN COALESCE(valid,false) AND dbhub_scope_allowed() AND dbhub_member_role(uid) IS NOT NULL;
 END $body$$f$,ns);
END $migration$;
