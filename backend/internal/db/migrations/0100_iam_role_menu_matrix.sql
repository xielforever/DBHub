-- New workspaces may opt in explicitly. Existing workspaces retain their
-- navigation, governance, and target-principal activation settings.
ALTER TABLE sys_iam_workspace_state
 ADD COLUMN matrix_enabled BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE sys_iam_menu_configuration
 ADD COLUMN role_visibility JSONB NOT NULL DEFAULT '{}'::jsonb
 CHECK(jsonb_typeof(role_visibility)='object');
