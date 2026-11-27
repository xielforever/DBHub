CREATE TABLE sys_iam_isolated_dashboard_reviews (
    workspace_id BIGINT NOT NULL,
    dashboard_id BIGINT NOT NULL,
    isolation_id TEXT NOT NULL REFERENCES sys_iam_isolation_guard(isolation_id),
    PRIMARY KEY (workspace_id, dashboard_id),
    FOREIGN KEY (workspace_id, dashboard_id) REFERENCES sys_managed_dashboards(workspace_id, id)
);
ALTER TABLE sys_iam_isolated_dashboard_reviews ENABLE ROW LEVEL SECURITY;
CREATE POLICY isolated_dashboard_review_enrollment ON sys_iam_isolated_dashboard_reviews FOR SELECT
    USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed());

ALTER TABLE sys_dashboard_reviews ADD COLUMN governed_validation_stamp TEXT NOT NULL DEFAULT '';
ALTER TABLE sys_dashboard_refresh_tiles ADD COLUMN business_policy_stamp TEXT NOT NULL DEFAULT '';

CREATE TABLE sys_dashboard_review_checks (
    workspace_id BIGINT NOT NULL,
    review_id BIGINT NOT NULL,
    assignment_version BIGINT NOT NULL CHECK (assignment_version > 0),
    boundary JSONB NOT NULL CHECK (jsonb_typeof(boundary) = 'object'),
    business_reviewer_id BIGINT NOT NULL CHECK (business_reviewer_id > 0),
    business_comment VARCHAR(1000) NOT NULL,
    business_signed_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    requires_data BOOLEAN NOT NULL,
    data_reviewer_id BIGINT,
    data_stamp TEXT,
    data_comment VARCHAR(1000),
    data_signed_at TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, review_id, assignment_version),
    FOREIGN KEY (workspace_id, review_id) REFERENCES sys_dashboard_reviews(workspace_id, id),
    CHECK ((data_reviewer_id IS NULL AND data_stamp IS NULL AND data_signed_at IS NULL)
        OR (requires_data AND data_reviewer_id IS NOT NULL AND data_reviewer_id <> business_reviewer_id
            AND data_stamp IS NOT NULL AND data_signed_at IS NOT NULL))
);
ALTER TABLE sys_dashboard_review_checks ENABLE ROW LEVEL SECURITY;
CREATE POLICY dashboard_review_check_scope ON sys_dashboard_review_checks
    USING (workspace_id = dbhub_workspace() AND dbhub_scope_allowed());
