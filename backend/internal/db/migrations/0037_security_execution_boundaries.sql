-- Never turn a configured network route into a direct connection on deletion.
-- Replace both original and workspace-composite SET NULL foreign keys, so a
-- concurrent connection bind cannot race an application-level dependency check.
DO $migration$
DECLARE fk RECORD;
BEGIN
 FOR fk IN SELECT conname,pg_get_constraintdef(oid) definition FROM pg_constraint
 WHERE contype='f' AND conrelid='sys_connections'::regclass
 AND confrelid IN ('sys_ssh_tunnels'::regclass,'sys_proxies'::regclass)
 AND confdeltype='n'
 LOOP
  EXECUTE format('ALTER TABLE sys_connections DROP CONSTRAINT %I',fk.conname);
  EXECUTE format('ALTER TABLE sys_connections ADD CONSTRAINT %I %s',fk.conname,
    regexp_replace(fk.definition,'ON DELETE SET NULL( \([^)]*\))?','ON DELETE RESTRICT'));
 END LOOP;
END $migration$;

-- Approval binds an immutable source revision, rather than a mutable personal
-- connection ID. Old pending approvals have no trustworthy original snapshot;
-- preserve their records and require resubmission instead of inventing one.
ALTER TABLE sys_tickets ADD COLUMN definition_revision_id BIGINT;
ALTER TABLE sys_tickets ADD CONSTRAINT ticket_definition_workspace
 FOREIGN KEY(workspace_id,definition_revision_id)
 REFERENCES sys_source_definitions(workspace_id,id);
UPDATE sys_tickets SET status='rejected',
 execution_error='安全升级已启用目标修订冻结，请核对目标后重新提交工单'
 WHERE status IN ('pending','approved');
ALTER TABLE sys_tickets ADD CHECK(status NOT IN ('pending','approved') OR definition_revision_id IS NOT NULL);

-- An administrator's own pending export must be reviewed by someone else.
-- Previously self-approved snapshots become pending again; downloads must not
-- keep relying on a decision that did not provide independent authorization.
UPDATE sys_export_requests SET status='pending',reviewed_by=NULL
 WHERE status='approved' AND reviewed_by=user_id;
