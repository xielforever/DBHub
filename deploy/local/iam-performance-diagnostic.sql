\set ON_ERROR_STOP on
SET search_path=iam_perf_candidate_v2,public;
SET dbhub.workspace_id='1';
SET dbhub.actor_id='1';
SET dbhub.actor_role='admin';
SET dbhub.runtime_bridge='phase1-bridge-2';
SET dbhub.action_id='connection.use';
SELECT config_version AS cfg,authorization_epoch AS epoch FROM sys_iam_workspace_state WHERE workspace_id=1 \gset
SELECT version AS member FROM sys_workspace_members WHERE workspace_id=1 AND user_id=1 \gset
SELECT set_config('dbhub.config_version',:'cfg',false),set_config('dbhub.authorization_epoch',:'epoch',false),set_config('dbhub.member_version',:'member',false);
EXPLAIN(ANALYZE,BUFFERS) SELECT * FROM dbhub_iam_identity(1);
EXPLAIN(ANALYZE,BUFFERS) SELECT dbhub_iam_has_action(1,'connection.use');
EXPLAIN(ANALYZE,BUFFERS) SELECT dbhub_scope_allowed();
EXPLAIN(ANALYZE,BUFFERS) SELECT * FROM dbhub_iam_identity(1);
EXPLAIN(ANALYZE,BUFFERS) SELECT dbhub_iam_has_action(1,'connection.use');
EXPLAIN(ANALYZE,BUFFERS) SELECT dbhub_scope_allowed();
