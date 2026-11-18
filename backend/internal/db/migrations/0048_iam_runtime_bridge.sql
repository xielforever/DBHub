-- Additive runtime bridge. Existing spaces remain LEGACY_GUARDED; there is no
-- automatic role, principal, source, workflow or task migration.
ALTER TABLE sys_iam_capabilities ADD COLUMN runtime_enabled BOOLEAN NOT NULL DEFAULT false;
UPDATE sys_iam_capabilities SET runtime_enabled=true WHERE scope='workspace'
 AND action_id NOT IN('source.manage','report.reveal','report.authorize','dashboard.authorize',
 'report.subscribeTeam','report.subscribeReview','report.stopEmergency','report.restoreEmergency',
 'dashboard.export','dashboard.exportReview','dashboard.subscribe','dashboard.subscribeTeam','dashboard.subscribeReview',
 'dashboard.stopEmergency','dashboard.restoreEmergency','source.policy.map');

ALTER TABLE sys_iam_workspace_state DROP CONSTRAINT sys_iam_workspace_state_mode_check;
ALTER TABLE sys_iam_workspace_state DROP CONSTRAINT sys_iam_workspace_state_activation_ready_check;
ALTER TABLE sys_iam_workspace_state ADD CONSTRAINT iam_mode CHECK(mode IN('LEGACY_GUARDED','SHADOW','COMPAT_ACTIVE'));
ALTER TABLE sys_iam_workspace_state ADD COLUMN active_bundle_id UUID;
ALTER TABLE sys_iam_workspace_state ADD CONSTRAINT iam_activation_proof CHECK(
 (mode<>'COMPAT_ACTIVE' AND NOT activation_ready) OR
 (mode='COMPAT_ACTIVE' AND activation_ready AND active_bundle_id IS NOT NULL AND minimum_bridge='phase1-bridge-2'));

