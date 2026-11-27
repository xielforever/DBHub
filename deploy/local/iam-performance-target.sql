\set ON_ERROR_STOP on
SET search_path=iam_perf_candidate_v2;
BEGIN;
DO $$BEGIN IF NOT EXISTS(SELECT 1 FROM schema_migrations WHERE version='0053_iam_legacy_mutation_barrier.sql') THEN RAISE EXCEPTION 'candidate migration incomplete';END IF;END$$;
-- Explicit synthetic performance principals; never a governance approval.
-- The runtime isolation guard remains empty and real activation remains shut.
INSERT INTO sys_iam_bundles(workspace_id,bundle_id,config_version,minimum_bridge,manifest,state,created_by)
 SELECT id,md5('A52-bundle-'||id)::uuid,2,'phase1-bridge-2','{"fixture":"A52_SYNTHETIC_NOT_RELEASE_APPROVAL"}','shadow',1 FROM sys_workspaces WHERE id>0;
INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by)
 SELECT w.id,md5('A52-role-'||w.id||'-'||p.n)::uuid,1,'A52 许可组合 '||p.n,
 ARRAY(SELECT action_id FROM sys_iam_capabilities c WHERE c.scope='workspace' AND c.runtime_enabled AND c.implementation_state='legacy' AND (p.role=ANY(c.legacy_roles) OR p.caps&&c.legacy_capabilities) ORDER BY action_id),true,1
 FROM sys_workspaces w CROSS JOIN (VALUES(0,'admin','{}'::text[]),(1,'developer','{}'::text[]),(2,'readonly','{}'::text[]),(3,'readonly',ARRAY['report_review']),(4,'readonly',ARRAY['datasource_manage']),(5,'readonly',ARRAY['audit_view']),(6,'readonly',ARRAY['operation_review']))p(n,role,caps) WHERE w.id>0;
INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled)
 SELECT workspace_id,role_id,revision,name,enabled FROM sys_iam_role_revisions;
INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id)
 SELECT workspace_id,md5('A52-assignment-'||workspace_id||'-'||user_id)::uuid,md5('A52-role-'||workspace_id||'-'||((user_id-1)%7))::uuid,1,'user',user_id,ARRAY[user_id],now()+interval '1 day',1,md5('A52-synthetic-proposal')::uuid FROM sys_workspace_members;
INSERT INTO sys_iam_principal_modes(workspace_id,user_id,mode,confirmed,legacy_data_role,legacy_capabilities,envelope_ref,bundle_id)
 SELECT workspace_id,user_id,'TARGET',true,role_code,capabilities,'{"fixture":"A52 immutable original data identity"}',md5('A52-bundle-'||workspace_id)::uuid FROM sys_workspace_members;
UPDATE sys_iam_workspace_state SET mode='COMPAT_ACTIVE',activation_ready=true,active_bundle_id=md5('A52-bundle-'||workspace_id)::uuid,minimum_bridge='phase1-bridge-2';
COMMIT;
