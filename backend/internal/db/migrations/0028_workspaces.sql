-- Global identities are separate from workspace membership and role.
CREATE TABLE sys_workspaces (
 id BIGSERIAL PRIMARY KEY, name VARCHAR(100) NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT true, allow_unknown_freshness BOOLEAN NOT NULL DEFAULT false,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO sys_workspaces(id,name,enabled) VALUES(0,'平台身份审计',false),(1,'默认工作空间',true);
SELECT setval(pg_get_serial_sequence('sys_workspaces','id'),1);
ALTER TABLE sys_users ADD COLUMN is_platform_admin BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE sys_workspace_members (
 workspace_id BIGINT NOT NULL REFERENCES sys_workspaces(id),
 user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,
 role_code VARCHAR(30) NOT NULL REFERENCES sys_roles(code),
 active BOOLEAN NOT NULL DEFAULT true, version BIGINT NOT NULL DEFAULT 1,
 joined_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(workspace_id,user_id)
);
INSERT INTO sys_workspace_members(workspace_id,user_id,role_code)
 SELECT 1,u.id,r.code FROM sys_users u JOIN sys_roles r ON r.id=u.role_id;
CREATE FUNCTION dbhub_workspace() RETURNS BIGINT LANGUAGE SQL STABLE AS $$
 SELECT COALESCE(NULLIF(current_setting('dbhub.workspace_id',true),''),'0')::bigint
$$;
CREATE FUNCTION dbhub_actor() RETURNS BIGINT LANGUAGE SQL STABLE AS $$
 SELECT COALESCE(NULLIF(current_setting('dbhub.actor_id',true),''),'0')::bigint
$$;
-- Security definer helpers return only the selected membership, never a directory.
DO $migration$
DECLARE ns TEXT:=current_schema();
BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_member_role(uid BIGINT) RETURNS TEXT
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT m.role_code FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
 JOIN sys_workspaces w ON w.id=m.workspace_id
 WHERE m.workspace_id=dbhub_workspace() AND m.user_id=uid AND m.active AND u.is_active AND w.enabled
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_scope_allowed() RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT COALESCE(dbhub_member_role(dbhub_actor())=current_setting('dbhub.actor_role',true),false)
 $body$$f$,ns);
 EXECUTE format($f$CREATE FUNCTION dbhub_identity_visible(uid BIGINT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT dbhub_scope_allowed() AND EXISTS(SELECT 1 FROM sys_workspace_members
 WHERE workspace_id=dbhub_workspace() AND user_id=uid)
 $body$$f$,ns);
END $migration$;

-- A constant default backfills metadata without UPDATE, preserving immutable history.
DO $migration$
DECLARE t RECORD;
BEGIN
 FOR t IN SELECT tablename FROM pg_tables WHERE schemaname=current_schema()
 AND tablename LIKE 'sys_%' AND tablename NOT IN ('sys_users','sys_roles','sys_workspaces','sys_workspace_members')
 AND tablename NOT LIKE 'sys_audit_logs_%'
 LOOP
  EXECUTE format('ALTER TABLE %I ADD COLUMN workspace_id BIGINT NOT NULL DEFAULT 1 REFERENCES sys_workspaces(id)',t.tablename);
  EXECUTE format('ALTER TABLE %I ALTER COLUMN workspace_id SET DEFAULT dbhub_workspace()',t.tablename);
  EXECUTE format('CREATE INDEX ON %I(workspace_id)',t.tablename);
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t.tablename);
  EXECUTE format('CREATE POLICY workspace_scope ON %I USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed()) WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_scope_allowed())',t.tablename);
 END LOOP;
 -- Partitions cannot be read directly to evade the parent's policy.
 FOR t IN SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE 'sys_audit_logs_%'
 LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t.tablename);
 END LOOP;
END $migration$;
ALTER TABLE sys_users ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_identity_read ON sys_users FOR SELECT USING(dbhub_identity_visible(id));
ALTER TABLE sys_workspaces ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_read ON sys_workspaces FOR SELECT USING(id=dbhub_workspace() AND dbhub_scope_allowed());
ALTER TABLE sys_workspace_members ENABLE ROW LEVEL SECURITY;
CREATE POLICY workspace_members_read ON sys_workspace_members FOR SELECT USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed());