CREATE TABLE sys_iam_role_heads(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),role_id UUID NOT NULL,
 revision BIGINT NOT NULL,display_name VARCHAR(100) NOT NULL,enabled BOOLEAN NOT NULL,
 version BIGINT NOT NULL DEFAULT 1,PRIMARY KEY(workspace_id,role_id),
 FOREIGN KEY(workspace_id,role_id,revision) REFERENCES sys_iam_role_revisions(workspace_id,role_id,revision)
);
CREATE TABLE sys_iam_principal_modes(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),user_id BIGINT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN('LEGACY_GUARDED','TARGET')),confirmed BOOLEAN NOT NULL DEFAULT false,
 legacy_data_role TEXT NOT NULL CHECK(legacy_data_role IN('admin','developer','readonly')),
 legacy_capabilities TEXT[] NOT NULL,version BIGINT NOT NULL DEFAULT 1,
 envelope_ref JSONB NOT NULL CHECK(jsonb_typeof(envelope_ref)='object'),
 valid_until TIMESTAMPTZ,bundle_id UUID,
 PRIMARY KEY(workspace_id,user_id),CHECK(mode<>'TARGET' OR confirmed)
);
CREATE TABLE sys_iam_menu_configuration(
 workspace_id BIGINT PRIMARY KEY REFERENCES sys_workspaces(id),version BIGINT NOT NULL DEFAULT 1,
 items JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(items)='array'),preferred_menu TEXT REFERENCES sys_iam_menus(menu_id),
 updated_by BIGINT NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE sys_iam_home_preferences(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),user_id BIGINT NOT NULL,
 menu_id TEXT NOT NULL REFERENCES sys_iam_menus(menu_id),PRIMARY KEY(workspace_id,user_id)
);
CREATE TABLE sys_iam_action_releases(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),user_id BIGINT NOT NULL,
 action_id TEXT NOT NULL REFERENCES sys_iam_capabilities(action_id),revocation_sequence BIGINT NOT NULL,
 proposal_id UUID NOT NULL,valid_until TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(workspace_id,user_id,action_id,revocation_sequence),
 FOREIGN KEY(workspace_id,proposal_id) REFERENCES sys_iam_proposals(workspace_id,proposal_id)
);
CREATE TABLE sys_iam_group_versions(
 workspace_id BIGINT NOT NULL,group_id BIGINT NOT NULL,version BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY(workspace_id,group_id),FOREIGN KEY(workspace_id,group_id) REFERENCES sys_user_groups(workspace_id,id) ON DELETE CASCADE
);
INSERT INTO sys_iam_group_versions(workspace_id,group_id) SELECT workspace_id,id FROM sys_user_groups;
CREATE FUNCTION dbhub_iam_group_changed() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE wid BIGINT;gid BIGINT;
BEGIN
 wid:=CASE WHEN TG_OP='DELETE' THEN OLD.workspace_id ELSE NEW.workspace_id END;
 gid:=CASE WHEN TG_OP='DELETE' THEN OLD.group_id ELSE NEW.group_id END;
 INSERT INTO sys_iam_group_versions(workspace_id,group_id) SELECT wid,gid WHERE EXISTS(SELECT 1 FROM sys_user_groups WHERE workspace_id=wid AND id=gid)
 ON CONFLICT(workspace_id,group_id) DO UPDATE SET version=sys_iam_group_versions.version+1;
 UPDATE sys_iam_workspace_state SET authorization_epoch=authorization_epoch+1 WHERE workspace_id=wid;
 -- Keep this already-admitted transaction internally consistent. External
 -- contexts keep their previous epoch and must rebind after commit.
 IF wid=dbhub_workspace() THEN
 PERFORM set_config('dbhub.authorization_epoch',(SELECT authorization_epoch::text FROM sys_iam_workspace_state WHERE workspace_id=wid),true);
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER iam_group_changed AFTER INSERT OR DELETE ON sys_user_group_members FOR EACH ROW EXECUTE FUNCTION dbhub_iam_group_changed();
CREATE TABLE sys_iam_runtime_agents(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),agent_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN('api','worker','database')),bridge_version TEXT NOT NULL,
 route_fingerprint TEXT NOT NULL,heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,agent_id)
);
CREATE TABLE sys_iam_custody(
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),user_id BIGINT NOT NULL,
 freeze_sequence BIGINT NOT NULL REFERENCES sys_iam_revocations(sequence),
 assets JSONB NOT NULL,state TEXT NOT NULL DEFAULT 'awaiting_handover' CHECK(state='awaiting_handover'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),PRIMARY KEY(workspace_id,user_id,freeze_sequence)
);
CREATE INDEX iam_assignment_beneficiaries ON sys_iam_assignments USING gin(approved_members);
CREATE INDEX iam_assignment_active ON sys_iam_assignments(workspace_id,role_id,role_revision) WHERE revoked_at IS NULL;
ALTER TABLE sys_iam_revocations DROP CONSTRAINT iam_action_revocation_bridge;
CREATE FUNCTION dbhub_iam_revoke_bridge() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind='action_revoke' AND (SELECT count(DISTINCT kind) FROM sys_iam_runtime_agents
 WHERE workspace_id=NEW.workspace_id AND bridge_version='phase1-bridge-2'
 AND route_fingerprint<>'' AND heartbeat_at>now()-interval '2 minutes')<>3 THEN
 RAISE EXCEPTION 'current API, database and worker bridge proof required' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER iam_action_revoke_bridge BEFORE INSERT ON sys_iam_revocations FOR EACH ROW EXECUTE FUNCTION dbhub_iam_revoke_bridge();

