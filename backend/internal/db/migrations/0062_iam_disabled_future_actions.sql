-- These editors/executors have no current product implementation. A finite
-- role/delegation must not advertise them as available capabilities. Read-only
-- resource/policy pages, emergency restriction and member freeze remain enabled.
DO $migration$ DECLARE changed INT;
BEGIN
 UPDATE sys_iam_capabilities SET runtime_enabled=false
 WHERE action_id IN('source.runtime.manage','space.resources.request','space.policy.manage','audit.export')
 AND runtime_enabled;
 GET DIAGNOSTICS changed=ROW_COUNT;
 IF changed>0 THEN
  UPDATE sys_iam_workspace_state SET authorization_epoch=authorization_epoch+1;
 END IF;
END $migration$;
