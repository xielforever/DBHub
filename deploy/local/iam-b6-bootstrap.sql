\set ON_ERROR_STOP on
SET search_path=iam_b6_acceptance;
BEGIN;
DO $$BEGIN
 IF current_database()<>'iam_test' OR current_user<>'iam_test'
 OR (SELECT count(*) FROM sys_users WHERE username IN('iam-b6-author','iam-b6-reviewer','iam-b6-member'))<>3
 OR EXISTS(SELECT 1 FROM sys_iam_principal_modes)
 THEN RAISE EXCEPTION 'fresh synthetic B6 namespace required';END IF;
END$$;
INSERT INTO sys_iam_isolation_guard VALUES('phase1-b6-http',current_database(),'Synthetic HTTP B6 acceptance; not live activation or human approval');
INSERT INTO sys_iam_delegations(workspace_id,delegation_id,issuer_id,delegate_id,governance_actions,grantable_actions,target_ids,source_ids,valid_until,proposal_id)
SELECT 1,gen_random_uuid(),issuer.id,delegate.id,
 ARRAY['role.propose','role.review','assignment.propose','assignment.review','restriction.revoke','bundle.prepare','bundle.activate','bundle.rollback'],
 ARRAY(SELECT action_id FROM sys_iam_capabilities WHERE scope='workspace' AND runtime_enabled ORDER BY action_id),
 ARRAY(SELECT id FROM sys_users ORDER BY id),'{}'::uuid[],now()+interval '1 day',gen_random_uuid()
FROM sys_users issuer CROSS JOIN sys_users delegate
WHERE issuer.username IN('iam-b6-author','iam-b6-reviewer') AND delegate.username IN('iam-b6-author','iam-b6-reviewer') AND issuer.id<>delegate.id;
INSERT INTO sys_iam_events(workspace_id,actor_id,request_id,operation,payload)
VALUES(1,0,gen_random_uuid(),'isolated.fixture.b6-bootstrap','{"synthetic_only":true,"human_approval":false,"real_activation":false}');
COMMIT;
