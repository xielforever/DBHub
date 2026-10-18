ALTER TABLE sys_access_requests ADD COLUMN target_user_id BIGINT;
ALTER TABLE sys_access_requests ADD COLUMN target_group_id BIGINT;
ALTER TABLE sys_access_requests ADD COLUMN decision_reason VARCHAR(1000) NOT NULL DEFAULT '';
UPDATE sys_access_requests SET target_user_id=requester_id;
ALTER TABLE sys_access_requests ALTER COLUMN target_user_id SET DEFAULT dbhub_actor();
ALTER TABLE sys_access_requests ADD CHECK(num_nonnulls(target_user_id,target_group_id)=1);
ALTER TABLE sys_access_requests ADD COLUMN target_key TEXT GENERATED ALWAYS AS
 (CASE WHEN target_user_id IS NOT NULL THEN 'user:'||target_user_id::text ELSE 'group:'||target_group_id::text END) STORED;
DROP INDEX access_request_pending;
CREATE UNIQUE INDEX access_request_pending ON sys_access_requests(workspace_id,requester_id,report_id,target_key) WHERE status IN ('pending','processing','approved_pending_release');
-- Target IDs are historical snapshots: deletion of a group must not erase the
-- request. New target references are checked against the same workspace.
CREATE FUNCTION dbhub_access_target() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.target_user_id IS NOT DISTINCT FROM OLD.target_user_id AND NEW.target_group_id IS NOT DISTINCT FROM OLD.target_group_id THEN RETURN NEW; END IF;
 IF NEW.target_user_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id WHERE m.workspace_id=NEW.workspace_id AND m.user_id=NEW.target_user_id AND m.active AND u.is_active) THEN RAISE EXCEPTION 'invalid workspace access target' USING ERRCODE='23503'; END IF;
 IF NEW.target_group_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM sys_user_groups WHERE workspace_id=NEW.workspace_id AND id=NEW.target_group_id) THEN RAISE EXCEPTION 'invalid workspace access target' USING ERRCODE='23503'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER access_target BEFORE INSERT OR UPDATE ON sys_access_requests FOR EACH ROW EXECUTE FUNCTION dbhub_access_target();
