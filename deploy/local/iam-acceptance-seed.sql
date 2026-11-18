-- Synthetic fixture only. Never execute on dbhub_meta, the existing WSL PG,
-- or a business database. No human approval is represented by this seed.
BEGIN;
SET search_path=iam_acceptance;
DO $fixture$
DECLARE candidate RECORD;uid BIGINT;rid UUID;bid UUID:=gen_random_uuid();actions TEXT[];data_role TEXT;
BEGIN
 IF current_database()<>'iam_test' OR current_user<>'iam_test' OR current_schema()<>'iam_acceptance' THEN
 RAISE EXCEPTION 'isolated acceptance database required';END IF;
 IF EXISTS(SELECT 1 FROM sys_iam_events WHERE operation='isolated.fixture.bootstrap') THEN RETURN;END IF;
 FOR candidate IN SELECT * FROM (VALUES
 ('iam-viewer','readonly','门店经营伙伴',ARRAY['report.page','report.view','dashboard.page','dashboard.view']),
 ('iam-developer','developer','数据产品制作员',ARRAY['report.page','report.view','report.design','report.submit','dashboard.page','dashboard.view','dashboard.design','dashboard.submit','query.page','query.read','query.write','query.reconcile','catalog.page','catalog.metadata','catalog.preview','connection.page','connection.use','connection.manage','connection.proxy.manage','connection.task.view','task.page']),
 ('iam-supervisor','readonly','经营内容负责人',ARRAY['report.page','report.view','report.review','report.publish','report.offline','report.exportReview','dashboard.page','dashboard.view','dashboard.review','dashboard.publish','dashboard.offline','task.page','audit.page']),
 ('iam-owner','admin','空间治理维护员',ARRAY['space.page','space.members.view','space.members.manage','space.members.exit','space.menus.view','space.menus.manage','space.roles.view','space.roles.manage','space.assign.view','space.assign.manage','space.resources.view','space.resources.revoke','space.policy.view','space.policy.manage','audit.page','task.page','source.page','source.definition.manage','source.account.manage','source.driver.manage','source.task.view','source.runtime.view']),
 ('iam-reviewer','admin','独立治理复核员',ARRAY['space.page','space.members.view','space.menus.view','space.roles.view','space.roles.manage','space.assign.view','space.assign.manage','space.policy.view','space.members.exit','space.resources.revoke','task.page'])
 ) AS f(username,base_role,role_name,actions) LOOP
  INSERT INTO sys_users(username,password_hash,role_id) SELECT candidate.username,u.password_hash,r.id FROM sys_users u CROSS JOIN sys_roles r WHERE u.username='iam-owner' AND r.code=candidate.base_role ON CONFLICT(username) DO NOTHING;
  SELECT id INTO uid FROM sys_users WHERE username=candidate.username;
  UPDATE sys_workspace_members SET capabilities=CASE WHEN candidate.username='iam-supervisor' THEN ARRAY['report_review','audit_view'] ELSE capabilities END WHERE workspace_id=1 AND user_id=uid;
  rid:=gen_random_uuid();
  WITH RECURSIVE dependencies(id) AS(SELECT unnest(candidate.actions) UNION SELECT unnest(c.dependencies) FROM dependencies d JOIN sys_iam_capabilities c ON c.action_id=d.id)
  SELECT array_agg(id ORDER BY id) INTO actions FROM dependencies;
  INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by) VALUES(1,rid,1,candidate.role_name,actions,true,1);
  INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled) VALUES(1,rid,1,candidate.role_name,true);
  INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id)
  VALUES(1,gen_random_uuid(),rid,1,'user',uid,ARRAY[uid],now()+interval '1 day',1,gen_random_uuid());
  INSERT INTO sys_iam_principal_modes(workspace_id,user_id,mode,confirmed,legacy_data_role,legacy_capabilities,envelope_ref,bundle_id,valid_until)
  SELECT 1,uid,'TARGET',true,m.role_code,m.capabilities,'{"synthetic_fixture_only":true,"not_a_migration_acceptance":true}'::jsonb,bid,now()+interval '1 day' FROM sys_workspace_members m WHERE m.workspace_id=1 AND m.user_id=uid;
 END LOOP;
 -- A separate untouched legacy principal proves that selecting these test
 -- identities never silently converts other members to the target engine.
 INSERT INTO sys_users(username,password_hash,role_id) SELECT 'iam-legacy',u.password_hash,r.id FROM sys_users u CROSS JOIN sys_roles r WHERE u.username='iam-owner' AND r.code='readonly' ON CONFLICT DO NOTHING;
 INSERT INTO sys_iam_bundles(workspace_id,bundle_id,config_version,minimum_bridge,manifest,state,created_by)
 SELECT 1,bid,config_version+1,'phase1-bridge-2','{"synthetic_fixture_only":true,"not_a_governance_approval":true}','shadow',1 FROM sys_iam_workspace_state WHERE workspace_id=1;
 FOR candidate IN SELECT a.id issuer,b.id delegate FROM sys_users a CROSS JOIN sys_users b WHERE (a.username='iam-owner' AND b.username='iam-reviewer') OR (a.username='iam-reviewer' AND b.username='iam-owner') LOOP
  INSERT INTO sys_iam_delegations(workspace_id,delegation_id,issuer_id,delegate_id,governance_actions,grantable_actions,target_ids,source_ids,valid_until,proposal_id)
  SELECT 1,gen_random_uuid(),candidate.issuer,candidate.delegate,
  ARRAY['role.propose','role.review','assignment.propose','assignment.review','group.propose','group.review','delegation.propose','delegation.review','menu.configure','member.freeze','restriction.revoke','bundle.prepare','bundle.activate','bundle.rollback'],
  ARRAY(SELECT action_id FROM sys_iam_capabilities WHERE scope='workspace' AND runtime_enabled ORDER BY action_id),
  ARRAY(SELECT user_id FROM sys_workspace_members WHERE workspace_id=1 ORDER BY user_id),'{}'::uuid[],now()+interval '1 day',gen_random_uuid();
 END LOOP;
 UPDATE sys_workspaces SET name='阶段1 · 隔离验收空间' WHERE id=1;
 UPDATE sys_iam_workspace_state SET mode='COMPAT_ACTIVE',activation_ready=true,active_bundle_id=bid,minimum_bridge='phase1-bridge-2' WHERE workspace_id=1;
 INSERT INTO sys_iam_events(workspace_id,actor_id,request_id,operation,payload) VALUES(1,0,gen_random_uuid(),'isolated.fixture.bootstrap','{"synthetic_only":true,"real_spaces_activated":false,"human_approval_claimed":false}');
END $fixture$;
COMMIT;
