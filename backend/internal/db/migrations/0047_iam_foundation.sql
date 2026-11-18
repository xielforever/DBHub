-- D1.0 / B1 foundation only. No principal is switched to the new role engine.
-- Existing workflow signatures, publication IDs, file expiry and data roles
-- are not rewritten. Runtime callers may read these tables, never grant rights.
ALTER TABLE sys_workspace_members ADD COLUMN expires_at TIMESTAMPTZ;

CREATE TABLE sys_iam_capabilities (
 action_id TEXT PRIMARY KEY, scope TEXT NOT NULL CHECK(scope IN('self','workspace','platform')),
 dependencies TEXT[] NOT NULL, implementation_state TEXT NOT NULL CHECK(implementation_state IN('legacy','planned','identity','compatibility')),
 legacy_roles TEXT[] NOT NULL, legacy_capabilities TEXT[] NOT NULL,
 catalog_version TEXT NOT NULL,
 CHECK(legacy_capabilities <@ ARRAY['report_review','datasource_manage','audit_view','operation_review']::TEXT[])
);
CREATE TABLE sys_iam_menus (
 menu_id TEXT PRIMARY KEY, label TEXT NOT NULL, category TEXT NOT NULL,
 action_id TEXT NOT NULL REFERENCES sys_iam_capabilities(action_id), route TEXT NOT NULL UNIQUE
);

CREATE TABLE sys_iam_workspace_state (
 workspace_id BIGINT PRIMARY KEY REFERENCES sys_workspaces(id),
 mode TEXT NOT NULL DEFAULT 'LEGACY_GUARDED' CHECK(mode IN('LEGACY_GUARDED','SHADOW')),
 config_version BIGINT NOT NULL DEFAULT 1 CHECK(config_version>0),
 authorization_epoch BIGINT NOT NULL DEFAULT 1 CHECK(authorization_epoch>0),
 minimum_bridge TEXT NOT NULL DEFAULT 'phase1-foundation-1',
 activation_ready BOOLEAN NOT NULL DEFAULT false CHECK(NOT activation_ready)
);
INSERT INTO sys_iam_workspace_state(workspace_id) SELECT id FROM sys_workspaces WHERE id>0;
CREATE FUNCTION dbhub_iam_initial_workspace() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id>0 THEN INSERT INTO sys_iam_workspace_state(workspace_id) VALUES(NEW.id); END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER iam_initial_workspace AFTER INSERT ON sys_workspaces FOR EACH ROW EXECUTE FUNCTION dbhub_iam_initial_workspace();

