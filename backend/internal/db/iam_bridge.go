package db

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/user/dbhub/internal/iam"
	"os"
)

func (r *UserRepository) IAMHeartbeat(ctx context.Context, kind, fingerprint string) error {
	if fingerprint == "" || (kind != "api" && kind != "worker" && kind != "database") {
		return ErrManagedInput
	}
	if kind == "database" {
		var valid bool
		e := r.pool.maintenance.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version='0062_iam_disabled_future_actions.sql') AND NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1 AND (rolsuper OR rolbypassrls)) AND NOT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE 'sys_iam_%' AND has_table_privilege($1,quote_ident(schemaname)||'.'||quote_ident(tablename),'INSERT,UPDATE,DELETE'))`, r.pool.role).Scan(&valid)
		if e != nil {
			return e
		}
		if !valid {
			return iamDenied("数据库兼容运行桥尚未通过自检")
		}
	}
	host, _ := os.Hostname()
	agent := fmt.Sprintf("%s:%s:%d", kind, host, os.Getpid())
	_, e := r.pool.maintenance.Exec(ctx, `INSERT INTO sys_iam_runtime_agents(workspace_id,agent_id,kind,bridge_version,route_fingerprint,heartbeat_at)
 SELECT workspace_id,$1,$2,$3,$4,now() FROM sys_iam_workspace_state ON CONFLICT(workspace_id,agent_id) DO UPDATE SET bridge_version=EXCLUDED.bridge_version,route_fingerprint=EXCLUDED.route_fingerprint,heartbeat_at=EXCLUDED.heartbeat_at`, agent, kind, iam.RuntimeBridge, fingerprint)
	return e
}

func (r *UserRepository) IAMBridgeStatus(ctx context.Context) (any, error) {
	tx, e := r.iamReadBegin(ctx, "space.assign.view")
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT jsonb_build_object('bridge','phase1-bridge-2','live_agents',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',agent_id,'kind',kind,'version',bridge_version,'route_fingerprint',route_fingerprint,'heartbeat_at',heartbeat_at,'current',heartbeat_at>now()-interval '2 minutes')) FROM sys_iam_runtime_agents WHERE workspace_id=$1),'[]'),'unbridged_runtime_connections',(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename LIKE 'dbhub_runtime_%' AND has_schema_privilege(usename,current_schema(),'USAGE') AND application_name NOT LIKE 'dbhub-iam:phase1-bridge-2:%%'),'business_data_touched',false)`, WorkspaceID(ctx)).Scan(&raw)
	if e != nil {
		return nil, e
	}
	var out map[string]any
	if e = json.Unmarshal(raw, &out); e != nil {
		return nil, e
	}
	out["isolated_activation_enabled"] = r.iamIsolation(ctx, tx) == nil
	return out, nil
}
