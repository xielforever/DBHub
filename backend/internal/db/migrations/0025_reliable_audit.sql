-- Stable event identity makes replay safe, including a lost DB acknowledgement.
ALTER TABLE sys_audit_logs ADD COLUMN event_id UUID;
CREATE UNIQUE INDEX idx_audit_event_identity ON sys_audit_logs(event_id, created_at);

-- A queued event can outlive the user/connection. Preserve historical IDs and
-- the credential-free context snapshot instead of poisoning the retry queue.
ALTER TABLE sys_audit_logs DROP CONSTRAINT sys_audit_logs_user_id_fkey;
ALTER TABLE sys_audit_logs DROP CONSTRAINT sys_audit_logs_connection_id_fkey;
