CREATE TABLE sys_iam_isolated_formal_exports (
    workspace_id BIGINT NOT NULL,
    report_id BIGINT NOT NULL,
    PRIMARY KEY (workspace_id,report_id),
    FOREIGN KEY (workspace_id,report_id) REFERENCES sys_iam_isolated_report_reviews(workspace_id,report_id)
);
ALTER TABLE sys_iam_isolated_formal_exports ENABLE ROW LEVEL SECURITY;
CREATE POLICY isolated_formal_export_scope ON sys_iam_isolated_formal_exports FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed());

ALTER TABLE sys_report_exports
    ADD COLUMN generation_request TEXT NOT NULL DEFAULT '',
    ADD COLUMN generation_preview TEXT NOT NULL DEFAULT '',
    ADD COLUMN generation_member_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT formal_export_payloads CHECK (
        (generation_request='' AND generation_preview='' AND generation_member_version=0)
        OR (generation_request<>'' AND generation_preview<>'' AND generation_member_version>0 AND encrypted_payload='')
    );

CREATE TABLE sys_report_export_jobs (
    id UUID PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    export_id BIGINT NOT NULL,
    approval_version BIGINT NOT NULL CHECK (approval_version>1),
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed','interrupted')),
    worker_token UUID,
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    finished_at TIMESTAMPTZ,
    UNIQUE (workspace_id,export_id),
    FOREIGN KEY (workspace_id,export_id) REFERENCES sys_report_exports(workspace_id,id),
    CHECK ((status='running')=(lease_until IS NOT NULL AND worker_token IS NOT NULL)),
    CHECK ((status IN ('succeeded','failed','interrupted'))=(finished_at IS NOT NULL))
);
CREATE INDEX formal_export_queue ON sys_report_export_jobs(created_at,id) WHERE status='queued';
CREATE INDEX formal_export_running ON sys_report_export_jobs(lease_until) WHERE status='running';
ALTER TABLE sys_report_export_jobs ENABLE ROW LEVEL SECURITY;
CREATE POLICY formal_export_job_read ON sys_report_export_jobs FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND EXISTS (
        SELECT 1 FROM sys_report_exports e WHERE e.workspace_id=sys_report_export_jobs.workspace_id
        AND e.id=export_id AND (e.user_id=dbhub_actor() OR e.reviewed_by=dbhub_actor()
            OR dbhub_iam_has_action(dbhub_actor(),'report.exportReview'))));
CREATE POLICY formal_export_job_create ON sys_report_export_jobs FOR INSERT
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND status='queued' AND EXISTS (
        SELECT 1 FROM sys_report_exports e WHERE e.workspace_id=sys_report_export_jobs.workspace_id AND e.id=export_id
        AND e.status='approved' AND e.version=approval_version AND e.generation_request<>'' AND e.reviewed_by=dbhub_actor()));
CREATE POLICY formal_export_job_finish ON sys_report_export_jobs FOR UPDATE
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND status='running' AND EXISTS (
        SELECT 1 FROM sys_report_exports e WHERE e.workspace_id=sys_report_export_jobs.workspace_id AND e.id=export_id
        AND e.status='approved' AND e.version=approval_version AND e.user_id=dbhub_actor()
        AND e.generation_member_version=dbhub_member_epoch()))
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND status='succeeded');

CREATE TABLE sys_report_export_files (
    workspace_id BIGINT NOT NULL,
    export_id BIGINT NOT NULL,
    format TEXT NOT NULL CHECK (format IN ('csv','json','xlsx')),
    encrypted_file TEXT NOT NULL CHECK (encrypted_file<>''),
    sha256 CHAR(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    snapshot_sha256 CHAR(64) NOT NULL CHECK (snapshot_sha256 ~ '^[0-9a-f]{64}$'),
    row_count INTEGER NOT NULL CHECK (row_count BETWEEN 0 AND 100000),
    size_bytes INTEGER NOT NULL CHECK (size_bytes BETWEEN 1 AND 16777216),
    generated_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()+interval '24 hours',
    PRIMARY KEY (workspace_id,export_id),
    FOREIGN KEY (workspace_id,export_id) REFERENCES sys_report_export_jobs(workspace_id,export_id),
    CHECK (expires_at=generated_at+interval '24 hours')
);
ALTER TABLE sys_report_export_files ENABLE ROW LEVEL SECURITY;
CREATE POLICY formal_export_file_create ON sys_report_export_files FOR INSERT
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND EXISTS (
        SELECT 1 FROM sys_report_export_jobs j JOIN sys_report_exports e
            ON e.workspace_id=j.workspace_id AND e.id=j.export_id
        WHERE j.workspace_id=sys_report_export_files.workspace_id AND j.export_id=sys_report_export_files.export_id
        AND j.status='succeeded' AND e.version=j.approval_version AND e.user_id=dbhub_actor()
        AND e.generation_member_version=dbhub_member_epoch()));
