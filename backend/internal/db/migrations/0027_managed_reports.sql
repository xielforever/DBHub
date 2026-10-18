-- Formal reports are managed resources; neither credentials nor ownership are
-- borrowed from a developer's personal connection. Existing reports stay personal.
CREATE TABLE sys_report_accounts (
 id BIGSERIAL PRIMARY KEY, name VARCHAR(100) NOT NULL,
 definition_revision_id BIGINT NOT NULL REFERENCES sys_source_definitions(id),
 username VARCHAR(128) NOT NULL, password TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT true, version BIGINT NOT NULL DEFAULT 1,
 credential_generation BIGINT NOT NULL DEFAULT 1,
 created_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE sys_managed_reports (
 id BIGSERIAL PRIMARY KEY, owner_id BIGINT NOT NULL REFERENCES sys_users(id),
 definition_revision_id BIGINT NOT NULL REFERENCES sys_source_definitions(id),
 draft JSONB NOT NULL CHECK(jsonb_typeof(draft)='object'),
 draft_version BIGINT NOT NULL DEFAULT 1, latest_version BIGINT NOT NULL DEFAULT 0,
 publication_no BIGINT NOT NULL DEFAULT 0, active_release_id BIGINT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE sys_report_versions (
 id BIGSERIAL PRIMARY KEY, report_id BIGINT NOT NULL REFERENCES sys_managed_reports(id),
 definition_revision_id BIGINT NOT NULL REFERENCES sys_source_definitions(id),
 number BIGINT NOT NULL, definition JSONB NOT NULL, content_hash CHAR(64) NOT NULL,
 submitted_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 submitter_name TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(report_id,number), UNIQUE(id,report_id)
);
CREATE TABLE sys_report_reviews (
 id BIGSERIAL PRIMARY KEY, version_id BIGINT NOT NULL UNIQUE REFERENCES sys_report_versions(id),
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','withdrawn')),
 decided_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 decider_name TEXT NOT NULL DEFAULT '', comment VARCHAR(1000) NOT NULL DEFAULT '',
 account_id BIGINT REFERENCES sys_report_accounts(id), manifest JSONB,
 manifest_hash CHAR(64), validation_id UUID, decided_at TIMESTAMPTZ,
 CHECK(status<>'approved' OR (account_id IS NOT NULL AND manifest IS NOT NULL AND manifest_hash IS NOT NULL AND validation_id IS NOT NULL))
);
CREATE TABLE sys_report_validations (
 id UUID PRIMARY KEY, review_id BIGINT NOT NULL REFERENCES sys_report_reviews(id),
 account_id BIGINT NOT NULL REFERENCES sys_report_accounts(id),
 account_generation BIGINT NOT NULL, account_version BIGINT NOT NULL,
 manifest JSONB NOT NULL, manifest_hash CHAR(64) NOT NULL,
 performed_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE sys_report_reviews ADD FOREIGN KEY(validation_id) REFERENCES sys_report_validations(id);
CREATE TABLE sys_report_releases (
 id BIGSERIAL PRIMARY KEY, report_id BIGINT NOT NULL REFERENCES sys_managed_reports(id),
 number BIGINT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('publish','rollback','unpublish')),
 review_id BIGINT REFERENCES sys_report_reviews(id),
 performed_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 performer_name TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(report_id,number), UNIQUE(id,report_id), CHECK(kind='unpublish' OR review_id IS NOT NULL)
);
ALTER TABLE sys_managed_reports ADD FOREIGN KEY(active_release_id,id) REFERENCES sys_report_releases(id,report_id);
CREATE TABLE sys_report_commands (
 actor_id BIGINT NOT NULL, request_id UUID NOT NULL, operation TEXT NOT NULL,
 request_hash CHAR(64) NOT NULL, result JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(actor_id,request_id)
);
CREATE INDEX report_version_report ON sys_report_versions(report_id,number DESC);
CREATE INDEX report_review_pending ON sys_report_reviews(id) WHERE status='pending';
CREATE INDEX managed_report_owner ON sys_managed_reports(owner_id,updated_at DESC);

-- Historical actor foreign keys use SET NULL: that is the sole permitted update.
CREATE FUNCTION dbhub_report_history_actor_null() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE actor_column TEXT := TG_ARGV[0];
BEGIN
 IF TG_OP='UPDATE' AND (to_jsonb(NEW)-actor_column)=(to_jsonb(OLD)-actor_column)
 AND to_jsonb(NEW)->actor_column='null'::jsonb THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'report history is append-only';
END $$;
CREATE TRIGGER report_version_immutable BEFORE UPDATE OR DELETE ON sys_report_versions FOR EACH ROW EXECUTE FUNCTION dbhub_report_history_actor_null('submitted_by');
CREATE TRIGGER report_validation_immutable BEFORE UPDATE OR DELETE ON sys_report_validations FOR EACH ROW EXECUTE FUNCTION dbhub_report_history_actor_null('performed_by');
CREATE TRIGGER report_release_immutable BEFORE UPDATE OR DELETE ON sys_report_releases FOR EACH ROW EXECUTE FUNCTION dbhub_report_history_actor_null('performed_by');
CREATE FUNCTION dbhub_report_review_once() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND OLD.status='pending' AND NEW.version_id=OLD.version_id AND NEW.status<>'pending' THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'decided_by')=(to_jsonb(OLD)-'decided_by') AND NEW.decided_by IS NULL THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'report review is already decided';
END $$;
CREATE TRIGGER report_review_once BEFORE UPDATE OR DELETE ON sys_report_reviews FOR EACH ROW EXECUTE FUNCTION dbhub_report_review_once();
CREATE FUNCTION dbhub_report_account_identity() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.definition_revision_id<>OLD.definition_revision_id OR NEW.username<>OLD.username THEN RAISE EXCEPTION 'create a new report account to change identity'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER report_account_identity BEFORE UPDATE ON sys_report_accounts FOR EACH ROW EXECUTE FUNCTION dbhub_report_account_identity();