DO $migration$ DECLARE ns TEXT:=current_schema();t TEXT;
BEGIN
 EXECUTE format('ALTER FUNCTION dbhub_iam_group_changed() SECURITY DEFINER SET search_path=%I,pg_temp',ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_is_target(uid BIGINT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT EXISTS(SELECT 1 FROM sys_iam_workspace_state s JOIN sys_iam_principal_modes p USING(workspace_id)
 WHERE s.workspace_id=dbhub_workspace() AND s.mode='COMPAT_ACTIVE' AND s.activation_ready
 AND p.user_id=uid AND p.mode='TARGET')
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_qualified(uid BIGINT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
 JOIN sys_workspaces w ON w.id=m.workspace_id WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid
 AND m.active AND u.is_active AND w.enabled AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())
 AND NOT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=m.workspace_id AND v.target_id=uid AND v.kind='member_freeze'))
 AND (NOT dbhub_iam_is_target(uid) OR EXISTS(SELECT 1 FROM sys_iam_principal_modes p WHERE p.workspace_id=dbhub_workspace()
 AND p.user_id=uid AND p.confirmed AND (p.valid_until IS NULL OR p.valid_until>statement_timestamp())))
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_member_role(uid BIGINT) RETURNS TEXT
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT CASE WHEN dbhub_iam_is_target(uid) THEN
 CASE WHEN m.role_code='readonly' OR p.legacy_data_role='readonly' THEN 'readonly'
 WHEN m.role_code='developer' OR p.legacy_data_role='developer' THEN 'developer' ELSE 'admin' END
 ELSE m.role_code END
 FROM sys_workspace_members m LEFT JOIN sys_iam_principal_modes p ON p.workspace_id=m.workspace_id AND p.user_id=uid
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid AND dbhub_iam_qualified(uid)
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_legacy_capability(uid BIGINT,capability TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT capability=ANY(ARRAY['report_review','datasource_manage','audit_view','operation_review']::TEXT[])
 AND dbhub_iam_qualified(uid) AND EXISTS(SELECT 1 FROM sys_workspace_members m
 LEFT JOIN sys_iam_principal_modes p ON p.workspace_id=m.workspace_id AND p.user_id=uid
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid
 AND (m.role_code='admin' OR capability=ANY(m.capabilities))
 AND (NOT dbhub_iam_is_target(uid) OR p.legacy_data_role='admin' OR capability=ANY(p.legacy_capabilities)))
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_action_denied(uid BIGINT,action TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=dbhub_workspace() AND v.target_id=uid
 AND v.kind='action_revoke' AND v.action_id=action AND NOT EXISTS(
 SELECT 1 FROM sys_iam_action_releases x JOIN sys_iam_proposals p ON p.workspace_id=x.workspace_id AND p.proposal_id=x.proposal_id
 WHERE x.workspace_id=v.workspace_id AND x.user_id=uid AND x.action_id=action AND x.revocation_sequence>=v.sequence
 AND x.valid_until>statement_timestamp() AND p.state='applied'))
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_action_basis(uid BIGINT,action TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_iam_qualified(uid) AND NOT dbhub_iam_action_denied(uid,action) AND
 CASE WHEN NOT dbhub_iam_is_target(uid) THEN EXISTS(
 SELECT 1 FROM sys_iam_capabilities c JOIN sys_workspace_members m ON m.workspace_id=dbhub_workspace() AND m.user_id=uid
 WHERE c.action_id=action AND c.scope='workspace' AND c.implementation_state='legacy'
 AND (m.role_code=ANY(c.legacy_roles) OR m.capabilities && c.legacy_capabilities))
 ELSE EXISTS(SELECT 1 FROM sys_iam_assignments a JOIN sys_iam_role_heads h ON h.workspace_id=a.workspace_id AND h.role_id=a.role_id
 JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision
 JOIN sys_iam_capabilities c ON c.action_id=action
 WHERE a.workspace_id=dbhub_workspace() AND a.role_revision=h.revision AND h.enabled AND r.enabled
 AND action=ANY(r.actions) AND c.runtime_enabled AND c.scope='workspace'
 AND a.approved_members @> ARRAY[uid] AND a.revoked_at IS NULL
 AND a.valid_from<=statement_timestamp() AND a.valid_until>statement_timestamp()
 AND (a.subject_type='user' AND a.subject_id=uid OR a.subject_type='group' AND EXISTS(
 SELECT 1 FROM sys_user_group_members gm WHERE gm.workspace_id=a.workspace_id AND gm.group_id=a.subject_id AND gm.user_id=uid))
 AND (action<>'query.write' OR dbhub_member_role(uid)<>'readonly')) END
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_iam_has_action(uid BIGINT,action TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_iam_action_basis(uid,action) AND NOT EXISTS(
 WITH RECURSIVE dependencies(id,path) AS(
 SELECT unnest(c.dependencies),ARRAY[action] FROM sys_iam_capabilities c WHERE c.action_id=action
 UNION ALL SELECT unnest(c.dependencies),d.path||d.id FROM dependencies d JOIN sys_iam_capabilities c ON c.action_id=d.id
 WHERE NOT d.id=ANY(d.path))
 SELECT 1 FROM dependencies d WHERE d.id=ANY(d.path) OR NOT dbhub_iam_action_basis(uid,d.id))
 $body$$f$,ns);
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_has_capability(uid BIGINT,capability TEXT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT (uid<>dbhub_actor() OR dbhub_member_epoch()=0 OR EXISTS(SELECT 1 FROM sys_workspace_members m WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid AND m.version=dbhub_member_epoch())) AND CASE WHEN NOT dbhub_iam_is_target(uid) THEN dbhub_iam_legacy_capability(uid,capability)
 ELSE CASE WHEN uid=dbhub_actor() AND COALESCE(current_setting('dbhub.action_id',true),'')<>'' THEN
 EXISTS(SELECT 1 FROM sys_iam_capabilities c WHERE c.action_id=current_setting('dbhub.action_id',true)
 AND capability=ANY(c.legacy_capabilities) AND dbhub_iam_has_action(uid,c.action_id))
 ELSE CASE capability WHEN 'report_review' THEN dbhub_iam_has_action(uid,'report.review')
 WHEN 'datasource_manage' THEN dbhub_iam_has_action(uid,'source.definition.manage')
 WHEN 'audit_view' THEN dbhub_iam_has_action(uid,'audit.page')
 WHEN 'operation_review' THEN dbhub_iam_has_action(uid,'operation.review') ELSE false END END END
 $body$$f$,ns);
 -- Resource visibility retains its original ceiling; a new functional source
 -- management permission never grants every source or another user's password.
 EXECUTE format($f$CREATE OR REPLACE FUNCTION dbhub_source_visible(definition_id BIGINT, actor_id BIGINT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_member_role(actor_id) IS NOT NULL AND EXISTS(
 SELECT 1 FROM sys_source_definitions d JOIN sys_source_families f USING(family_id) WHERE d.id=definition_id AND (
 (d.visibility='private' AND d.owner_id=actor_id) OR
 (d.visibility='global' AND (dbhub_iam_legacy_capability(actor_id,'datasource_manage') OR f.access_mode='global' OR EXISTS(
 SELECT 1 FROM sys_source_grants g WHERE g.family_id=f.family_id AND
 (g.user_id=actor_id OR EXISTS(SELECT 1 FROM sys_user_group_members m WHERE m.group_id=g.group_id AND m.user_id=actor_id)))))))
 $body$$f$,ns);
 FOREACH t IN ARRAY ARRAY['sys_iam_role_heads','sys_iam_principal_modes','sys_iam_menu_configuration','sys_iam_home_preferences',
 'sys_iam_action_releases','sys_iam_group_versions','sys_iam_runtime_agents','sys_iam_custody'] LOOP
 EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
 EXECUTE format('CREATE POLICY iam_read ON %I FOR SELECT USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed())',t);
 EXECUTE format('CREATE TRIGGER workspace_immutable BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_immutable()',t);
 END LOOP;
END $migration$;
