-- Each renewal is a new independently reviewed, bounded proposal. Preserve
-- every prior release and the original append-only prohibitions.
ALTER TABLE sys_iam_action_releases DROP CONSTRAINT sys_iam_action_releases_pkey;
ALTER TABLE sys_iam_action_releases ADD PRIMARY KEY(workspace_id,user_id,action_id,revocation_sequence,proposal_id);
ALTER TABLE sys_iam_action_releases ADD CHECK(revocation_sequence>0);
CREATE TRIGGER iam_release_immutable BEFORE UPDATE OR DELETE ON sys_iam_action_releases
 FOR EACH ROW EXECUTE FUNCTION dbhub_iam_immutable();
CREATE TRIGGER iam_release_no_truncate BEFORE TRUNCATE ON sys_iam_action_releases
 FOR EACH STATEMENT EXECUTE FUNCTION dbhub_iam_immutable();

CREATE FUNCTION dbhub_iam_release_approved() RETURNS TRIGGER LANGUAGE plpgsql AS $body$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM sys_iam_proposals p
  JOIN sys_iam_revocations v ON v.sequence=NEW.revocation_sequence
   AND v.workspace_id=NEW.workspace_id AND v.target_id=NEW.user_id
   AND v.kind='action_revoke' AND v.action_id=NEW.action_id
  WHERE p.workspace_id=NEW.workspace_id AND p.proposal_id=NEW.proposal_id
   AND p.state='applied' AND p.reviewer_id<>p.requester_id
   AND NOT(p.reviewer_id=ANY(p.beneficiary_ids))
   AND p.beneficiary_ids=ARRAY[NEW.user_id]
   AND p.patch->>'kind'='action_release'
   AND (p.patch->>'subject_id')::bigint=NEW.user_id
   AND p.patch->>'action_id'=NEW.action_id
   AND (p.patch->>'revocation_sequence')::bigint=NEW.revocation_sequence
   AND (p.patch->>'valid_until')::timestamptz=NEW.valid_until) THEN
  RAISE EXCEPTION 'release requires exact independent approval' USING ERRCODE='42501';
 END IF;
 RETURN NEW;
END $body$;
DO $migration$ BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_release_approved() SET search_path=%I,pg_temp',current_schema());
END $migration$;
REVOKE EXECUTE ON FUNCTION dbhub_iam_release_approved() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER iam_release_approval AFTER INSERT ON sys_iam_action_releases
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION dbhub_iam_release_approved();
