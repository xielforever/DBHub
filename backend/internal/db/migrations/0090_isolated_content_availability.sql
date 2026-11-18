CREATE TABLE sys_iam_isolated_content_operators (
    workspace_id BIGINT NOT NULL,
    report_id BIGINT,
    dashboard_id BIGINT,
    user_id BIGINT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('emergency_stop', 'emergency_restore')),
    valid_until TIMESTAMPTZ NOT NULL,
    CHECK (num_nonnulls(report_id, dashboard_id) = 1),
    FOREIGN KEY (workspace_id, report_id) REFERENCES sys_iam_isolated_report_reviews(workspace_id, report_id),
    FOREIGN KEY (workspace_id, dashboard_id) REFERENCES sys_iam_isolated_dashboard_reviews(workspace_id, dashboard_id),
    FOREIGN KEY (workspace_id, user_id) REFERENCES sys_workspace_members(workspace_id, user_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX isolated_report_operator ON sys_iam_isolated_content_operators(workspace_id, report_id, user_id, action)
    WHERE report_id IS NOT NULL;
CREATE UNIQUE INDEX isolated_dashboard_operator ON sys_iam_isolated_content_operators(workspace_id, dashboard_id, user_id, action)
    WHERE dashboard_id IS NOT NULL;
ALTER TABLE sys_iam_isolated_content_operators ENABLE ROW LEVEL SECURITY;

DO $migration$
BEGIN
    EXECUTE format($function$CREATE FUNCTION dbhub_isolated_content_operator(p_report BIGINT, p_dashboard BIGINT, p_action TEXT)
        RETURNS BOOLEAN LANGUAGE SQL SECURITY DEFINER SET search_path=%I,pg_temp AS $body$
        SELECT dbhub_scope_allowed() AND EXISTS (
            SELECT 1 FROM sys_iam_isolated_content_operators
            WHERE workspace_id=dbhub_workspace() AND user_id=dbhub_actor()
              AND report_id IS NOT DISTINCT FROM p_report AND dashboard_id IS NOT DISTINCT FROM p_dashboard
              AND action=p_action AND valid_until>clock_timestamp()
            FOR SHARE)
        $body$$function$, current_schema());
END $migration$;

CREATE TABLE sys_content_availability_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    report_id BIGINT,
    dashboard_id BIGINT,
    base_publication_no BIGINT NOT NULL CHECK (base_publication_no > 0),
    actor_id BIGINT NOT NULL CHECK (actor_id > 0),
    action TEXT NOT NULL CHECK (action IN ('request_offline', 'approve_offline', 'emergency_stop', 'emergency_restore')),
    reason VARCHAR(1000) NOT NULL CHECK (length(trim(reason)) > 0),
    related_event_id BIGINT,
    basis_stamp TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id),
    CHECK (num_nonnulls(report_id, dashboard_id) = 1),
    CHECK ((related_event_id IS NOT NULL) = (action IN ('approve_offline', 'emergency_restore'))),
    FOREIGN KEY (workspace_id, report_id) REFERENCES sys_iam_isolated_report_reviews(workspace_id, report_id),
    FOREIGN KEY (workspace_id, dashboard_id) REFERENCES sys_iam_isolated_dashboard_reviews(workspace_id, dashboard_id),
    FOREIGN KEY (workspace_id, related_event_id) REFERENCES sys_content_availability_events(workspace_id, id)
);
CREATE INDEX content_availability_report_events ON sys_content_availability_events(workspace_id, report_id, base_publication_no, action)
    WHERE report_id IS NOT NULL;
CREATE INDEX content_availability_dashboard_events ON sys_content_availability_events(workspace_id, dashboard_id, base_publication_no, action)
    WHERE dashboard_id IS NOT NULL;
ALTER TABLE sys_content_availability_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY content_availability_read ON sys_content_availability_events FOR SELECT
    USING (workspace_id=dbhub_workspace() AND dbhub_scope_allowed());
CREATE POLICY content_availability_append ON sys_content_availability_events FOR INSERT
    WITH CHECK (workspace_id=dbhub_workspace() AND dbhub_scope_allowed() AND actor_id=dbhub_actor());
