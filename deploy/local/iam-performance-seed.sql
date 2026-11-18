\set ON_ERROR_STOP on
SET search_path=iam_perf_baseline;
BEGIN;
DO $$BEGIN
 IF current_schema()<>'iam_perf_baseline' OR (SELECT count(*) FROM sys_users)>1 THEN RAISE EXCEPTION 'fresh isolated baseline required';END IF;
END$$;
-- Synthetic benchmark metadata only. Actual content lifecycle/security tests
-- use the separately approved three-report fixture, never this bulk fixture.
CREATE FUNCTION pg_temp.perf_clone(tab TEXT,overrides JSONB) RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE original JSONB;columns TEXT;
BEGIN
 EXECUTE format('SELECT to_jsonb(t) FROM iam_acceptance.%I t WHERE workspace_id=1 LIMIT 1',tab) INTO original;
 IF original IS NULL THEN RAISE EXCEPTION 'missing isolated template %',tab;END IF;
 SELECT string_agg(quote_ident(attname),',' ORDER BY attnum) INTO columns FROM pg_attribute WHERE attrelid=format('iam_perf_baseline.%I',tab)::regclass AND attnum>0 AND NOT attisdropped AND attgenerated='';
 EXECUTE format('INSERT INTO iam_perf_baseline.%I (%s) SELECT %s FROM jsonb_populate_record(NULL::iam_perf_baseline.%I,$1)',tab,columns,columns,tab) USING original||overrides;
END$$;
UPDATE sys_workspaces SET name='A52 空间 1' WHERE id=1;
INSERT INTO sys_workspaces(id,name) SELECT n,'A52 空间 '||n FROM generate_series(2,5)n;
DO $$DECLARE n INT;role TEXT;caps TEXT[];hash TEXT;BEGIN
 SELECT password_hash INTO hash FROM iam_acceptance.sys_users WHERE username='iam-viewer';
 IF hash IS NULL THEN RAISE EXCEPTION 'isolated password template missing';END IF;
 FOR n IN 1..1000 LOOP
  role:=CASE (n-1)%7 WHEN 0 THEN 'admin' WHEN 1 THEN 'developer' ELSE 'readonly' END;
  caps:=CASE (n-1)%7 WHEN 3 THEN ARRAY['report_review'] WHEN 4 THEN ARRAY['datasource_manage'] WHEN 5 THEN ARRAY['audit_view'] WHEN 6 THEN ARRAY['operation_review'] ELSE '{}'::text[] END;
  IF n=1 THEN UPDATE sys_users SET password_hash=hash WHERE id=1;
  ELSE INSERT INTO sys_users(id,username,password_hash,role_id) SELECT n,'perf-user-'||n,hash,id FROM sys_roles WHERE code=role;END IF;
  -- The first seven profiles deliberately share original workspace 1.
  DELETE FROM sys_workspace_members WHERE user_id=n;
  INSERT INTO sys_workspace_members(workspace_id,user_id,role_code,capabilities) VALUES(CASE WHEN n<=7 THEN 1 ELSE (n-1)%5+1 END,n,role,caps),(CASE WHEN n<=7 THEN 2 ELSE n%5+1 END,n,role,caps);
 END LOOP;