CREATE TABLE sys_iam_role_revisions (
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id), role_id UUID NOT NULL,
 revision BIGINT NOT NULL CHECK(revision>0), name VARCHAR(100) NOT NULL CHECK(length(trim(name))>0),
 actions TEXT[] NOT NULL, enabled BOOLEAN NOT NULL,
 proposal_id UUID, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,role_id,revision)
);
CREATE TABLE sys_iam_assignments (
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id), assignment_id UUID NOT NULL,
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>0), role_id UUID NOT NULL, role_revision BIGINT NOT NULL,
 subject_type TEXT NOT NULL CHECK(subject_type IN('user','group')), subject_id BIGINT NOT NULL CHECK(subject_id>0),
 -- Approved group beneficiaries are explicit; future members acquire nothing.
 approved_members BIGINT[] NOT NULL CHECK(cardinality(approved_members)>0),
 valid_from TIMESTAMPTZ NOT NULL DEFAULT now(), valid_until TIMESTAMPTZ NOT NULL,
 granted_by BIGINT NOT NULL, proposal_id UUID NOT NULL, revoked_at TIMESTAMPTZ,
 PRIMARY KEY(workspace_id,assignment_id),
 FOREIGN KEY(workspace_id,role_id,role_revision) REFERENCES sys_iam_role_revisions(workspace_id,role_id,revision),
 CHECK(valid_until>valid_from),
 CHECK(subject_type<>'user' OR approved_members=ARRAY[subject_id])
);
CREATE TABLE sys_iam_delegations (
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id), delegation_id UUID NOT NULL,
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>0), issuer_id BIGINT NOT NULL, delegate_id BIGINT NOT NULL,
 governance_actions TEXT[] NOT NULL CHECK(cardinality(governance_actions)>0 AND NOT('*'=ANY(governance_actions))),
 grantable_actions TEXT[] NOT NULL CHECK(cardinality(grantable_actions)>0 AND NOT('*'=ANY(grantable_actions))),
 target_ids BIGINT[] NOT NULL CHECK(cardinality(target_ids)>0), source_ids UUID[] NOT NULL,
 valid_from TIMESTAMPTZ NOT NULL DEFAULT now(), valid_until TIMESTAMPTZ NOT NULL, proposal_id UUID NOT NULL,
 revoked_at TIMESTAMPTZ, PRIMARY KEY(workspace_id,delegation_id),
 CHECK(issuer_id<>delegate_id), CHECK(valid_until>valid_from)
);
CREATE TABLE sys_iam_proposals (
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id), proposal_id UUID NOT NULL,
 request_id UUID NOT NULL, request_hash TEXT NOT NULL, requester_id BIGINT NOT NULL,
 beneficiary_ids BIGINT[] NOT NULL, baseline_config BIGINT NOT NULL CHECK(baseline_config>0),
 baseline_authorization BIGINT NOT NULL CHECK(baseline_authorization>0),
 delegation_id UUID NOT NULL, delegation_version BIGINT NOT NULL,
 patch JSONB NOT NULL CHECK(jsonb_typeof(patch)='object'),
 state TEXT NOT NULL CHECK(state IN('draft','pending','applied','rejected','withdrawn','expired','conflict')),
 reviewer_id BIGINT, review_delegation_id UUID, valid_until TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), reviewed_at TIMESTAMPTZ,
 PRIMARY KEY(workspace_id,proposal_id), UNIQUE(workspace_id,requester_id,request_id),
 CHECK(reviewer_id IS NULL OR (reviewer_id<>requester_id AND NOT(reviewer_id=ANY(beneficiary_ids)))),
 CHECK(state<>'applied' OR (reviewer_id IS NOT NULL AND reviewed_at IS NOT NULL))
);
CREATE TABLE sys_iam_bundles (
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id), bundle_id UUID NOT NULL,
 config_version BIGINT NOT NULL CHECK(config_version>0), minimum_bridge TEXT NOT NULL,
 manifest JSONB NOT NULL CHECK(jsonb_typeof(manifest)='object'),
 state TEXT NOT NULL CHECK(state IN('draft','shadow','superseded')),
 created_by BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,bundle_id), UNIQUE(workspace_id,config_version)
);
CREATE TABLE sys_iam_legacy_envelopes (
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id), user_id BIGINT NOT NULL,
 execution_path TEXT NOT NULL CHECK(execution_path IN('personal','managed_report','managed_dashboard','operation','ticket')),
 source_id BIGINT NOT NULL, action_id TEXT NOT NULL REFERENCES sys_iam_capabilities(action_id),
 legacy_data_role TEXT NOT NULL CHECK(legacy_data_role IN('admin','developer','readonly')),
 constraint_ref JSONB NOT NULL CHECK(jsonb_typeof(constraint_ref)='object'),
 inventory_status TEXT NOT NULL CHECK(inventory_status IN('unknown','candidate','confirmed')),
 evidence_ref TEXT NOT NULL, valid_until TIMESTAMPTZ,
 PRIMARY KEY(workspace_id,user_id,execution_path,source_id,action_id)
);
CREATE TABLE sys_iam_revocations (
 sequence BIGSERIAL PRIMARY KEY, workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),
 actor_id BIGINT NOT NULL, target_id BIGINT NOT NULL, request_id UUID NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN('member_freeze','action_revoke','legacy_membership_change')),
 action_id TEXT REFERENCES sys_iam_capabilities(action_id), reason VARCHAR(1000) NOT NULL,
 evidence JSONB NOT NULL DEFAULT '{}', recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,actor_id,request_id),
 -- Do not acknowledge action-level revocation before all legacy execution
 -- boundaries honor it. B2 must remove this gate only with full coverage.
 CONSTRAINT iam_action_revocation_bridge CHECK(kind<>'action_revoke'),
 CHECK((kind='action_revoke')=(action_id IS NOT NULL))
);
CREATE INDEX iam_revocation_target ON sys_iam_revocations(workspace_id,target_id,kind,action_id,sequence);
CREATE INDEX iam_revocation_workspace_sequence ON sys_iam_revocations(workspace_id,sequence DESC);
CREATE FUNCTION dbhub_iam_immutable() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'IAM evidence is append-only' USING ERRCODE='42501'; END $$;
CREATE TRIGGER iam_revocation_immutable BEFORE UPDATE OR DELETE ON sys_iam_revocations FOR EACH ROW EXECUTE FUNCTION dbhub_iam_immutable();
CREATE TRIGGER iam_revocation_no_truncate BEFORE TRUNCATE ON sys_iam_revocations FOR EACH STATEMENT EXECUTE FUNCTION dbhub_iam_immutable();
CREATE TRIGGER iam_revision_immutable BEFORE UPDATE OR DELETE ON sys_iam_role_revisions FOR EACH ROW EXECUTE FUNCTION dbhub_iam_immutable();
CREATE TRIGGER iam_bundle_immutable BEFORE UPDATE OR DELETE ON sys_iam_bundles FOR EACH ROW EXECUTE FUNCTION dbhub_iam_immutable();