-- Composite foreign keys prevent writes that smuggle another workspace's ID.
DO $migration$
DECLARE fk RECORD; childcols TEXT; parentcols TEXT; deleteaction TEXT; cname TEXT;
BEGIN
 FOR fk IN SELECT c.oid,c.conrelid::regclass child,c.confrelid::regclass parent,c.conname,c.conkey,c.confkey,c.confdeltype
 FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_class p ON p.oid=c.confrelid
 JOIN pg_namespace n ON n.oid=t.relnamespace
 WHERE c.contype='f' AND n.nspname=current_schema() AND t.relname LIKE 'sys_%'
 AND t.relname NOT LIKE 'sys_audit_logs%' AND p.relname NOT IN ('sys_users','sys_roles','sys_workspaces')
 AND t.relname<>'sys_workspace_members'
 LOOP
  SELECT string_agg(quote_ident(a.attname),',' ORDER BY ord) INTO childcols FROM unnest(fk.conkey) WITH ORDINALITY k(attnum,ord) JOIN pg_attribute a ON a.attrelid=fk.child AND a.attnum=k.attnum;
  SELECT string_agg(quote_ident(a.attname),',' ORDER BY ord) INTO parentcols FROM unnest(fk.confkey) WITH ORDINALITY k(attnum,ord) JOIN pg_attribute a ON a.attrelid=fk.parent AND a.attnum=k.attnum;
  cname:='ws_ref_'||fk.oid;
  EXECUTE format('CREATE UNIQUE INDEX %I ON %s(workspace_id,%s)',cname,fk.parent,parentcols);
  deleteaction:=CASE fk.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL ('||childcols||')' ELSE 'NO ACTION' END;
  EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I FOREIGN KEY(workspace_id,%s) REFERENCES %s(workspace_id,%s) ON DELETE %s',fk.child,cname,childcols,fk.parent,parentcols,deleteaction);
 END LOOP;
END $migration$;
ALTER TABLE sys_user_groups DROP CONSTRAINT sys_user_groups_name_key;
ALTER TABLE sys_user_groups ADD UNIQUE(workspace_id,name);
ALTER TABLE sys_query_settings DROP CONSTRAINT sys_query_settings_pkey;
ALTER TABLE sys_query_settings ADD PRIMARY KEY(workspace_id,user_id);
ALTER TABLE sys_query_executions DROP CONSTRAINT sys_query_executions_pkey;
ALTER TABLE sys_query_executions ADD PRIMARY KEY(workspace_id,user_id,request_id);
ALTER TABLE sys_report_commands DROP CONSTRAINT sys_report_commands_pkey;
ALTER TABLE sys_report_commands ADD PRIMARY KEY(workspace_id,actor_id,request_id);
CREATE FUNCTION dbhub_join_initial_workspace() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO sys_workspace_members(workspace_id,user_id,role_code)
 SELECT COALESCE(NULLIF(dbhub_workspace(),0),1),NEW.id,code FROM sys_roles WHERE id=NEW.role_id;
 RETURN NEW;
END $$;
CREATE TRIGGER user_initial_workspace AFTER INSERT ON sys_users FOR EACH ROW EXECUTE FUNCTION dbhub_join_initial_workspace();
CREATE OR REPLACE FUNCTION dbhub_source_visible(definition_id BIGINT, actor_id BIGINT) RETURNS BOOLEAN
 LANGUAGE SQL STABLE AS $$
 SELECT dbhub_member_role(actor_id) IS NOT NULL AND EXISTS(
 SELECT 1 FROM sys_source_definitions d JOIN sys_source_families f USING(family_id)
 WHERE d.id=definition_id AND (
 (d.visibility='private' AND d.owner_id=actor_id) OR
 (d.visibility='global' AND (dbhub_member_role(actor_id)='admin' OR f.access_mode='global' OR EXISTS(
 SELECT 1 FROM sys_source_grants g WHERE g.family_id=f.family_id AND
 (g.user_id=actor_id OR EXISTS(SELECT 1 FROM sys_user_group_members m WHERE m.group_id=g.group_id AND m.user_id=actor_id)))))))
 $$;

-- Catalog names and personal creation UUIDs are independent per workspace.
DROP INDEX jdbc_driver_global_artifact;
DROP INDEX jdbc_driver_private_artifact;
CREATE UNIQUE INDEX jdbc_driver_global_artifact ON sys_jdbc_drivers(workspace_id,artifact_id) WHERE visibility='global';
CREATE UNIQUE INDEX jdbc_driver_private_artifact ON sys_jdbc_drivers(workspace_id,created_by,artifact_id) WHERE visibility='private';
ALTER TABLE sys_datasource_templates DROP CONSTRAINT sys_datasource_templates_template_key_owner_scope_revision_key;
ALTER TABLE sys_datasource_templates ADD UNIQUE(workspace_id,template_key,owner_scope,revision);
DROP INDEX report_creation_key;
CREATE UNIQUE INDEX report_creation_key ON sys_reports(workspace_id,owner_user_id,creation_key) WHERE creation_key IS NOT NULL;
