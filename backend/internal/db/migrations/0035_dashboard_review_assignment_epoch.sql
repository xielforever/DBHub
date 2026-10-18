ALTER TABLE sys_dashboard_reviews ADD COLUMN assignment_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE sys_dashboard_refreshes ADD COLUMN review_assignment_version BIGINT NOT NULL DEFAULT 0;