-- Every legacy membership write also invalidates the new generation. This is
-- transactional evidence, not a second grant path and not a business signature.
CREATE FUNCTION dbhub_iam_membership_changed() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.role_code,NEW.active,NEW.capabilities,NEW.expires_at) IS DISTINCT FROM
    (OLD.role_code,OLD.active,OLD.capabilities,OLD.expires_at) THEN
  UPDATE sys_iam_workspace_state SET authorization_epoch=authorization_epoch+1 WHERE workspace_id=NEW.workspace_id;
  INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason,evidence)
  VALUES(NEW.workspace_id,dbhub_actor(),NEW.user_id,gen_random_uuid(),'legacy_membership_change','旧成员资格变化',
    jsonb_build_object('previous_version',OLD.version,'current_version',NEW.version));
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER iam_membership_changed AFTER UPDATE ON sys_workspace_members FOR EACH ROW EXECUTE FUNCTION dbhub_iam_membership_changed();

CREATE FUNCTION dbhub_iam_membership_version() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.version<OLD.version THEN RAISE EXCEPTION 'membership generation cannot decrease' USING ERRCODE='23514'; END IF;
 IF (NEW.role_code,NEW.active,NEW.capabilities,NEW.expires_at) IS DISTINCT FROM
    (OLD.role_code,OLD.active,OLD.capabilities,OLD.expires_at) THEN
  NEW.version:=greatest(OLD.version+1,NEW.version);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER iam_membership_version BEFORE UPDATE ON sys_workspace_members FOR EACH ROW EXECUTE FUNCTION dbhub_iam_membership_version();
CREATE FUNCTION dbhub_iam_revocation_applied() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind IN('member_freeze','action_revoke') THEN
  UPDATE sys_workspace_members SET version=version+1 WHERE workspace_id=NEW.workspace_id AND user_id=NEW.target_id;
  UPDATE sys_iam_workspace_state SET authorization_epoch=authorization_epoch+1 WHERE workspace_id=NEW.workspace_id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER iam_revocation_applied AFTER INSERT ON sys_iam_revocations FOR EACH ROW EXECUTE FUNCTION dbhub_iam_revocation_applied();

