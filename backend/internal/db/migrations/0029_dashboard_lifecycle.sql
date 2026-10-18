-- Report contract and audience changes reuse immutable report releases.
CREATE TABLE sys_report_audience_changes (
 workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 review_id BIGINT PRIMARY KEY, report_id BIGINT NOT NULL, base_publication_no BIGINT NOT NULL,
 account_id BIGINT NOT NULL, user_ids BIGINT[] NOT NULL, group_ids BIGINT[] NOT NULL,
 group_versions JSONB NOT NULL, request_ids BIGINT[] NOT NULL DEFAULT '{}',
 FOREIGN KEY(workspace_id,review_id) REFERENCES sys_report_reviews(workspace_id,id),
 FOREIGN KEY(workspace_id,report_id) REFERENCES sys_managed_reports(workspace_id,id),
 FOREIGN KEY(workspace_id,account_id) REFERENCES sys_report_accounts(workspace_id,id)
);
CREATE TABLE sys_access_requests (
 id BIGSERIAL PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 requester_id BIGINT NOT NULL REFERENCES sys_users(id), report_id BIGINT NOT NULL,
 dashboard_id BIGINT, reason VARCHAR(1000) NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','approved_pending_release','fulfilled','rejected','cancelled','expired')),
 version BIGINT NOT NULL DEFAULT 1, assigned_to BIGINT REFERENCES sys_users(id), review_id BIGINT,
 actual_publication_no BIGINT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '7 days',
 UNIQUE(workspace_id,id), FOREIGN KEY(workspace_id,report_id) REFERENCES sys_managed_reports(workspace_id,id)
);
CREATE UNIQUE INDEX access_request_pending ON sys_access_requests(workspace_id,requester_id,report_id) WHERE status IN ('pending','processing','approved_pending_release');
CREATE TABLE sys_managed_dashboards (
 id BIGSERIAL PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 owner_id BIGINT NOT NULL REFERENCES sys_users(id), draft JSONB NOT NULL,
 draft_version BIGINT NOT NULL DEFAULT 1, latest_version BIGINT NOT NULL DEFAULT 0,
 publication_no BIGINT NOT NULL DEFAULT 0, active_release_id BIGINT,
 lifecycle TEXT NOT NULL DEFAULT 'active' CHECK(lifecycle IN ('active','archived','deleted')),
 lifecycle_epoch BIGINT NOT NULL DEFAULT 1, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id)
);
CREATE TABLE sys_dashboard_versions (
 id BIGSERIAL PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 dashboard_id BIGINT NOT NULL, number BIGINT NOT NULL, definition JSONB NOT NULL, content_hash CHAR(64) NOT NULL,
 submitted_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL, submitter_name TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,id), UNIQUE(dashboard_id,number),
 FOREIGN KEY(workspace_id,dashboard_id) REFERENCES sys_managed_dashboards(workspace_id,id)
);
CREATE TRIGGER dashboard_version_immutable BEFORE UPDATE OR DELETE ON sys_dashboard_versions FOR EACH ROW EXECUTE FUNCTION dbhub_report_history_actor_null('submitted_by');
CREATE TABLE sys_dashboard_reviews (
 id BIGSERIAL PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 version_id BIGINT NOT NULL, dashboard_id BIGINT NOT NULL, lifecycle_epoch BIGINT NOT NULL,
 reviewer_id BIGINT NOT NULL REFERENCES sys_users(id), status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','rejected','withdrawn','expired')),
 validation JSONB, validated_at TIMESTAMPTZ, comment VARCHAR(1000) NOT NULL DEFAULT '',
 decided_by BIGINT REFERENCES sys_users(id), decided_at TIMESTAMPTZ,
 expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '7 days',
 UNIQUE(workspace_id,id), FOREIGN KEY(workspace_id,version_id) REFERENCES sys_dashboard_versions(workspace_id,id),
 FOREIGN KEY(workspace_id,dashboard_id) REFERENCES sys_managed_dashboards(workspace_id,id)
);
CREATE UNIQUE INDEX dashboard_one_pending ON sys_dashboard_reviews(dashboard_id) WHERE status='pending';
CREATE FUNCTION dbhub_dashboard_review_guard() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND OLD.status='pending' AND NEW.workspace_id=OLD.workspace_id AND NEW.version_id=OLD.version_id AND NEW.dashboard_id=OLD.dashboard_id AND NEW.lifecycle_epoch=OLD.lifecycle_epoch AND NEW.expires_at=OLD.expires_at THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'dashboard review is immutable after decision';
END $$;
CREATE TRIGGER dashboard_review_guard BEFORE UPDATE OR DELETE ON sys_dashboard_reviews FOR EACH ROW EXECUTE FUNCTION dbhub_dashboard_review_guard();
CREATE TABLE sys_dashboard_audience_changes (
 workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 review_id BIGINT PRIMARY KEY, base_publication_no BIGINT NOT NULL,
 FOREIGN KEY(workspace_id,review_id) REFERENCES sys_dashboard_reviews(workspace_id,id)
);
CREATE TABLE sys_dashboard_releases (
 id BIGSERIAL PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 dashboard_id BIGINT NOT NULL, number BIGINT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('publish','rollback','unpublish','archive')),
 review_id BIGINT, lifecycle_epoch BIGINT NOT NULL, performed_by BIGINT REFERENCES sys_users(id) ON DELETE SET NULL,
 performer_name TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id), UNIQUE(dashboard_id,number),
 FOREIGN KEY(workspace_id,dashboard_id) REFERENCES sys_managed_dashboards(workspace_id,id),
 FOREIGN KEY(workspace_id,review_id) REFERENCES sys_dashboard_reviews(workspace_id,id)
);
CREATE TRIGGER dashboard_release_immutable BEFORE UPDATE OR DELETE ON sys_dashboard_releases FOR EACH ROW EXECUTE FUNCTION dbhub_report_history_actor_null('performed_by');
ALTER TABLE sys_managed_dashboards ADD FOREIGN KEY(workspace_id,active_release_id) REFERENCES sys_dashboard_releases(workspace_id,id);
ALTER TABLE sys_access_requests ADD FOREIGN KEY(workspace_id,dashboard_id) REFERENCES sys_managed_dashboards(workspace_id,id);
CREATE TABLE sys_dashboard_refreshes (
 id UUID PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 user_id BIGINT NOT NULL REFERENCES sys_users(id), dashboard_id BIGINT NOT NULL,
 publication_no BIGINT NOT NULL, lifecycle_epoch BIGINT NOT NULL, mode TEXT NOT NULL CHECK(mode IN ('published','draft','review')),
 draft_version BIGINT NOT NULL, review_id BIGINT, definition JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '180 seconds',
 UNIQUE(workspace_id,id), FOREIGN KEY(workspace_id,dashboard_id) REFERENCES sys_managed_dashboards(workspace_id,id)
);
CREATE TABLE sys_dashboard_refresh_tiles (
 workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id), refresh_id UUID NOT NULL,
 tile_key VARCHAR(64) NOT NULL, report_id BIGINT NOT NULL, report_publication_no BIGINT NOT NULL,
 account_version BIGINT NOT NULL, account_generation BIGINT NOT NULL, contract_hash CHAR(64) NOT NULL,
 execution_id UUID NOT NULL UNIQUE, status TEXT NOT NULL, queried_at TIMESTAMPTZ, data_as_of TIMESTAMPTZ,
 PRIMARY KEY(refresh_id,tile_key), FOREIGN KEY(workspace_id,refresh_id) REFERENCES sys_dashboard_refreshes(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,report_id) REFERENCES sys_managed_reports(workspace_id,id)
);
-- Shared cluster leases cover dashboard queries from every page/process.
CREATE TABLE sys_dashboard_query_leases (
 id UUID PRIMARY KEY, workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 user_id BIGINT NOT NULL REFERENCES sys_users(id), account_id BIGINT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '190 seconds',
 FOREIGN KEY(workspace_id,account_id) REFERENCES sys_report_accounts(workspace_id,id)
);
CREATE INDEX dashboard_refresh_expiry ON sys_dashboard_refreshes(expires_at);
CREATE INDEX dashboard_lease_expiry ON sys_dashboard_query_leases(expires_at);
DO $migration$
DECLARE t TEXT;
BEGIN
 FOREACH t IN ARRAY ARRAY['sys_report_audience_changes','sys_access_requests','sys_managed_dashboards','sys_dashboard_versions','sys_dashboard_reviews','sys_dashboard_audience_changes','sys_dashboard_releases','sys_dashboard_refreshes','sys_dashboard_refresh_tiles','sys_dashboard_query_leases']
 LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY workspace_scope ON %I USING(workspace_id=dbhub_workspace() AND dbhub_scope_allowed()) WITH CHECK(workspace_id=dbhub_workspace() AND dbhub_scope_allowed())',t);
 END LOOP;
END $migration$;
