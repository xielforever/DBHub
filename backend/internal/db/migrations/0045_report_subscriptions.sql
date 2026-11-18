CREATE TABLE sys_report_subscriptions (
 id BIGSERIAL PRIMARY KEY,workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace() REFERENCES sys_workspaces(id),
 user_id BIGINT NOT NULL REFERENCES sys_users(id) ON DELETE CASCADE,member_version BIGINT NOT NULL DEFAULT dbhub_member_epoch(),
 report_id BIGINT NOT NULL,publication_no BIGINT NOT NULL CHECK(publication_no>0),
 name VARCHAR(120) NOT NULL,encrypted_definition TEXT NOT NULL,
 interval_minutes INTEGER NOT NULL CHECK(interval_minutes BETWEEN 5 AND 10080),
 enabled BOOLEAN NOT NULL DEFAULT true,archived BOOLEAN NOT NULL DEFAULT false,version BIGINT NOT NULL DEFAULT 1,
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),last_run_at TIMESTAMPTZ,last_status VARCHAR(30) NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,id,user_id),FOREIGN KEY(workspace_id,report_id) REFERENCES sys_managed_reports(workspace_id,id)
);
CREATE INDEX report_subscription_due ON sys_report_subscriptions(next_run_at,id) WHERE enabled;
CREATE TABLE sys_report_subscription_runs (
 id UUID PRIMARY KEY,delivery_id BIGSERIAL UNIQUE,workspace_id BIGINT NOT NULL,user_id BIGINT NOT NULL,subscription_id BIGINT NOT NULL,
 subscription_version BIGINT NOT NULL,member_version BIGINT NOT NULL,report_id BIGINT NOT NULL,publication_no BIGINT NOT NULL,
 name VARCHAR(120) NOT NULL,encrypted_definition TEXT NOT NULL,
 request_id UUID NOT NULL,status VARCHAR(20) NOT NULL DEFAULT 'queued' CHECK(status IN('queued','running','succeeded','failed','interrupted')),
 message VARCHAR(300) NOT NULL DEFAULT '',worker_token UUID,lease_until TIMESTAMPTZ,
 encrypted_snapshot TEXT NOT NULL DEFAULT '',snapshot_hash CHAR(64) NOT NULL DEFAULT '',row_count INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),started_at TIMESTAMPTZ,finished_at TIMESTAMPTZ,expires_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '7 days',
 FOREIGN KEY(workspace_id,subscription_id,user_id) REFERENCES sys_report_subscriptions(workspace_id,id,user_id) ON DELETE CASCADE,
 UNIQUE(workspace_id,user_id,request_id)
);
CREATE INDEX report_subscription_queue ON sys_report_subscription_runs(created_at,id) WHERE status='queued';
CREATE INDEX report_subscription_history ON sys_report_subscription_runs(workspace_id,user_id,created_at DESC);
CREATE INDEX report_subscription_running ON sys_report_subscription_runs(lease_until) WHERE status='running';
CREATE INDEX report_subscription_owner_active ON sys_report_subscription_runs(workspace_id,user_id,subscription_id) WHERE status IN('queued','running');
CREATE INDEX report_subscription_payload_expiry ON sys_report_subscription_runs(expires_at) WHERE encrypted_snapshot<>'';
CREATE TABLE sys_metric_incidents (
 id BIGSERIAL PRIMARY KEY,workspace_id BIGINT NOT NULL,user_id BIGINT NOT NULL,subscription_id BIGINT NOT NULL,
 subscription_version BIGINT NOT NULL,report_id BIGINT NOT NULL,publication_no BIGINT NOT NULL,
 name VARCHAR(120) NOT NULL,encrypted_rule TEXT NOT NULL,encrypted_value TEXT NOT NULL,
 status VARCHAR(20) NOT NULL DEFAULT 'open' CHECK(status IN('open','acknowledged','closed','superseded')),
 condition_active BOOLEAN NOT NULL DEFAULT true,version BIGINT NOT NULL DEFAULT 1,
 first_run_id UUID NOT NULL REFERENCES sys_report_subscription_runs(id),last_run_id UUID NOT NULL REFERENCES sys_report_subscription_runs(id),
 opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),recovered_at TIMESTAMPTZ,closed_at TIMESTAMPTZ,
 FOREIGN KEY(workspace_id,subscription_id,user_id) REFERENCES sys_report_subscriptions(workspace_id,id,user_id) ON DELETE CASCADE,UNIQUE(workspace_id,id,user_id)
);
CREATE UNIQUE INDEX one_open_metric_incident ON sys_metric_incidents(workspace_id,subscription_id) WHERE status IN('open','acknowledged');
CREATE TABLE sys_metric_incident_events (
 id BIGSERIAL PRIMARY KEY,workspace_id BIGINT NOT NULL DEFAULT dbhub_workspace(),user_id BIGINT NOT NULL DEFAULT dbhub_actor(),
 incident_id BIGINT NOT NULL,event_type VARCHAR(30) NOT NULL,comment VARCHAR(1000) NOT NULL DEFAULT '',
 run_id UUID REFERENCES sys_report_subscription_runs(id),created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(workspace_id,incident_id,user_id) REFERENCES sys_metric_incidents(workspace_id,id,user_id) ON DELETE CASCADE
);
CREATE TABLE sys_webhook_deliveries (
 id UUID PRIMARY KEY,workspace_id BIGINT NOT NULL,user_id BIGINT NOT NULL,
 run_id UUID NOT NULL REFERENCES sys_report_subscription_runs(id) ON DELETE CASCADE,
 event_type VARCHAR(30) NOT NULL CHECK(event_type IN('report_ready','metric_opened','metric_recovered')),
 resource_id TEXT NOT NULL,status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK(status IN('pending','sending','sent','failed')),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),worker_token UUID,lease_until TIMESTAMPTZ,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),last_status INTEGER,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),sent_at TIMESTAMPTZ,
 UNIQUE(run_id,event_type)
);
CREATE INDEX webhook_due ON sys_webhook_deliveries(next_attempt_at,id) WHERE status='pending';
CREATE INDEX webhook_sending ON sys_webhook_deliveries(lease_until) WHERE status='sending';
DO $$DECLARE item TEXT;BEGIN
 FOREACH item IN ARRAY ARRAY['sys_report_subscriptions','sys_report_subscription_runs','sys_metric_incidents','sys_metric_incident_events','sys_webhook_deliveries'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',item);
  EXECUTE format('CREATE POLICY scoped_owner ON %I USING(workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND dbhub_scope_allowed()) WITH CHECK(workspace_id=dbhub_workspace() AND user_id=dbhub_actor() AND dbhub_scope_allowed())',item);
  EXECUTE format('CREATE TRIGGER workspace_immutable BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION dbhub_workspace_immutable()',item);
 END LOOP;
END $$;