CREATE FUNCTION dbhub_iam_state_version() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.workspace_id<>OLD.workspace_id OR NEW.authorization_epoch<OLD.authorization_epoch OR NEW.config_version<OLD.config_version THEN
  RAISE EXCEPTION 'IAM scope and generations cannot rewind' USING ERRCODE='23514';
 END IF;
 IF NEW.mode<>OLD.mode THEN
  NEW.config_version:=greatest(NEW.config_version,OLD.config_version+1);
  NEW.authorization_epoch:=greatest(NEW.authorization_epoch,OLD.authorization_epoch+1);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER iam_state_version BEFORE UPDATE ON sys_iam_workspace_state FOR EACH ROW EXECUTE FUNCTION dbhub_iam_state_version();

CREATE FUNCTION dbhub_iam_validate_actions() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM unnest(NEW.actions) a WHERE NOT EXISTS(
  SELECT 1 FROM sys_iam_capabilities c WHERE c.action_id=a AND c.scope='workspace' AND c.implementation_state<>'compatibility'))
 OR cardinality(NEW.actions)<>(SELECT count(DISTINCT a) FROM unnest(NEW.actions) a) THEN
  RAISE EXCEPTION 'unknown, duplicate or out-of-scope IAM action' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER iam_validate_actions BEFORE INSERT ON sys_iam_role_revisions FOR EACH ROW EXECUTE FUNCTION dbhub_iam_validate_actions();

DO $migration$
DECLARE ns TEXT:=current_schema(); t TEXT;
BEGIN
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_member_role(uid BIGINT) RETURNS TEXT
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT m.role_code FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
 JOIN sys_workspaces w ON w.id=m.workspace_id
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid AND m.active AND u.is_active AND w.enabled
 AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())
 AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=m.workspace_id AND v.target_id=uid AND v.kind='member_freeze')
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_scope_allowed() RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT COALESCE(dbhub_member_role(dbhub_actor())=current_setting('dbhub.actor_role',true),false)
 AND EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_iam_workspace_state s USING(workspace_id)
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=dbhub_actor()
 AND (dbhub_member_epoch()=0 OR m.version=dbhub_member_epoch())
 AND (COALESCE(NULLIF(current_setting('dbhub.authorization_epoch',true),''),'0')::BIGINT=0
      OR s.authorization_epoch=current_setting('dbhub.authorization_epoch',true)::BIGINT)
 AND (COALESCE(NULLIF(current_setting('dbhub.config_version',true),''),'0')::BIGINT=0
      OR s.config_version=current_setting('dbhub.config_version',true)::BIGINT))
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_has_capability(uid BIGINT, capability TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT capability=ANY(ARRAY['report_review','datasource_manage','audit_view','operation_review']::TEXT[])
 AND dbhub_member_role(uid) IS NOT NULL AND EXISTS(
 SELECT 1 FROM sys_workspace_members m WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid
 AND (m.role_code='admin' OR capability=ANY(m.capabilities))
 AND (uid<>dbhub_actor() OR dbhub_member_epoch()=0 OR m.version=dbhub_member_epoch()))
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_legacy_action(uid BIGINT, action TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_member_role(uid) IS NOT NULL AND EXISTS(
 SELECT 1 FROM sys_iam_capabilities c JOIN sys_workspace_members m ON m.user_id=uid AND m.workspace_id=dbhub_workspace()
 JOIN sys_iam_workspace_state s ON s.workspace_id=m.workspace_id
 WHERE c.action_id=action AND c.scope='workspace' AND c.implementation_state='legacy'
 AND s.mode IN('LEGACY_GUARDED','SHADOW') AND NOT s.activation_ready
 AND (m.role_code=ANY(c.legacy_roles) OR m.capabilities && c.legacy_capabilities)
 AND (uid<>dbhub_actor() OR dbhub_member_epoch()=0 OR m.version=dbhub_member_epoch())
 AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=m.workspace_id AND v.target_id=uid AND v.kind='action_revoke' AND v.action_id=action))
 $body$$f$,ns);
 FOREACH t IN ARRAY ARRAY['sys_iam_workspace_state','sys_iam_role_revisions','sys_iam_assignments','sys_iam_delegations','sys_iam_proposals','sys_iam_bundles','sys_iam_legacy_envelopes','sys_iam_revocations'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY iam_workspace_read ON %I FOR SELECT USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND dbhub_member_role(dbhub_actor())=''admin'')',t);
  IF t<>'sys_iam_workspace_state' THEN
   EXECUTE format('CREATE TRIGGER workspace_immutable BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_immutable()',t);
  END IF;
 END LOOP;
