ALTER TABLE sys_report_team_subscriptions
    ADD COLUMN schedule_interval_minutes INTEGER CHECK (schedule_interval_minutes BETWEEN 5 AND 10080),
    ADD COLUMN schedule_checked_at TIMESTAMPTZ;
CREATE INDEX team_subscription_schedule ON sys_report_team_subscriptions(review_until,id) WHERE status='approved';

ALTER TABLE sys_report_team_periods ADD COLUMN scheduled_for TIMESTAMPTZ;
CREATE UNIQUE INDEX team_period_automatic_slot ON sys_report_team_periods(workspace_id,plan_id,scheduled_for)
    WHERE scheduled_for IS NOT NULL;

ALTER TABLE sys_report_export_files
    DROP CONSTRAINT sys_report_export_files_encrypted_file_check,
    ADD COLUMN payload_purged_at TIMESTAMPTZ,
    ADD CHECK ((encrypted_file='')=(payload_purged_at IS NOT NULL) AND
        (payload_purged_at IS NULL OR payload_purged_at>=expires_at));
CREATE INDEX formal_export_payload_expiry ON sys_report_export_files(expires_at,workspace_id,export_id)
    WHERE payload_purged_at IS NULL;
CREATE INDEX formal_export_credential_expiry ON sys_report_export_credentials(expires_at,digest);

ALTER TABLE sys_report_exports DROP CONSTRAINT formal_export_payloads,
    ADD COLUMN preview_purged_at TIMESTAMPTZ,
    ADD CONSTRAINT formal_export_payloads CHECK (
        (generation_request='' AND generation_preview='' AND generation_member_version=0 AND preview_purged_at IS NULL)
        OR (generation_request<>'' AND generation_member_version>0 AND encrypted_payload='' AND
            (generation_preview='')=(preview_purged_at IS NOT NULL) AND
            (preview_purged_at IS NULL OR preview_purged_at>=expires_at)));
CREATE INDEX report_export_preview_expiry ON sys_report_exports(expires_at,id)
    WHERE encrypted_payload<>'' OR generation_preview<>'';

DO $migration$
DECLARE payload_constraint TEXT;
BEGIN
    SELECT conname INTO STRICT payload_constraint FROM pg_constraint
    WHERE conrelid='sys_report_team_deliveries'::regclass AND contype='c'
      AND pg_get_constraintdef(oid) LIKE '%encrypted_snapshot%';
    EXECUTE format('ALTER TABLE sys_report_team_deliveries DROP CONSTRAINT %I',payload_constraint);
END $migration$;

ALTER TABLE sys_report_team_deliveries ADD COLUMN payload_purged_at TIMESTAMPTZ,
    ADD CHECK ((status='succeeded')=(snapshot_sha256<>'' AND audience_stamp<>'' AND expires_at IS NOT NULL)),
    ADD CHECK (CASE WHEN status<>'succeeded' THEN encrypted_snapshot='' AND payload_purged_at IS NULL
        WHEN payload_purged_at IS NULL THEN encrypted_snapshot<>''
        ELSE encrypted_snapshot='' AND payload_purged_at>=expires_at END);
CREATE INDEX team_delivery_payload_expiry ON sys_report_team_deliveries(expires_at,id) WHERE encrypted_snapshot<>'';

ALTER TABLE sys_notifications ADD COLUMN link_expires_at TIMESTAMPTZ;
ALTER TABLE sys_metric_incident_events ADD COLUMN encrypted_observed_value TEXT NOT NULL DEFAULT '',
    ADD COLUMN observed_policy_stamp TEXT NOT NULL DEFAULT '';
ALTER TABLE sys_metric_incidents ADD COLUMN value_policy_stamp TEXT NOT NULL DEFAULT '';

CREATE FUNCTION dbhub_notify_delivery(wid BIGINT, uid BIGINT, event TEXT, heading TEXT, body TEXT, resource TEXT, rid BIGINT)
RETURNS VOID LANGUAGE SQL AS $$
    INSERT INTO sys_notifications(workspace_id,recipient_id,event_key,kind,title,message,resource_type,resource_id,
        link_expires_at)
    SELECT wid,uid,event,CASE WHEN resource='report_delivery' THEN 'report_subscription' ELSE 'report_delivery' END,
        heading,body,resource,rid,statement_timestamp()+interval '24 hours'
    WHERE EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
      JOIN sys_workspaces w ON w.id=m.workspace_id
      WHERE m.workspace_id=wid AND m.user_id=uid AND m.active AND u.is_active AND w.enabled)
    ON CONFLICT(workspace_id,recipient_id,event_key) DO NOTHING
$$;
