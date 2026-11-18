-- A new workspace subject keeps every owner/audience reference to the frozen
-- membership isolated while the authenticated platform account stays the same.
ALTER TABLE sys_users ADD COLUMN workspace_account_id BIGINT;
ALTER TABLE sys_users ADD CONSTRAINT workspace_subject_non_login
    CHECK (workspace_account_id IS NULL OR
        (workspace_account_id<>id AND password_hash='' AND NOT is_platform_admin));
CREATE INDEX workspace_subject_account ON sys_users(workspace_account_id) WHERE workspace_account_id IS NOT NULL;

-- Keep automatic first membership for real platform accounts. A workspace
-- subject is explicitly linked to the reviewed rejoin before any membership.
DROP TRIGGER user_initial_workspace ON sys_users;
CREATE TRIGGER user_initial_workspace AFTER INSERT ON sys_users
    FOR EACH ROW WHEN (NEW.workspace_account_id IS NULL) EXECUTE FUNCTION dbhub_join_initial_workspace();

CREATE TABLE sys_iam_isolated_custody_rejoins (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id BIGINT NOT NULL,
    retired_user_id BIGINT NOT NULL,
    freeze_sequence BIGINT NOT NULL,
    retired_member_version BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    isolation_id TEXT NOT NULL REFERENCES sys_iam_isolation_guard(isolation_id),
    proposed_by BIGINT NOT NULL,
    proposed_reason VARCHAR(1000) NOT NULL,
    proposed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    approved_by BIGINT,
    approved_at TIMESTAMPTZ,
    new_subject_id BIGINT UNIQUE,
    UNIQUE(workspace_id,retired_user_id,freeze_sequence),
    FOREIGN KEY(workspace_id,retired_user_id,freeze_sequence) REFERENCES sys_iam_custody(workspace_id,user_id,freeze_sequence),
    CHECK (num_nonnulls(approved_by,approved_at,new_subject_id) IN (0,3)),
    CHECK (approved_by IS NULL OR approved_by<>proposed_by)
);
CREATE INDEX isolated_rejoins_account ON sys_iam_isolated_custody_rejoins(account_id,workspace_id)
    WHERE approved_at IS NOT NULL;
ALTER TABLE sys_iam_isolated_custody_rejoins ENABLE ROW LEVEL SECURITY;

DO $migration$
BEGIN
    EXECUTE format($f$CREATE FUNCTION dbhub_workspace_subject_membership() RETURNS TRIGGER
        LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        BEGIN
            IF EXISTS(SELECT 1 FROM sys_users WHERE id=NEW.user_id AND workspace_account_id IS NOT NULL)
                AND NOT EXISTS(SELECT 1 FROM sys_iam_isolated_custody_rejoins
                    WHERE workspace_id=NEW.workspace_id AND new_subject_id=NEW.user_id AND approved_at IS NOT NULL)
            THEN RAISE EXCEPTION 'workspace subject is bound to its approved workspace' USING ERRCODE='23514';
            END IF;
            RETURN NEW;
        END
        $body$$f$,current_schema());
    EXECUTE format($f$CREATE FUNCTION dbhub_retire_workspace_subjects() RETURNS TRIGGER
        LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        BEGIN
            IF OLD.workspace_account_id IS NULL AND (TG_OP='DELETE' OR NOT NEW.is_active) THEN
                UPDATE sys_users SET is_active=false WHERE workspace_account_id=OLD.id AND is_active;
            END IF;
            RETURN NULL;
        END
        $body$$f$,current_schema());
END $migration$;
CREATE TRIGGER workspace_subject_membership BEFORE INSERT OR UPDATE OF workspace_id,user_id ON sys_workspace_members
    FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_subject_membership();
CREATE TRIGGER retire_workspace_subjects AFTER UPDATE OF is_active OR DELETE ON sys_users
    FOR EACH ROW EXECUTE FUNCTION dbhub_retire_workspace_subjects();
