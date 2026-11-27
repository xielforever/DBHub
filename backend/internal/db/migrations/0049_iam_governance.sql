-- Governance commands, receipts and their audit evidence commit atomically.
CREATE TABLE sys_iam_commands(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),actor_id BIGINT NOT NULL,request_id UUID NOT NULL,
 operation TEXT NOT NULL,request_hash TEXT NOT NULL,result JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,actor_id,request_id)
);
CREATE TABLE sys_iam_events(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),sequence BIGSERIAL PRIMARY KEY,
 actor_id BIGINT NOT NULL,request_id UUID NOT NULL,operation TEXT NOT NULL,payload JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER iam_events_immutable BEFORE UPDATE OR DELETE ON sys_iam_events FOR EACH ROW EXECUTE FUNCTION dbhub_iam_immutable();
CREATE TRIGGER iam_events_no_truncate BEFORE TRUNCATE ON sys_iam_events FOR EACH STATEMENT EXECUTE FUNCTION dbhub_iam_immutable();
CREATE TRIGGER iam_commands_immutable BEFORE UPDATE OR DELETE ON sys_iam_commands FOR EACH ROW EXECUTE FUNCTION dbhub_iam_immutable();
ALTER TABLE sys_iam_delegations ADD COLUMN parent_id UUID;
ALTER TABLE sys_iam_delegations ADD COLUMN parent_version BIGINT;
ALTER TABLE sys_iam_proposals ADD COLUMN version BIGINT NOT NULL DEFAULT 1;

-- Only a deployment owner may preregister recovery verifiers. The normal
-- governance API cannot add itself to this roster or manufacture verification.
CREATE TABLE sys_iam_recovery_roster(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),user_id BIGINT NOT NULL REFERENCES sys_users(id),
 version BIGINT NOT NULL DEFAULT 1,active BOOLEAN NOT NULL DEFAULT true,PRIMARY KEY(workspace_id,user_id)
);
CREATE TABLE sys_iam_recovery_requests(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),request_id UUID NOT NULL,
 requester_id BIGINT NOT NULL,target_id BIGINT NOT NULL,reason TEXT NOT NULL,
 baseline_epoch BIGINT NOT NULL,roster_version TEXT NOT NULL,expires_at TIMESTAMPTZ NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN('pending','applied','expired','conflict')),
 PRIMARY KEY(workspace_id,request_id)
);
CREATE TABLE sys_iam_recovery_verifications(
 workspace_id BIGINT NOT NULL,request_id UUID NOT NULL,verifier_id BIGINT NOT NULL,
 session_id TEXT NOT NULL,roster_version BIGINT NOT NULL,verified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 auth_version BIGINT NOT NULL,
 PRIMARY KEY(workspace_id,request_id,verifier_id),
 FOREIGN KEY(workspace_id,request_id) REFERENCES sys_iam_recovery_requests(workspace_id,request_id)
);
CREATE TABLE sys_iam_recovery_grants(
 workspace_id BIGINT NOT NULL,user_id BIGINT NOT NULL,request_id UUID NOT NULL,
 valid_until TIMESTAMPTZ NOT NULL,issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,user_id,request_id),
 FOREIGN KEY(workspace_id,request_id) REFERENCES sys_iam_recovery_requests(workspace_id,request_id)
);
CREATE TABLE sys_iam_recovery_attempts(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),user_id BIGINT NOT NULL,
 window_at TIMESTAMPTZ NOT NULL DEFAULT now(),attempts INT NOT NULL DEFAULT 0,
 PRIMARY KEY(workspace_id,user_id)
);
DO $migration$ DECLARE t TEXT;
BEGIN
 FOREACH t IN ARRAY ARRAY['sys_iam_commands','sys_iam_events','sys_iam_recovery_roster','sys_iam_recovery_requests','sys_iam_recovery_verifications','sys_iam_recovery_grants','sys_iam_recovery_attempts'] LOOP
 EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
 EXECUTE format('CREATE POLICY iam_read ON %I FOR SELECT USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed())',t);
 EXECUTE format('CREATE TRIGGER workspace_immutable BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_immutable()',t);
 END LOOP;
END $migration$;
