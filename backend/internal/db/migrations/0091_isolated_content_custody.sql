CREATE TABLE sys_content_custody_items (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    freeze_sequence BIGINT NOT NULL,
    report_id BIGINT,
    dashboard_id BIGINT,
    captured JSONB NOT NULL CHECK (jsonb_typeof(captured)='object'),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version>0),
    state TEXT NOT NULL DEFAULT 'held' CHECK (state IN ('held','nominated','accepted','transferred')),
    candidate_id BIGINT,
    nominated_by BIGINT,
    nomination_stamp TEXT,
    accepted_at TIMESTAMPTZ,
    decided_by BIGINT,
    completed_at TIMESTAMPTZ,
    CHECK (num_nonnulls(report_id,dashboard_id)=1),
    CHECK (num_nonnulls(candidate_id,nominated_by,nomination_stamp) = CASE WHEN state='held' THEN 0 ELSE 3 END),
    CHECK ((state IN ('accepted','transferred'))=(accepted_at IS NOT NULL)),
    CHECK ((state='transferred')=(decided_by IS NOT NULL AND completed_at IS NOT NULL)),
    FOREIGN KEY(workspace_id,user_id,freeze_sequence) REFERENCES sys_iam_custody(workspace_id,user_id,freeze_sequence),
    FOREIGN KEY(workspace_id,report_id) REFERENCES sys_iam_isolated_report_reviews(workspace_id,report_id),
    FOREIGN KEY(workspace_id,dashboard_id) REFERENCES sys_iam_isolated_dashboard_reviews(workspace_id,dashboard_id)
);
CREATE UNIQUE INDEX custody_open_report ON sys_content_custody_items(workspace_id,report_id)
    WHERE report_id IS NOT NULL AND state<>'transferred';
CREATE UNIQUE INDEX custody_open_dashboard ON sys_content_custody_items(workspace_id,dashboard_id)
    WHERE dashboard_id IS NOT NULL AND state<>'transferred';
CREATE INDEX custody_items_freeze ON sys_content_custody_items(workspace_id,user_id,freeze_sequence);
CREATE INDEX custody_candidate ON sys_content_custody_items(workspace_id,candidate_id) WHERE candidate_id IS NOT NULL;
ALTER TABLE sys_content_custody_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY custody_transfer_guard ON sys_content_custody_items FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed());
