package db

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/iam"
)

// Configuration flags cannot designate a production database as an isolated
// one. The database owner must separately provision its empty-on-upgrade guard.
func (r *UserRepository) ConfigureIAMIsolation(id, fingerprint string) {
	r.iamIsolationID = id
	r.iamRouteFingerprint = fingerprint
}
func (r *UserRepository) iamIsolation(ctx context.Context, tx pgx.Tx) error {
	if r.iamIsolationID == "" || r.iamRouteFingerprint == "" {
		return iamDenied("真实权限切换尚未开放；仅允许登记过的隔离验收部署")
	}
	var ok bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_iam_isolation_guard WHERE isolation_id=$1 AND database_name=current_database())`, r.iamIsolationID).Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return iamDenied("当前数据库没有匹配的隔离验收登记")
	}
	return nil
}

// These flags describe which migration controls can be shown to an actor who
// still uses legacy permissions. The commands check the delegation again.
type iamIsolatedMigrationAccess struct {
	Prepare  bool `json:"prepare"`
	Review   bool `json:"review"`
	Rollback bool `json:"rollback"`
}

func (r *UserRepository) isolatedMigrationAccess(ctx context.Context) (iamIsolatedMigrationAccess, error) {
	var access iamIsolatedMigrationAccess
	if r.iamIsolationID == "" {
		return access, nil
	}
	tx, err := r.iamReadBegin(ctx, "space.assign.manage")
	if errors.Is(err, ErrManagedDenied) {
		return access, nil
	}
	if err != nil {
		return access, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = r.iamIsolation(ctx, tx); errors.Is(err, ErrManagedDenied) {
		return access, nil
	}
	if err != nil {
		return access, err
	}
	actor := ActorID(ctx)
	rows, err := tx.Query(ctx, `SELECT delegation_id::text,version FROM sys_iam_delegations
		WHERE workspace_id=$1 AND delegate_id=$2 AND revoked_at IS NULL AND valid_from<=now() AND valid_until>now()
		AND governance_actions && ARRAY['bundle.prepare','bundle.activate','bundle.rollback']`, WorkspaceID(ctx), actor)
	if err != nil {
		return access, err
	}
	candidates, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
		ID      string
		Version int64
	}])
	if err != nil {
		return access, err
	}
	for _, candidate := range candidates {
		delegation, err := iamDelegationTx(ctx, tx, candidate.ID, candidate.Version, actor)
		if errors.Is(err, ErrManagedDenied) {
			continue
		}
		if err != nil {
			return access, err
		}
		canReview := slices.Contains(delegation.Actions, "bundle.activate")
		access.Prepare = access.Prepare || (canReview && slices.Contains(delegation.Actions, "bundle.prepare"))
		access.Review = access.Review || canReview
		access.Rollback = access.Rollback || slices.Contains(delegation.Actions, "bundle.rollback")
	}
	return access, nil
}

func (r *UserRepository) iamFleet(ctx context.Context, tx pgx.Tx) error {
	var ok bool
	e := tx.QueryRow(ctx, `SELECT (SELECT count(DISTINCT kind) FROM sys_iam_runtime_agents WHERE workspace_id=$1 AND bridge_version=$2 AND route_fingerprint=$3 AND heartbeat_at>now()-interval '2 minutes')=3
 AND NOT EXISTS(SELECT 1 FROM sys_iam_runtime_agents WHERE workspace_id=$1 AND heartbeat_at>now()-interval '2 minutes' AND (bridge_version<>$2 OR route_fingerprint<>$3))
 AND NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND usename LIKE 'dbhub_runtime_%' AND has_schema_privilege(usename,current_schema(),'USAGE') AND application_name NOT LIKE 'dbhub-iam:phase1-bridge-2:%%')`, WorkspaceID(ctx), iam.RuntimeBridge, r.iamRouteFingerprint).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return iamDenied("API、数据库或任务执行器未通过同一版本检查；不能切换或回退")
	}
	return nil
}

type IAMActionDifference struct {
	Action string `json:"action"`
	Legacy bool   `json:"legacy"`
	Target bool   `json:"target"`
	Reason string `json:"reason,omitempty"`
}
type IAMShadowPrincipal struct {
	UserID                int64                 `json:"user_id"`
	Username              string                `json:"username"`
	MemberVersion         int64                 `json:"membership_version"`
	DataRole              string                `json:"legacy_data_role"`
	Capabilities          []string              `json:"legacy_capabilities"`
	Actions               []string              `json:"actions"`
	Differences           []IAMActionDifference `json:"differences"`
	ConstraintFingerprint string                `json:"constraint_fingerprint"`
	Unknown               int                   `json:"unknown"`
}
type IAMShadowResult struct {
	Baseline                IAMBaseline          `json:"baseline"`
	Principals              []IAMShadowPrincipal `json:"principals"`
	Fingerprint             string               `json:"fingerprint"`
	ReadOnly                bool                 `json:"read_only"`
	TargetConnectionsOpened int                  `json:"target_connections_opened"`
}
type IAMShadowCommand struct {
	UserIDs []int64 `json:"user_ids"`
}
type IAMBundleCommand struct {
	RequestID         string                          `json:"request_id"`
	Baseline          IAMBaseline                     `json:"baseline"`
	DelegationID      string                          `json:"delegation_id"`
	DelegationVersion int64                           `json:"delegation_version"`
	UserIDs           []int64                         `json:"user_ids"`
	ShadowFingerprint string                          `json:"shadow_fingerprint"`
	Differences       map[int64][]IAMActionDifference `json:"differences"`
	ValidUntil        time.Time                       `json:"valid_until"`
}
type iamBundleManifest struct {
	Shadow               IAMShadowResult                 `json:"shadow"`
	Differences          map[int64][]IAMActionDifference `json:"approved_differences"`
	ValidUntil           time.Time                       `json:"valid_until"`
	Bridge               string                          `json:"minimum_bridge"`
	RouteFingerprint     string                          `json:"route_fingerprint"`
	UnknownPrincipalMode string                          `json:"unknown_principal_mode"`
}

// This read set contains only metadata IDs, versions and policy constraints. No
// credentials, result rows, export contents or target SQL execution are read.
const iamConstraintSQL = `SELECT jsonb_build_object(
 'members',(SELECT COALESCE(jsonb_agg(jsonb_build_array(user_id,role_code,capabilities,active,version,expires_at) ORDER BY user_id),'[]') FROM sys_workspace_members WHERE workspace_id=$1),
 'connections',(SELECT COALESCE(jsonb_agg(jsonb_build_array(id,user_id,definition_revision_id,updated_at) ORDER BY id),'[]') FROM sys_connections WHERE workspace_id=$1 AND user_id=$2),
 'policies',(SELECT COALESCE(jsonb_agg(jsonb_build_array(p.id,p.connection_id,p.role_code,p.database_name,p.schema_name,p.table_name,p.column_permissions,p.row_conditions,p.updated_at) ORDER BY p.id),'[]') FROM sys_query_policies p JOIN sys_connections c ON c.id=p.connection_id AND c.workspace_id=p.workspace_id WHERE p.workspace_id=$1 AND c.user_id=$2),
 'source_versions',(SELECT COALESCE(jsonb_agg(jsonb_build_array(family_id,access_mode,access_version,profile_version) ORDER BY family_id),'[]') FROM sys_source_families WHERE workspace_id=$1),
 'source_grants',(SELECT COALESCE(jsonb_agg(jsonb_build_array(family_id,user_id,group_id) ORDER BY family_id,user_id,group_id),'[]') FROM sys_source_grants WHERE workspace_id=$1),
 'accounts',(SELECT COALESCE(jsonb_agg(jsonb_build_array(id,definition_revision_id,version,credential_generation,enabled) ORDER BY id),'[]') FROM sys_report_accounts WHERE workspace_id=$1),
 'reports',(SELECT COALESCE(jsonb_agg(jsonb_build_array(id,owner_id,definition_revision_id,publication_no,active_release_id,draft_version) ORDER BY id),'[]') FROM sys_managed_reports WHERE workspace_id=$1),
 'report_contracts',(SELECT COALESCE(jsonb_agg(jsonb_build_array(id,status,account_id,manifest_hash,validation_id) ORDER BY id),'[]') FROM sys_report_reviews WHERE workspace_id=$1),
 'dashboards',(SELECT COALESCE(jsonb_agg(jsonb_build_array(id,owner_id,publication_no,active_release_id,draft_version,lifecycle,lifecycle_epoch) ORDER BY id),'[]') FROM sys_managed_dashboards WHERE workspace_id=$1),
 'groups',(SELECT COALESCE(jsonb_agg(jsonb_build_array(group_id,user_id) ORDER BY group_id,user_id),'[]') FROM sys_user_group_members WHERE workspace_id=$1),
 'assignments',(SELECT COALESCE(jsonb_agg(jsonb_build_array(assignment_id,version,role_id,role_revision,approved_members,valid_until,revoked_at) ORDER BY assignment_id),'[]') FROM sys_iam_assignments WHERE workspace_id=$1 AND approved_members @> ARRAY[$2::bigint]),
 'role_versions',(SELECT COALESCE(jsonb_agg(jsonb_build_array(role_id,revision,version,enabled) ORDER BY role_id),'[]') FROM sys_iam_role_heads WHERE workspace_id=$1),
 'no_policy_behavior','original_database_account_ceiling','null_masking','original_policy_semantics','business_workflow','legacy-frozen-v1')`

func iamShadowTx(ctx context.Context, tx pgx.Tx, ids []int64) (IAMShadowResult, error) {
	out := IAMShadowResult{ReadOnly: true, Principals: []IAMShadowPrincipal{}}
	if len(ids) == 0 || len(ids) > 500 || !unique(ids) {
		return out, iamInput("请选择不重复的有效主体，最多500人")
	}
	if e := tx.QueryRow(ctx, `SELECT config_version,authorization_epoch FROM sys_iam_workspace_state WHERE workspace_id=$1`, WorkspaceID(ctx)).Scan(&out.Baseline.Config, &out.Baseline.Epoch); e != nil {
		return out, e
	}
	sorted := append([]int64{}, ids...)
	slices.Sort(sorted)
	for _, id := range sorted {
		p := IAMShadowPrincipal{UserID: id, Actions: []string{}, Differences: []IAMActionDifference{}}
		e := tx.QueryRow(ctx, `SELECT u.username,m.version,dbhub_member_role(m.user_id),m.capabilities FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND dbhub_iam_qualified(m.user_id)`, WorkspaceID(ctx), id).Scan(&p.Username, &p.MemberVersion, &p.DataRole, &p.Capabilities)
		if e != nil {
			return out, iamDenied("所选主体已退出、冻结、到期或不属于此工作空间")
		}
		var constraints []byte
		if e = tx.QueryRow(ctx, iamConstraintSQL, WorkspaceID(ctx), id).Scan(&constraints); e != nil {
			return out, e
		}
		p.ConstraintFingerprint = reportDigest(string(constraints))
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_iam_legacy_envelopes WHERE workspace_id=$1 AND user_id=$2 AND inventory_status<>'confirmed'`, WorkspaceID(ctx), id).Scan(&p.Unknown); e != nil {
			return out, e
		}
		rows, e := tx.Query(ctx, `SELECT c.action_id, c.implementation_state='legacy' AND (m.role_code=ANY(c.legacy_roles) OR m.capabilities&&c.legacy_capabilities) AND NOT dbhub_iam_action_denied(m.user_id,c.action_id),dbhub_iam_shadow_action(m.user_id,c.action_id) FROM sys_iam_capabilities c JOIN sys_workspace_members m ON m.workspace_id=$1 AND m.user_id=$2 WHERE c.scope='workspace' ORDER BY c.action_id`, WorkspaceID(ctx), id)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var d IAMActionDifference
			if e = rows.Scan(&d.Action, &d.Legacy, &d.Target); e != nil {
				rows.Close()
				return out, e
			}
			if d.Target {
				p.Actions = append(p.Actions, d.Action)
			}
			if d.Legacy != d.Target {
				p.Differences = append(p.Differences, d)
			}
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return out, e
		}
		out.Principals = append(out.Principals, p)
	}
	out.Fingerprint = reportDigest(string(canonicalReportJSON(out)))
	return out, nil
}