END$$;
-- Do not flood notices while manufacturing history. Ordinary constraints and
-- authorization triggers remain enabled; these notification triggers alone
-- are restored in the same transaction before commit.
ALTER TABLE sys_report_reviews DISABLE TRIGGER report_notification;
ALTER TABLE sys_report_releases DISABLE TRIGGER report_release_notification;
ALTER TABLE sys_dashboard_reviews DISABLE TRIGGER dashboard_notification;
ALTER TABLE sys_dashboard_releases DISABLE TRIGGER dashboard_release_notification;
DO $$DECLARE wid INT;k INT;uid BIGINT;cid BIGINT;sid BIGINT;owner BIGINT;reviewer BIGINT;rid BIGINT;did BIGINT;fam UUID;audience JSONB;mf JSONB;definition JSONB;validation UUID;BEGIN
 FOR wid IN 1..5 LOOP
  SELECT min(user_id) INTO owner FROM sys_workspace_members WHERE workspace_id=wid AND role_code IN('admin','developer');
  SELECT min(user_id) INTO reviewer FROM sys_workspace_members WHERE workspace_id=wid AND user_id<>owner AND (role_code='admin' OR 'report_review'=ANY(capabilities));
  PERFORM set_config('dbhub.workspace_id',wid::text,true);PERFORM set_config('dbhub.actor_id',owner::text,true);
  PERFORM set_config('dbhub.actor_role',(SELECT role_code FROM sys_workspace_members WHERE workspace_id=wid AND user_id=owner),true);
  SELECT jsonb_agg(user_id ORDER BY user_id) INTO audience FROM sys_workspace_members WHERE workspace_id=wid;
  FOR k IN 1..10 LOOP
   sid:=(wid-1)*10+k;fam:=md5('A52-source-'||wid||'-'||k)::uuid;
   PERFORM pg_temp.perf_clone('sys_source_families',jsonb_build_object('workspace_id',wid,'family_id',fam));
   PERFORM pg_temp.perf_clone('sys_source_definitions',jsonb_build_object('workspace_id',wid,'id',sid,'family_id',fam,'owner_id',owner,'name','A52 共享源 '||wid||'-'||k,'revision',1,'visibility','global','enabled',true));
  END LOOP;
  FOR uid IN SELECT user_id FROM sys_workspace_members WHERE workspace_id=wid ORDER BY user_id LOOP
   cid:=uid*10+wid;
   PERFORM pg_temp.perf_clone('sys_connections',jsonb_build_object('workspace_id',wid,'id',cid,'user_id',uid,'name','A52 个人连接 '||uid||'-'||wid,'definition_revision_id',(wid-1)*10+1,'definition_family_id',md5('A52-source-'||wid||'-1')::uuid,'ssh_tunnel_id',NULL,'proxy_id',NULL));
  END LOOP;
  FOR k IN 1..1000 LOOP
   SELECT id,user_id INTO cid,uid FROM sys_connections WHERE workspace_id=wid ORDER BY id OFFSET (k-1)%(SELECT count(*) FROM sys_connections WHERE workspace_id=wid) LIMIT 1;
   INSERT INTO sys_query_policies(workspace_id,connection_id,table_name,role_code,column_permissions,row_conditions,updated_by)
   VALUES(wid,cid,'a52_table_'||k,'readonly','{"sensitive_value":"masked"}','[{"column":"department_id","operator":"=","value":"D01"}]',owner);
  END LOOP;
  PERFORM pg_temp.perf_clone('sys_report_accounts',jsonb_build_object('workspace_id',wid,'id',wid,'name','A52 受管执行账号 '||wid,'definition_revision_id',(wid-1)*10+1,'created_by',owner));
  FOR k IN 1..100 LOOP
   rid:=(wid-1)*100+k;validation:=md5('A52-validation-'||rid)::uuid;
   SELECT draft||jsonb_build_object('name','A52 报表 '||wid||'-'||k,'definition_revision_id',(wid-1)*10+1) INTO definition FROM iam_acceptance.sys_managed_reports WHERE workspace_id=1 ORDER BY id LIMIT 1;
   SELECT rv.manifest||jsonb_build_object('user_ids',audience,'group_ids','[]'::jsonb) INTO mf FROM iam_acceptance.sys_report_reviews rv WHERE workspace_id=1 AND status='approved' ORDER BY id LIMIT 1;
   PERFORM pg_temp.perf_clone('sys_managed_reports',jsonb_build_object('workspace_id',wid,'id',rid,'owner_id',owner,'definition_revision_id',(wid-1)*10+1,'draft',definition,'active_release_id',NULL));
   PERFORM pg_temp.perf_clone('sys_report_versions',jsonb_build_object('workspace_id',wid,'id',rid,'report_id',rid,'definition_revision_id',(wid-1)*10+1,'definition',definition,'submitted_by',owner));
   PERFORM pg_temp.perf_clone('sys_report_reviews',jsonb_build_object('workspace_id',wid,'id',rid,'version_id',rid,'status','pending','account_id',NULL,'manifest',NULL,'manifest_hash',NULL,'validation_id',NULL,'decided_by',NULL));
   PERFORM pg_temp.perf_clone('sys_report_validations',jsonb_build_object('workspace_id',wid,'id',validation,'review_id',rid,'account_id',wid,'manifest',mf,'performed_by',reviewer));
   UPDATE sys_report_reviews SET status='approved',account_id=wid,manifest=mf,manifest_hash=encode(sha256(convert_to(mf::text,'UTF8')),'hex'),validation_id=validation,decided_by=reviewer WHERE id=rid;
   PERFORM pg_temp.perf_clone('sys_report_releases',jsonb_build_object('workspace_id',wid,'id',rid,'report_id',rid,'review_id',rid,'performed_by',reviewer));
   UPDATE sys_managed_reports SET active_release_id=rid WHERE id=rid;
  END LOOP;
  FOR k IN 1..30 LOOP
   did:=(wid-1)*30+k;
   SELECT draft||jsonb_build_object('name','A52 仪表盘 '||wid||'-'||k,'user_ids',audience,'group_ids','[]'::jsonb) INTO definition FROM iam_acceptance.sys_managed_dashboards WHERE workspace_id=1 ORDER BY id LIMIT 1;
   PERFORM pg_temp.perf_clone('sys_managed_dashboards',jsonb_build_object('workspace_id',wid,'id',did,'owner_id',owner,'draft',definition,'active_release_id',NULL));
   PERFORM pg_temp.perf_clone('sys_dashboard_versions',jsonb_build_object('workspace_id',wid,'id',did,'dashboard_id',did,'definition',definition,'submitted_by',owner));
   PERFORM pg_temp.perf_clone('sys_dashboard_reviews',jsonb_build_object('workspace_id',wid,'id',did,'version_id',did,'dashboard_id',did,'reviewer_id',reviewer,'decided_by',reviewer,'expires_at',now()+interval '1 day'));
   PERFORM pg_temp.perf_clone('sys_dashboard_releases',jsonb_build_object('workspace_id',wid,'id',did,'dashboard_id',did,'review_id',did,'performed_by',reviewer));
   UPDATE sys_managed_dashboards SET active_release_id=did WHERE id=did;
  END LOOP;
  SELECT min(id),min(user_id) INTO cid,uid FROM sys_connections WHERE workspace_id=wid;
  INSERT INTO sys_source_tasks(id,workspace_id,actor_id,connection_id,connection_name,kind,status,progress,phase,request_id,request_hash,member_version,config_updated_at,finished_at)
  SELECT md5('A52-task-'||wid||'-'||n)::uuid,wid,uid,cid,'A52 历史任务','check','succeeded',100,'合成历史元信息',md5('A52-request-'||wid||'-'||n)::uuid,repeat('0',64),1,now(),now() FROM generate_series(1,2000)n;
  INSERT INTO sys_audit_logs(workspace_id,user_id,action,resource_type,resource_name,status,context)
  SELECT wid,owner,'A52_FIXTURE','PERFORMANCE','合成审计记录',1,'{"fixture":"A52"}'::jsonb FROM generate_series(1,10000);
 END LOOP;
END$$;
ALTER TABLE sys_report_reviews ENABLE TRIGGER report_notification;
ALTER TABLE sys_report_releases ENABLE TRIGGER report_release_notification;
ALTER TABLE sys_dashboard_reviews ENABLE TRIGGER dashboard_notification;
ALTER TABLE sys_dashboard_releases ENABLE TRIGGER dashboard_release_notification;
SELECT setval(pg_get_serial_sequence('sys_users','id'),1000);
SELECT setval(pg_get_serial_sequence('sys_connections','id'),(SELECT max(id) FROM sys_connections));
SELECT setval(pg_get_serial_sequence('sys_workspaces','id'),5);
COMMIT;
