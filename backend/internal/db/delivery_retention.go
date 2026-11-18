package db

import "context"

// PurgeDeliveryPayloads leaves approval, checksum, expiry and receipt metadata
// intact. Each transaction handles a bounded batch and can safely be retried.
func (r *ReportSubscriptionRepository) PurgeDeliveryPayloads(ctx context.Context) error {
	if r.reports.phase2ReportIsolation == "" {
		return nil
	}
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var isolated bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_iam_isolation_guard
		WHERE isolation_id=$1 AND database_name=current_database())`, r.reports.phase2ReportIsolation).Scan(&isolated); err != nil || !isolated {
		return err
	}
	for _, statement := range []string{
		`WITH due AS (SELECT digest FROM sys_dashboard_delivery_credentials WHERE expires_at<=statement_timestamp()
			ORDER BY expires_at,digest FOR UPDATE SKIP LOCKED LIMIT 200)
		 DELETE FROM sys_dashboard_delivery_credentials c USING due WHERE c.digest=due.digest`,
		`WITH due AS (SELECT id FROM sys_dashboard_deliveries WHERE expires_at<=statement_timestamp()
			AND payload<>'' ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT 200)
		 UPDATE sys_dashboard_deliveries d SET payload='',payload_purged_at=statement_timestamp() FROM due WHERE d.id=due.id`,
		`WITH due AS (SELECT digest FROM sys_report_export_credentials WHERE expires_at<=statement_timestamp()
			ORDER BY expires_at,digest FOR UPDATE SKIP LOCKED LIMIT 200)
		 DELETE FROM sys_report_export_credentials c USING due WHERE c.digest=due.digest`,
		`WITH due AS (SELECT workspace_id,export_id FROM sys_report_export_files WHERE expires_at<=statement_timestamp()
			AND payload_purged_at IS NULL ORDER BY expires_at,workspace_id,export_id FOR UPDATE SKIP LOCKED LIMIT 200)
		 UPDATE sys_report_export_files f SET encrypted_file='',payload_purged_at=statement_timestamp()
		 FROM due WHERE f.workspace_id=due.workspace_id AND f.export_id=due.export_id`,
		`WITH due AS (SELECT id FROM sys_report_exports WHERE expires_at<=statement_timestamp()
			AND (encrypted_payload<>'' OR generation_preview<>'') ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT 200)
		 UPDATE sys_report_exports e SET encrypted_payload='',generation_preview='',
		 preview_purged_at=CASE WHEN generation_preview<>'' THEN statement_timestamp() ELSE preview_purged_at END
		 FROM due WHERE e.id=due.id`,
		`WITH due AS (SELECT id FROM sys_report_team_deliveries WHERE expires_at<=statement_timestamp()
			AND encrypted_snapshot<>'' ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT 200)
		 UPDATE sys_report_team_deliveries d SET encrypted_snapshot='',payload_purged_at=statement_timestamp()
		 FROM due WHERE d.id=due.id`,
	} {
		if _, err = tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