END $migration$;

-- Frozen D1.0 catalog. Integration tests compare every row with the embedded
-- Go catalog; a future catalog change requires a new migration.
WITH document AS (SELECT $iam_catalog${
  "version": "DBH-UX-IAM-D1.0",
  "runtime_bridge": "phase1-foundation-1",
  "activation_ready": false,
  "capabilities": [
    {
      "id": "report.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "report.view",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "report.design",
      "scope": "workspace",
      "dependencies": [
        "report.page",
        "report.view"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "report.submit",
      "scope": "workspace",
      "dependencies": [
        "report.design"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "report.review",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "report_review"
      ]
    },
    {
      "id": "report.authorize",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "report.publish",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "report_review"
      ]
    },
    {
      "id": "report.offline",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "report_review"
      ]
    },
    {
      "id": "report.export",
      "scope": "workspace",
      "dependencies": [
        "report.view"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "report.exportReview",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "report_review"
      ]
    },
    {
      "id": "report.subscribe",
      "scope": "workspace",
      "dependencies": [
        "report.view"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "report.subscribeTeam",
      "scope": "workspace",
      "dependencies": [
        "report.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "report.subscribeReview",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "report.stopEmergency",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "report.restoreEmergency",
      "scope": "workspace",
      "dependencies": [
        "report.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.view",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.design",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page",
        "dashboard.view"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.submit",
      "scope": "workspace",
      "dependencies": [
        "dashboard.design"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.review",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "report_review"
      ]
    },
    {
      "id": "dashboard.authorize",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.publish",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "report_review"
      ]
    },
    {
      "id": "dashboard.offline",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "report_review"
      ]
    },
    {
      "id": "dashboard.export",
      "scope": "workspace",
      "dependencies": [
        "dashboard.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.exportReview",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.subscribe",
      "scope": "workspace",
      "dependencies": [
        "dashboard.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.subscribeTeam",
      "scope": "workspace",
      "dependencies": [
        "dashboard.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.subscribeReview",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.stopEmergency",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "dashboard.restoreEmergency",
      "scope": "workspace",
      "dependencies": [
        "dashboard.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "report.reveal",
      "scope": "workspace",
      "dependencies": [
        "report.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "query.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "query.read",
      "scope": "workspace",
      "dependencies": [
        "query.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "query.write",
      "scope": "workspace",
      "dependencies": [
        "query.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "query.reconcile",
      "scope": "workspace",
      "dependencies": [
        "query.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "catalog.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "catalog.metadata",
      "scope": "workspace",
      "dependencies": [
        "catalog.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "catalog.preview",
      "scope": "workspace",
      "dependencies": [
        "catalog.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "catalog.classify",
      "scope": "workspace",
      "dependencies": [
        "catalog.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "connection.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "connection.use",
      "scope": "workspace",
      "dependencies": [
        "connection.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "connection.manage",
      "scope": "workspace",
      "dependencies": [
        "connection.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "connection.proxy.manage",
      "scope": "workspace",
      "dependencies": [
        "connection.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "connection.task.view",
      "scope": "workspace",
      "dependencies": [
        "connection.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "source.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "datasource_manage"
      ]
    },
    {
      "id": "source.definition.manage",
      "scope": "workspace",
      "dependencies": [
        "source.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "datasource_manage"
      ]
    },
    {
      "id": "source.account.manage",
      "scope": "workspace",
      "dependencies": [
        "source.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "datasource_manage"
      ]
    },
    {
      "id": "source.driver.manage",
      "scope": "workspace",
      "dependencies": [
        "source.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "datasource_manage"
      ]
    },
    {
      "id": "source.policy.map",
      "scope": "workspace",
      "dependencies": [
        "source.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "source.task.view",
      "scope": "workspace",
      "dependencies": [
        "source.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "datasource_manage"
      ]
    },
    {
      "id": "source.runtime.view",
      "scope": "workspace",
      "dependencies": [
        "source.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "source.runtime.manage",
      "scope": "workspace",
      "dependencies": [
        "source.page",
        "source.runtime.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "metric.manage",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "operation.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "operation.apply",
      "scope": "workspace",
      "dependencies": [
        "operation.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "operation.review",
      "scope": "workspace",
      "dependencies": [
        "operation.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "operation_review"
      ]
    },
    {
      "id": "operation.execute",
      "scope": "workspace",
      "dependencies": [
        "operation.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "operation.reconcile",
      "scope": "workspace",
      "dependencies": [
        "operation.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "operation.template.manage",
      "scope": "workspace",
      "dependencies": [
        "operation.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "datasource_manage"
      ]
    },
    {
      "id": "operation.account.manage",
      "scope": "workspace",
      "dependencies": [
        "operation.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "datasource_manage"
      ]
    },
    {
      "id": "change.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "change.apply",
      "scope": "workspace",
      "dependencies": [
        "change.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "change.review",
      "scope": "workspace",
      "dependencies": [
        "change.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "change.execute",
      "scope": "workspace",
      "dependencies": [
        "change.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "change.reconcile",
      "scope": "workspace",
      "dependencies": [
        "change.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "task.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin",
        "developer",
        "readonly"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "space.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "space.members.view",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "space.members.manage",
      "scope": "workspace",
      "dependencies": [
        "space.page",
        "space.members.view"
      ],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": []
    },
    {
      "id": "space.members.exit",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.menus.view",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.menus.manage",
      "scope": "workspace",
      "dependencies": [
        "space.page",
        "space.menus.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.roles.view",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.roles.manage",
      "scope": "workspace",
      "dependencies": [
        "space.page",
        "space.roles.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.assign.view",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.assign.manage",
      "scope": "workspace",
      "dependencies": [
        "space.page",
        "space.assign.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.resources.view",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.resources.request",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.resources.revoke",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.policy.view",
      "scope": "workspace",
      "dependencies": [
        "space.page"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "space.policy.manage",
      "scope": "workspace",
      "dependencies": [
        "space.page",
        "space.policy.view"
      ],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "audit.page",
      "scope": "workspace",
      "dependencies": [],
      "state": "legacy",
      "legacy_roles": [
        "admin"
      ],
      "legacy_capabilities": [
        "audit_view"
      ]
    },
    {
      "id": "audit.export",
      "scope": "workspace",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "self.profile",
      "scope": "self",
      "dependencies": [],
      "state": "identity",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "self.security",
      "scope": "self",
      "dependencies": [],
      "state": "identity",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "notification.self",
      "scope": "self",
      "dependencies": [],
      "state": "identity",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.identity.manage",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.sso.manage",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.space.create",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.space.manage",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.ops.view",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.backup.manage",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.restore.execute",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.audit.view",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "platform.governance.recover",
      "scope": "platform",
      "dependencies": [],
      "state": "planned",
      "legacy_roles": [],
      "legacy_capabilities": []
    },
    {
      "id": "source.manage",
      "scope": "workspace",
      "dependencies": [],
      "state": "compatibility",
      "legacy_roles": [],
      "legacy_capabilities": []
    }
  ],
  "menus": [
    {
      "id": "M01",
      "label": "报表",
      "group": "业务应用",
      "capability": "report.page",
      "path": "/reports"
    },
    {
      "id": "M02",
      "label": "仪表盘",
      "group": "业务应用",
      "capability": "dashboard.page",
      "path": "/dashboards"
    },
    {
      "id": "M03",
      "label": "业务办理",
      "group": "业务应用",
      "capability": "operation.page",
      "path": "/business-operations"
    },
    {
      "id": "M04",
      "label": "数据工作台",
      "group": "数据工具",
      "capability": "query.page",
      "path": "/query"
    },
    {
      "id": "M05",
      "label": "数据目录",
      "group": "数据工具",
      "capability": "catalog.page",
      "path": "/assets"
    },
    {
      "id": "M06",
      "label": "我的连接",
      "group": "数据工具",
      "capability": "connection.page",
      "path": "/connections"
    },
    {
      "id": "M07",
      "label": "数据库变更工单",
      "group": "数据工具",
      "capability": "change.page",
      "path": "/tickets"
    },
    {
      "id": "M08",
      "label": "待办中心",
      "group": "协作",
      "capability": "task.page",
      "path": "/inbox"
    },
    {
      "id": "M09",
      "label": "数据管理",
      "group": "管理",
      "capability": "source.page",
      "path": "/sources"
    },
    {
      "id": "M10",
      "label": "空间管理",
      "group": "管理",
      "capability": "space.page",
      "path": "/space"
    },
    {
      "id": "M11",
      "label": "操作审计",
      "group": "管理",
      "capability": "audit.page",
      "path": "/audit"
    }
  ],
  "legacy_capabilities": [
    {
      "id": "report_review",
      "actions": [
        "report.review",
        "report.publish",
        "report.offline",
        "report.exportReview",
        "dashboard.review",
        "dashboard.publish",
        "dashboard.offline"
      ]
    },
    {
      "id": "datasource_manage",
      "actions": [
        "source.page",
        "source.definition.manage",
        "source.account.manage",
        "source.driver.manage",
        "source.task.view",
        "operation.template.manage",
        "operation.account.manage"
      ]
    },
    {
      "id": "audit_view",
      "actions": [
        "audit.page"
      ]
    },
    {
      "id": "operation_review",
      "actions": [
        "operation.review"
      ]
    }
  ]
}
$iam_catalog$::jsonb AS body)
INSERT INTO sys_iam_capabilities(action_id,scope,dependencies,implementation_state,legacy_roles,legacy_capabilities,catalog_version)
SELECT c.id,c.scope,c.dependencies,c.state,c.legacy_roles,c.legacy_capabilities,document.body->>'version'
FROM document CROSS JOIN LATERAL jsonb_to_recordset(document.body->'capabilities')
AS c(id TEXT,scope TEXT,dependencies TEXT[],state TEXT,legacy_roles TEXT[],legacy_capabilities TEXT[]);
INSERT INTO sys_iam_menus(menu_id,label,category,action_id,route) VALUES
('M01','报表','业务应用','report.page','/reports'),
('M02','仪表盘','业务应用','dashboard.page','/dashboards'),
('M03','业务办理','业务应用','operation.page','/business-operations'),
('M04','数据工作台','数据工具','query.page','/query'),
('M05','数据目录','数据工具','catalog.page','/assets'),
('M06','我的连接','数据工具','connection.page','/connections'),
('M07','数据库变更工单','数据工具','change.page','/tickets'),
('M08','待办中心','协作','task.page','/inbox'),
('M09','数据管理','管理','source.page','/sources'),
('M10','空间管理','管理','space.page','/space'),
('M11','操作审计','管理','audit.page','/audit');
