-- Current actors must be active members; historical actor references remain
-- stable after departure. New ownership/member links require a current member.
CREATE FUNCTION dbhub_workspace_user_reference() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE field TEXT; uid BIGINT;
BEGIN
 FOREACH field IN ARRAY TG_ARGV LOOP
  IF TG_OP='UPDATE' AND to_jsonb(NEW)->field=to_jsonb(OLD)->field THEN CONTINUE; END IF;
  uid:=(to_jsonb(NEW)->>field)::bigint;
  IF uid IS NOT NULL AND NOT EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id WHERE m.workspace_id=NEW.workspace_id AND m.user_id=uid AND m.active AND u.is_active) THEN
   RAISE EXCEPTION 'workspace member reference is invalid' USING ERRCODE='23503';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
DO $migration$
DECLARE t RECORD; args TEXT;
BEGIN
 FOR t IN SELECT c.conrelid::regclass tab,string_agg(DISTINCT quote_literal(a.attname),',') fields
 FROM pg_constraint c JOIN pg_class p ON p.oid=c.conrelid JOIN pg_namespace n ON n.oid=p.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=ANY(c.conkey)
 WHERE c.contype='f' AND c.confrelid='sys_users'::regclass AND n.nspname=current_schema()
 AND p.relname NOT IN('sys_workspace_members') AND p.relname NOT LIKE 'sys_audit_logs%'
 GROUP BY c.conrelid
 LOOP
  EXECUTE format('CREATE TRIGGER workspace_user_reference BEFORE INSERT OR UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_user_reference(%s)',t.tab,t.fields);
 END LOOP;
END $migration$;
-- Pre-authentication events belong to the platform identity log (workspace 0).
-- A workspace admin has no implicit access to that log or to another tenant.
DO $migration$
DECLARE ns TEXT:=current_schema();
BEGIN
 EXECUTE format($f$CREATE FUNCTION dbhub_platform_actor() RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
 SELECT EXISTS(SELECT 1 FROM sys_users WHERE id=dbhub_actor() AND is_active AND is_platform_admin)
 $body$$f$,ns);
END $migration$;
CREATE POLICY platform_identity_audit ON sys_audit_logs FOR SELECT USING(workspace_id=0 AND dbhub_workspace()=0 AND dbhub_platform_actor());
-- Don't permit moving mutable records to another tenant with an owner session.
CREATE FUNCTION dbhub_workspace_immutable() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.workspace_id<>OLD.workspace_id THEN RAISE EXCEPTION 'workspace identity is immutable'; END IF;
 RETURN NEW;
END $$;
DO $migration$
DECLARE t RECORD;
BEGIN
 FOR t IN SELECT table_name FROM information_schema.columns WHERE table_schema=current_schema() AND column_name='workspace_id' AND table_name NOT LIKE 'sys_audit_logs_%'
 LOOP EXECUTE format('CREATE TRIGGER workspace_immutable BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_immutable()',t.table_name); END LOOP;
END $migration$;

UPDATE sys_roles SET description='管理当前工作空间的资源、成员和审批，不自动获得报表数据权限' WHERE code='admin';
UPDATE sys_roles SET description='在当前工作空间开发报表、仪表盘与个人数据连接' WHERE code='developer';
UPDATE sys_roles SET description='在当前工作空间按独立授权查看报表和仪表盘' WHERE code='readonly';