func (r *UserRepository) IAMShadow(ctx context.Context, in IAMShadowCommand) (IAMShadowResult, error) {
	tx, e := r.pool.maintenance.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return IAMShadowResult{}, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = iamScope(ctx, tx); e != nil {
		return IAMShadowResult{}, e
	}
	if e = iamCurrent(ctx, tx); e != nil {
		return IAMShadowResult{}, e
	}
	var ok bool
	e = tx.QueryRow(ctx, `SELECT CASE WHEN dbhub_iam_is_target(dbhub_actor()) THEN dbhub_iam_has_action(dbhub_actor(),'space.assign.view') ELSE dbhub_member_role(dbhub_actor())='admin' END`).Scan(&ok)
	if e != nil {
		return IAMShadowResult{}, e
	}
	if !ok {
		return IAMShadowResult{}, ErrManagedDenied
	}
	return iamShadowTx(ctx, tx, in.UserIDs)
}
func (r *UserRepository) IAMPrepareBundle(ctx context.Context, in IAMBundleCommand) (json.RawMessage, error) {
	tx, e := r.iamBegin(ctx, "space.assign.manage")
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	previous, hash, e := iamReceipt(ctx, tx, in.RequestID, "bundle.prepare", in)
	if e != nil || previous != nil {
		return previous, e
	}
	if e = iamBaselineTx(ctx, tx, in.Baseline); e != nil {
		return nil, e
	}
	if e = r.iamIsolation(ctx, tx); e != nil {
		return nil, e
	}
	shadow, e := iamShadowTx(ctx, tx, in.UserIDs)
	if e != nil {
		return nil, e
	}
	if shadow.Fingerprint != in.ShadowFingerprint {
		return nil, iamConflict("只读对照已变化，请重新比较")
	}
	if !in.ValidUntil.After(time.Now()) || in.ValidUntil.After(time.Now().Add(30*24*time.Hour)) {
		return nil, iamInput("验收配置期限须在未来30天内")
	}
	actions := []string{}
	for _, p := range shadow.Principals {
		if p.Unknown > 0 {
			return nil, iamDenied("存在未确认的数据约束，不能纳入启用包")
		}
		ack := append([]IAMActionDifference{}, in.Differences[p.UserID]...)
		sort.Slice(ack, func(i, j int) bool { return ack[i].Action < ack[j].Action })
		if len(ack) != len(p.Differences) {
			return nil, iamInput("每项扩权和收窄都须明确确认原因")
		}
		for i, d := range p.Differences {
			a := ack[i]
			if a.Action != d.Action || a.Legacy != d.Legacy || a.Target != d.Target || len([]rune(strings.TrimSpace(a.Reason))) < 4 || len([]rune(a.Reason)) > 500 {
				return nil, iamInput("差异及确认原因与当前对照不一致")
			}
		}
		for _, a := range p.Actions {
			if !slices.Contains(actions, a) {
				actions = append(actions, a)
			}
		}
	}
	var missing int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_iam_principal_modes WHERE workspace_id=$1 AND mode='TARGET' AND NOT(user_id=ANY($2::bigint[]))`, WorkspaceID(ctx), in.UserIDs).Scan(&missing); e != nil {
		return nil, e
	}
	if missing > 0 {
		return nil, iamDenied("已采用目标方案的主体必须明确列入完整配置包；退出目标方案请执行受控回退")
	}
	d, e := iamDelegationTx(ctx, tx, in.DelegationID, in.DelegationVersion, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	if e = iamCeiling(d, "bundle.prepare", actions, in.UserIDs, nil, in.ValidUntil); e != nil {
		return nil, e
	}
	manifest := iamBundleManifest{Shadow: shadow, Differences: in.Differences, ValidUntil: in.ValidUntil, Bridge: iam.RuntimeBridge, RouteFingerprint: r.iamRouteFingerprint, UnknownPrincipalMode: iam.LegacyGuarded}
	id := dashboardUUID()
	_, e = tx.Exec(ctx, `INSERT INTO sys_iam_bundles(workspace_id,bundle_id,config_version,minimum_bridge,manifest,state,created_by) SELECT $1,$2::uuid,GREATEST(s.config_version,COALESCE((SELECT max(config_version) FROM sys_iam_bundles WHERE workspace_id=$1),0))+1,$3,$4::jsonb,'shadow',$5 FROM sys_iam_workspace_state s WHERE workspace_id=$1`, WorkspaceID(ctx), id, iam.RuntimeBridge, string(canonicalReportJSON(manifest)), ActorID(ctx))
	if e != nil {
		return nil, e
	}
	return iamCommit(ctx, tx, in.RequestID, "bundle.prepare", hash, map[string]any{"id": id, "state": "shadow", "manifest": manifest, "activation_applied": false})
}

func iamBundle(ctx context.Context, tx pgx.Tx, id string) (iamBundleManifest, error) {
	var out iamBundleManifest
	var raw []byte
	if !iamUUID.MatchString(id) {
		return out, iamInput("配置包编号无效")
	}
	if e := tx.QueryRow(ctx, `SELECT manifest FROM sys_iam_bundles WHERE workspace_id=$1 AND bundle_id=$2::uuid`, WorkspaceID(ctx), id).Scan(&raw); e != nil {
		return out, managedError(e)
	}
	if e := json.Unmarshal(raw, &out); e != nil {
		return out, e
	}
	if out.Bridge != iam.RuntimeBridge || out.UnknownPrincipalMode != iam.LegacyGuarded || out.Shadow.Fingerprint == "" {
		return out, iamDenied("配置包不支持当前兼容运行版本")
	}
	return out, nil
}
func iamBundleImpact(ctx context.Context, tx pgx.Tx, p IAMChange) ([]int64, []string, error) {
	m, e := iamBundle(ctx, tx, p.BundleID)
	if e != nil {
		return nil, nil, e
	}
	if !m.ValidUntil.After(time.Now()) || !m.ValidUntil.Equal(p.ValidUntil) {
		return nil, nil, iamDenied("配置包已到期或期限发生变化")
	}
	ids, actions := []int64{}, []string{}
	for _, member := range m.Shadow.Principals {
		ids = append(ids, member.UserID)
		for _, a := range member.Actions {
			if !slices.Contains(actions, a) {
				actions = append(actions, a)
			}
		}
	}
	// Recusal covers every selected principal, even a deliberate narrowing.
	return ids, actions, nil
}
func (r *UserRepository) iamApplyBundle(ctx context.Context, tx pgx.Tx, p IAMChange) error {
	if e := r.iamIsolation(ctx, tx); e != nil {
		return e
	}
	if e := r.iamFleet(ctx, tx); e != nil {
		return e
	}
	m, e := iamBundle(ctx, tx, p.BundleID)
	if e != nil {
		return e
	}
	if m.RouteFingerprint != r.iamRouteFingerprint {
		return iamConflict("端点清单已变化，必须重新对照")
	}
	ids := []int64{}
	for _, member := range m.Shadow.Principals {
		ids = append(ids, member.UserID)
	}
	shadow, e := iamShadowTx(ctx, tx, ids)
	if e != nil {
		return e
	}
	if shadow.Fingerprint != m.Shadow.Fingerprint {
		return iamConflict("配置包对照基线发生变化，未启用任何主体")
	}
	for _, member := range m.Shadow.Principals {
		_, e = tx.Exec(ctx, `INSERT INTO sys_iam_principal_modes(workspace_id,user_id,mode,confirmed,legacy_data_role,legacy_capabilities,envelope_ref,valid_until,bundle_id)
 VALUES($1,$2,'TARGET',true,$3,$4,$5::jsonb,$6,$7::uuid) ON CONFLICT(workspace_id,user_id) DO UPDATE SET mode='TARGET',confirmed=true,legacy_data_role=EXCLUDED.legacy_data_role,legacy_capabilities=EXCLUDED.legacy_capabilities,envelope_ref=EXCLUDED.envelope_ref,valid_until=LEAST(COALESCE(sys_iam_principal_modes.valid_until,EXCLUDED.valid_until),EXCLUDED.valid_until),bundle_id=EXCLUDED.bundle_id,rollback_guard=false,version=sys_iam_principal_modes.version+1`, WorkspaceID(ctx), member.UserID, member.DataRole, member.Capabilities, string(canonicalReportJSON(map[string]any{"constraint_fingerprint": member.ConstraintFingerprint, "business_workflow": "legacy-frozen-v1"})), m.ValidUntil, p.BundleID)
		if e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, `UPDATE sys_iam_workspace_state SET mode='COMPAT_ACTIVE',activation_ready=true,active_bundle_id=$2::uuid,minimum_bridge=$3 WHERE workspace_id=$1`, WorkspaceID(ctx), p.BundleID, iam.RuntimeBridge)
	return e
}

type IAMRollbackCommand struct {
	RequestID         string      `json:"request_id"`
	Baseline          IAMBaseline `json:"baseline"`
	BundleID          string      `json:"bundle_id"`
	DelegationID      string      `json:"delegation_id"`
	DelegationVersion int64       `json:"delegation_version"`
	Reason            string      `json:"reason"`
}

func (r *UserRepository) IAMRollback(ctx context.Context, in IAMRollbackCommand) (json.RawMessage, error) {
	tx, e := r.iamBegin(ctx, "space.assign.manage")
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	previous, hash, e := iamReceipt(ctx, tx, in.RequestID, "bundle.rollback", in)
	if e != nil || previous != nil {
		return previous, e
	}
	if e = iamBaselineTx(ctx, tx, in.Baseline); e != nil {
		return nil, e
	}
	if e = r.iamIsolation(ctx, tx); e != nil {
		return nil, e
	}
	if e = r.iamFleet(ctx, tx); e != nil {
		return nil, e
	}
	if strings.TrimSpace(in.Reason) == "" || len([]rune(in.Reason)) > 1000 {
		return nil, iamInput("请填写回退原因")
	}
	var current string
	if e = tx.QueryRow(ctx, `SELECT COALESCE(active_bundle_id::text,'') FROM sys_iam_workspace_state WHERE workspace_id=$1`, WorkspaceID(ctx)).Scan(&current); e != nil {
		return nil, e
	}
	if current != in.BundleID || !iamUUID.MatchString(in.BundleID) {
		return nil, iamConflict("活动配置包已变化")
	}
	m, e := iamBundle(ctx, tx, in.BundleID)
	if e != nil {
		return nil, e
	}
	ids := []int64{}
	for _, p := range m.Shadow.Principals {
		ids = append(ids, p.UserID)
	}
	d, e := iamDelegationTx(ctx, tx, in.DelegationID, in.DelegationVersion, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	if e = iamCeiling(d, "bundle.rollback", nil, ids, nil, time.Now()); e != nil {
		return nil, e
	}
	suspended := 0
	for _, uid := range ids {
		// Persist every currently missing legacy action before changing the pointer.
		// Losing a role source, a temporary grant or an action cannot revive old admin.
		rows, e := tx.Query(ctx, `SELECT action_id FROM sys_iam_capabilities c WHERE c.scope='workspace' AND NOT dbhub_iam_has_action($1,c.action_id) ORDER BY action_id`, uid)
		if e != nil {
			return nil, e
		}
		deny := []string{}
		for rows.Next() {
			var a string
			if e = rows.Scan(&a); e != nil {
				rows.Close()
				return nil, e
			}
			deny = append(deny, a)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return nil, e
		}
		for _, a := range deny {
			if _, e = tx.Exec(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,action_id,reason,evidence) VALUES($1,$2,$3,$4::uuid,'action_revoke',$5,$6,$7::jsonb)`, WorkspaceID(ctx), ActorID(ctx), uid, dashboardUUID(), a, "配置回退保留当前授权上限："+in.Reason, string(canonicalReportJSON(map[string]any{"rollback_request_id": in.RequestID, "bundle_id": in.BundleID}))); e != nil {
				return nil, e
			}
		}
		// Tasks introduced under the target model remain evidence, never auto-retry.
		tag, e := tx.Exec(ctx, `UPDATE sys_source_tasks SET status='stale',phase='权限配置已回退，需重新确认' WHERE workspace_id=$1 AND actor_id=$2 AND permission_bridge=$3 AND status='queued'`, WorkspaceID(ctx), uid, iam.RuntimeBridge)
		if e != nil {
			return nil, e
		}
		suspended += int(tag.RowsAffected())
		if _, e = tx.Exec(ctx, `UPDATE sys_report_subscription_runs SET status='interrupted',message='权限配置已回退，需重新确认',finished_at=now() WHERE workspace_id=$1 AND user_id=$2 AND permission_bridge=$3 AND status IN('queued','running')`, WorkspaceID(ctx), uid, iam.RuntimeBridge); e != nil {
			return nil, e
		}
		if _, e = tx.Exec(ctx, `UPDATE sys_iam_principal_modes SET mode='LEGACY_GUARDED',rollback_guard=true,version=version+1 WHERE workspace_id=$1 AND user_id=$2`, WorkspaceID(ctx), uid); e != nil {
			return nil, e
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE sys_iam_workspace_state SET mode='LEGACY_GUARDED',activation_ready=false,active_bundle_id=NULL WHERE workspace_id=$1`, WorkspaceID(ctx)); e != nil {
		return nil, e
	}
	if e = iamBump(ctx, tx); e != nil {
		return nil, e
	}
	return iamCommit(ctx, tx, in.RequestID, "bundle.rollback", hash, map[string]any{"state": iam.LegacyGuarded, "prior_bundle_id": in.BundleID, "frozen_and_revoked_preserved": true, "original_files_and_receipts_preserved": true, "new_tasks_suspended": suspended})
}
