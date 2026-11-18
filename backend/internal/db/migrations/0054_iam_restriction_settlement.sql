-- The access cutoff commits first. Confirmation of an execution fence is a
-- separate immutable fact; it never rewrites the original command receipt.
-- A revocation increments the member generation without changing its legacy
-- grant. Waiting for TARGET leases here would prevent the cutoff from ever
-- committing while a long operation is admitted. Actual legacy grant changes
-- retain the barrier; generation-only invalidation uses the separate settlement.
DO $migration$ BEGIN
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_iam_legacy_mutation_barrier() RETURNS TRIGGER
 LANGUAGE plpgsql SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 DECLARE wid BIGINT;
 BEGIN
  IF TG_TABLE_NAME='sys_users' THEN
   IF (NEW.is_active,NEW.auth_version) IS NOT DISTINCT FROM (OLD.is_active,OLD.auth_version) THEN RETURN NEW;END IF;
   FOR wid IN SELECT workspace_id FROM sys_workspace_members WHERE user_id=NEW.id ORDER BY workspace_id LOOP
    PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:target:'||wid,0));
   END LOOP;
  ELSE
   IF TG_TABLE_NAME='sys_workspace_members' AND TG_OP='UPDATE' THEN
    IF (NEW.workspace_id,NEW.user_id,NEW.role_code,NEW.active,NEW.capabilities,NEW.expires_at)
       IS NOT DISTINCT FROM (OLD.workspace_id,OLD.user_id,OLD.role_code,OLD.active,OLD.capabilities,OLD.expires_at) THEN RETURN NEW;END IF;
   END IF;
   wid:=CASE WHEN TG_OP='DELETE' THEN OLD.workspace_id ELSE NEW.workspace_id END;
   PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':iam:target:'||wid,0));
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;
 END $body$$f$,current_schema());
END $migration$;
CREATE TABLE sys_iam_restriction_settlements(
 workspace_id BIGINT NOT NULL,actor_id BIGINT NOT NULL,request_id UUID NOT NULL,
 bridge_version TEXT NOT NULL,route_fingerprint TEXT NOT NULL,settled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,actor_id,request_id),
 FOREIGN KEY(workspace_id,actor_id,request_id) REFERENCES sys_iam_commands(workspace_id,actor_id,request_id) DEFERRABLE INITIALLY DEFERRED
);
ALTER TABLE sys_iam_restriction_settlements ENABLE ROW LEVEL SECURITY;
CREATE POLICY iam_read ON sys_iam_restriction_settlements FOR SELECT USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed());
CREATE TRIGGER iam_settlement_immutable BEFORE UPDATE OR DELETE ON sys_iam_restriction_settlements FOR EACH ROW EXECUTE FUNCTION dbhub_iam_immutable();
CREATE TRIGGER iam_settlement_no_truncate BEFORE TRUNCATE ON sys_iam_restriction_settlements FOR EACH STATEMENT EXECUTE FUNCTION dbhub_iam_immutable();
