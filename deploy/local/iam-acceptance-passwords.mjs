// Test fixture provisioning only. The bootstrap owner's private password is
// retained; these synthetic UI accounts use the repository's CI test password.
import {randomBytes,pbkdf2Sync} from 'node:crypto'
import {writeFile,access} from 'node:fs/promises'
const path='/evidence/fixture-passwords.sql'
try{await access(path);process.stdout.write('Synthetic UI credentials already prepared\n');process.exit(0)}catch{}
const salt=randomBytes(16),key=pbkdf2Sync('TestPassword123!',salt,210000,32,'sha256')
const b64=b=>b.toString('base64').replace(/=+$/,'')
const hash=`pbkdf2_sha256$210000$${b64(salt)}$${b64(key)}`
const sql=`BEGIN;
SET search_path=iam_acceptance;
DO $$BEGIN IF current_database()<>'iam_test' OR current_user<>'iam_test' OR NOT EXISTS(SELECT 1 FROM sys_iam_events WHERE operation='isolated.fixture.bootstrap') THEN RAISE EXCEPTION 'synthetic acceptance fixture required';END IF;END$$;
INSERT INTO sys_users(username,password_hash,role_id) SELECT 'iam-admin','${hash}',role_id FROM sys_users WHERE username='iam-owner' ON CONFLICT DO NOTHING;
INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id)
SELECT a.workspace_id,gen_random_uuid(),a.role_id,a.role_revision,'user',u.id,ARRAY[u.id],a.valid_until,0,gen_random_uuid() FROM sys_iam_assignments a JOIN sys_users owner ON owner.id=a.subject_id AND owner.username='iam-owner' CROSS JOIN sys_users u WHERE u.username='iam-admin' AND NOT EXISTS(SELECT 1 FROM sys_iam_assignments x WHERE x.subject_id=u.id AND x.workspace_id=a.workspace_id);
INSERT INTO sys_iam_principal_modes(workspace_id,user_id,mode,confirmed,legacy_data_role,legacy_capabilities,envelope_ref,bundle_id,valid_until)
SELECT p.workspace_id,u.id,'TARGET',true,'admin','{}','{"synthetic_fixture_only":true}',p.bundle_id,p.valid_until FROM sys_iam_principal_modes p JOIN sys_users owner ON owner.id=p.user_id AND owner.username='iam-owner' CROSS JOIN sys_users u WHERE u.username='iam-admin' ON CONFLICT DO NOTHING;
INSERT INTO sys_iam_delegations(workspace_id,delegation_id,issuer_id,delegate_id,governance_actions,grantable_actions,target_ids,source_ids,valid_until,proposal_id)
SELECT 1,gen_random_uuid(),a.id,b.id,ARRAY['role.propose','role.review','assignment.propose','assignment.review','group.propose','group.review','delegation.propose','delegation.review','menu.configure','member.freeze','restriction.revoke','bundle.prepare','bundle.activate','bundle.rollback'],ARRAY(SELECT action_id FROM sys_iam_capabilities WHERE scope='workspace' AND runtime_enabled ORDER BY action_id),ARRAY(SELECT user_id FROM sys_workspace_members WHERE workspace_id=1 ORDER BY user_id),'{}'::uuid[],now()+interval '1 day',gen_random_uuid() FROM sys_users a CROSS JOIN sys_users b WHERE a.username='iam-reviewer' AND b.username='iam-admin' AND NOT EXISTS(SELECT 1 FROM sys_iam_delegations d WHERE d.workspace_id=1 AND d.issuer_id=a.id AND d.delegate_id=b.id);
UPDATE sys_users SET password_hash='${hash}',auth_version=auth_version+1 WHERE username IN('iam-viewer','iam-developer','iam-supervisor','iam-reviewer','iam-admin');
INSERT INTO sys_iam_events(workspace_id,actor_id,request_id,operation,payload) VALUES(1,0,gen_random_uuid(),'isolated.fixture.ui-accounts','{"synthetic_only":true,"real_credentials_affected":false}');
COMMIT;`
await writeFile(path,sql,{mode:0o600});process.stdout.write('Synthetic UI account fixture prepared\n')
