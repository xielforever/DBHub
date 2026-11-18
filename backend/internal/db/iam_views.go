package db

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

type IAMMenuItem struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Group   string `json:"group"`
	Order   int    `json:"order"`
	Visible bool   `json:"visible"`
}
type IAMMenus struct {
	Version   int64         `json:"version"`
	Items     []IAMMenuItem `json:"items"`
	Preferred string        `json:"preferred_menu"`
}
type IAMMenuCommand struct {
	RequestID     string      `json:"request_id"`
	Baseline      IAMBaseline `json:"baseline"`
	Configuration IAMMenus    `json:"configuration"`
}

func (r *UserRepository) IAMRead(ctx context.Context, section string) (json.RawMessage, error) {
	type view struct{ cap, query string }
	views := map[string]view{
		"roles":        {"space.roles.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',h.role_id,'name',h.display_name,'version',h.version,'revision',h.revision,'enabled',h.enabled,'actions',r.actions,'proposal_id',r.proposal_id,'assigned_count',(SELECT count(*) FROM sys_iam_assignments a WHERE a.workspace_id=h.workspace_id AND a.role_id=h.role_id AND a.revoked_at IS NULL AND a.valid_until>now())) ORDER BY h.display_name),'[]') FROM sys_iam_role_heads h JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision WHERE h.workspace_id=$1`},
		"assignments":  {"space.assign.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',a.assignment_id,'version',a.version,'role_id',a.role_id,'role_revision',a.role_revision,'role_name',h.display_name,'subject_type',a.subject_type,'subject_id',a.subject_id,'approved_members',a.approved_members,'valid_until',a.valid_until,'revoked_at',a.revoked_at,'proposal_id',a.proposal_id) ORDER BY a.valid_from DESC),'[]') FROM sys_iam_assignments a JOIN sys_iam_role_heads h USING(workspace_id,role_id) WHERE a.workspace_id=$1`},
		"delegations":  {"space.assign.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',delegation_id,'version',version,'issuer_id',issuer_id,'delegate_id',delegate_id,'governance_actions',governance_actions,'grantable_actions',grantable_actions,'target_ids',target_ids,'source_ids',source_ids,'valid_until',valid_until,'revoked_at',revoked_at,'parent_id',parent_id) ORDER BY valid_from DESC),'[]') FROM sys_iam_delegations WHERE workspace_id=$1`},
		"restrictions": {"space.assign.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('sequence',v.sequence,'subject_id',v.target_id,'username',u.username,'action_id',v.action_id,'reason',v.reason,'recorded_at',v.recorded_at,'release_until',x.valid_until,'release_proposal_id',x.proposal_id,'release_history',COALESCE((SELECT jsonb_agg(jsonb_build_object('proposal_id',r.proposal_id,'valid_until',r.valid_until,'requester_id',p.requester_id,'reviewer_id',p.reviewer_id) ORDER BY p.reviewed_at DESC) FROM sys_iam_action_releases r JOIN sys_iam_proposals p USING(workspace_id,proposal_id) WHERE r.workspace_id=v.workspace_id AND r.user_id=v.target_id AND r.action_id=v.action_id AND r.revocation_sequence=v.sequence AND p.state='applied'),'[]')) ORDER BY v.sequence DESC),'[]') FROM (SELECT DISTINCT ON(target_id,action_id) * FROM sys_iam_revocations WHERE workspace_id=$1 AND kind='action_revoke' ORDER BY target_id,action_id,sequence DESC)v JOIN sys_users u ON u.id=v.target_id LEFT JOIN LATERAL(SELECT r.valid_until,r.proposal_id FROM sys_iam_action_releases r JOIN sys_iam_proposals p USING(workspace_id,proposal_id) WHERE r.workspace_id=v.workspace_id AND r.user_id=v.target_id AND r.action_id=v.action_id AND r.revocation_sequence>=v.sequence AND r.valid_until>statement_timestamp() AND p.state='applied' ORDER BY r.valid_until DESC LIMIT 1)x ON true WHERE EXISTS(SELECT 1 FROM sys_iam_delegations d WHERE d.workspace_id=$1 AND d.delegate_id=$2 AND d.target_ids @> ARRAY[v.target_id] AND d.grantable_actions @> ARRAY[v.action_id] AND d.governance_actions && ARRAY['assignment.propose','assignment.review'] AND d.revoked_at IS NULL AND d.valid_from<=statement_timestamp() AND d.valid_until>statement_timestamp() AND dbhub_iam_qualified(d.issuer_id))`},
		"proposals":    {"space.assign.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',proposal_id,'requester_id',requester_id,'beneficiaries',beneficiary_ids,'baseline',jsonb_build_object('config_version',baseline_config,'authorization_epoch',baseline_authorization),'patch',patch,'state',CASE WHEN state='pending' AND valid_until<=now() THEN 'expired' ELSE state END,'version',version,'reviewer_id',reviewer_id,'valid_until',valid_until,'created_at',created_at) ORDER BY created_at DESC),'[]') FROM sys_iam_proposals WHERE workspace_id=$1`},
		"members":      {"space.members.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',u.id,'username',u.username,'role',m.role_code,'active',m.active AND u.is_active,'version',m.version,'expires_at',m.expires_at,'frozen',EXISTS(SELECT 1 FROM sys_iam_revocations v WHERE v.workspace_id=m.workspace_id AND v.target_id=u.id AND v.kind='member_freeze'),'principal_mode',COALESCE(p.mode,'LEGACY_GUARDED')) ORDER BY u.username),'[]') FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id LEFT JOIN sys_iam_principal_modes p USING(workspace_id,user_id) WHERE m.workspace_id=$1`},
		"groups":       {"space.assign.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',g.id,'name',g.name,'version',COALESCE(v.version,1),'member_ids',ARRAY(SELECT user_id FROM sys_user_group_members m WHERE m.workspace_id=g.workspace_id AND m.group_id=g.id ORDER BY user_id)) ORDER BY g.name),'[]') FROM sys_user_groups g LEFT JOIN sys_iam_group_versions v ON v.workspace_id=g.workspace_id AND v.group_id=g.id WHERE g.workspace_id=$1`},
		"resources":    {"space.resources.view", `SELECT jsonb_build_object('editable',false,'reason','阶段1保留原资源授权、数据范围及审批签名；新增数据范围在后续阶段实施','sources',(SELECT count(*) FROM sys_source_definitions WHERE workspace_id=$1),'reports',(SELECT count(*) FROM sys_managed_reports WHERE workspace_id=$1),'dashboards',(SELECT count(*) FROM sys_managed_dashboards WHERE workspace_id=$1),'sensitive_reveal_enabled',false,'team_delivery_enabled',false)`},
		"policy":       {"space.policy.view", `SELECT jsonb_build_object('version','legacy-frozen-v1','editable',false,'reason','报表和仪表盘继续执行原版本的验证、独立审批与发布流程；当前阶段不重签在途任务','allow_unknown_freshness',allow_unknown_freshness,'new_export_expiry_enabled',false,'dashboard_export_enabled',false,'team_subscription_enabled',false) FROM sys_workspaces WHERE id=$1`},
		"custody":      {"space.members.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('user_id',user_id,'freeze_sequence',freeze_sequence,'assets',assets,'state',state,'created_at',created_at) ORDER BY created_at DESC),'[]') FROM sys_iam_custody WHERE workspace_id=$1`},
		"migration":    {"space.assign.view", `SELECT jsonb_build_object('state',s.mode,'config_version',s.config_version,'authorization_epoch',s.authorization_epoch,'minimum_bridge',s.minimum_bridge,'active_bundle_id',s.active_bundle_id,'principals',COALESCE((SELECT jsonb_agg(jsonb_build_object('user_id',p.user_id,'mode',p.mode,'confirmed',p.confirmed,'version',p.version,'legacy_data_role',p.legacy_data_role,'valid_until',p.valid_until)) FROM sys_iam_principal_modes p WHERE p.workspace_id=s.workspace_id),'[]'),'inventory_unknown_count',(SELECT count(*) FROM sys_iam_legacy_envelopes e WHERE e.workspace_id=s.workspace_id AND e.inventory_status='unknown')) FROM sys_iam_workspace_state s WHERE s.workspace_id=$1`},
	}
	if section == "own-proposals" {
		views[section] = views["proposals"]
	}
	views["bundles"] = view{"space.assign.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',bundle_id,'manifest',manifest,'created_at',created_at,'created_by',created_by) ORDER BY created_at DESC),'[]') FROM sys_iam_bundles WHERE workspace_id=$1`}
	// Choosers expose only the objects permitted by a current finite delegation.
	// Assignment management does not imply a full member or role directory.
	views["assignment-targets"] = view{"space.assign.view", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',u.id,'username',u.username,'active',dbhub_iam_qualified(u.id),'frozen',false,'version',m.version,'principal_mode',COALESCE(p.mode,'LEGACY_GUARDED')) ORDER BY u.username),'[]') FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id LEFT JOIN sys_iam_principal_modes p USING(workspace_id,user_id) WHERE m.workspace_id=$1 AND EXISTS(SELECT 1 FROM sys_iam_delegations d WHERE d.workspace_id=$1 AND d.delegate_id=$2 AND d.target_ids @> ARRAY[u.id] AND d.revoked_at IS NULL AND d.valid_from<=now() AND d.valid_until>now() AND dbhub_iam_qualified(d.issuer_id))`}
	views["assignable-roles"] = view{"space.assign.manage", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',h.role_id,'name',h.display_name,'version',h.version,'revision',h.revision,'enabled',h.enabled,'actions',r.actions,'assigned_count',0) ORDER BY h.display_name),'[]') FROM sys_iam_role_heads h JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision WHERE h.workspace_id=$1 AND h.enabled AND EXISTS(SELECT 1 FROM sys_iam_delegations d WHERE d.workspace_id=$1 AND d.delegate_id=$2 AND r.actions <@ d.grantable_actions AND d.revoked_at IS NULL AND d.valid_from<=now() AND d.valid_until>now() AND dbhub_iam_qualified(d.issuer_id))`}
	v, ok := views[section]
	if !ok {
		return nil, ErrManagedInput
	}
	args := []any{WorkspaceID(ctx)}
	if section == "assignment-targets" || section == "assignable-roles" || section == "restrictions" {
		args = append(args, ActorID(ctx))
	}
	if section == "proposals" || section == "own-proposals" {
		v.cap = ""
		kinds := []string{}
		c, _ := auth.ClaimsFromContext(ctx)
		if section != "own-proposals" && c != nil {
			if c.IAM != nil && c.IAM.Target {
				if auth.HasAction(c, "space.roles.manage") {
					kinds = append(kinds, "role_upsert")
				}
				if auth.HasAction(c, "space.assign.manage") {
					kinds = append(kinds, "assignment_add", "group_members_add", "delegation_add", "bundle_activate", "action_release")
				}
			} else if c.Role == "admin" {
				kinds = []string{"role_upsert", "assignment_add", "group_members_add", "delegation_add", "bundle_activate", "action_release"}
			}
		}
		v.query += ` AND (requester_id=$2 OR $2=ANY(beneficiary_ids) OR patch->>'kind'=ANY($3::text[]))`
		args = append(args, ActorID(ctx), kinds)
	}
	tx, e := r.iamReadBegin(ctx, v.cap)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var raw []byte
	e = tx.QueryRow(ctx, v.query, args...).Scan(&raw)
	if e == nil && section == "delegations" {
		c, _ := auth.ClaimsFromContext(ctx)
		if c != nil && c.IAM != nil && c.IAM.GovernanceOnly && len(c.IAM.Sources) > 0 {
			d, found, err := iamRecoveryDelegationTx(ctx, tx, c.IAM.Sources[0].ID, 1, ActorID(ctx))
			if err != nil {
				return nil, err
			}
			if found {
				var items []map[string]any
				if err = json.Unmarshal(raw, &items); err != nil {
					return nil, err
				}
				items = append(items, map[string]any{"id": d.ID, "version": 1, "issuer_id": 0, "delegate_id": d.Delegate, "governance_actions": d.Actions, "grantable_actions": d.Grantable, "target_ids": d.Targets, "source_ids": d.Sources, "valid_until": d.Until, "recovery_only": true})
				raw = canonicalReportJSON(items)
			}
		}
	}
	return raw, e
}

func (r *UserRepository) IAMMenuConfiguration(ctx context.Context) (IAMMenus, error) {
	out := IAMMenus{Version: 0, Items: []IAMMenuItem{}}
	var raw []byte
	e := r.pool.maintenance.QueryRow(ctx, `SELECT version,items,COALESCE(preferred_menu,'') FROM sys_iam_menu_configuration WHERE workspace_id=$1`, WorkspaceID(ctx)).Scan(&out.Version, &raw, &out.Preferred)
	if e != nil && e.Error() != "no rows in result set" {
		return out, e
	}
	if len(raw) > 0 {
		if e = json.Unmarshal(raw, &out.Items); e != nil {
			return out, e
		}
	}
	if out.Version == 0 {
		for i, m := range iam.ReadCatalog().Menus {
			out.Items = append(out.Items, IAMMenuItem{ID: m.ID, Label: m.Label, Group: m.Group, Order: i, Visible: true})
		}
	}
	return out, nil
}
func (r *UserRepository) IAMEffective(ctx context.Context) (any, error) {
	c, _ := auth.ClaimsFromContext(ctx)
	if c == nil || c.IAM == nil {
		return nil, ErrManagedDenied
	}
	if e := r.IAMCurrent(ctx); e != nil {
		return nil, e
	}
	menus, e := r.IAMMenuConfiguration(ctx)
	if e != nil {
		return nil, e
	}
	var home string
	e = r.pool.maintenance.QueryRow(ctx, `SELECT menu_id FROM sys_iam_home_preferences WHERE workspace_id=$1 AND user_id=$2`, WorkspaceID(ctx), ActorID(ctx)).Scan(&home)
	if e != nil && e.Error() != "no rows in result set" {
		return nil, e
	}
	catalog := iam.ReadCatalog()
	actions := []string{}
	decisions := []iam.Decision{}
	for _, cap := range catalog.Capabilities {
		if cap.Scope != "workspace" {
			continue
		}
		d := iam.ExplainLegacy(*c.IAM, cap.ID, time.Now())
		if c.IAM.Target {
			d.Allowed = auth.HasAction(c, cap.ID)
			d.Sources = []string{}
			for _, s := range c.IAM.Sources {
				if slices.Contains(s.Actions, cap.ID) {
					d.Sources = append(d.Sources, s.ID)
				}
			}
			if d.Allowed {
				d.Reason = "current_role_sources"
			} else {
				d.Reason = "no_current_grant_or_dependency"
			}
		}
		if d.Allowed {
			actions = append(actions, cap.ID)
		}
		decisions = append(decisions, d)
	}
	var enabled []string
	rows, e := r.pool.maintenance.Query(ctx, `SELECT action_id FROM sys_iam_capabilities WHERE runtime_enabled ORDER BY action_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		enabled = append(enabled, id)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	out := map[string]any{"snapshot": c.IAM, "actions": actions, "decisions": decisions, "catalog": catalog, "menus": menus, "personal_home": home, "runtime_enabled": enabled, "governance_operations": governanceOperations(), "runtime_bridge": iam.RuntimeBridge, "real_activation_automatic": false}
	if !c.IAM.Target && c.IAM.LegacyDataRole == "admin" {
		access, err := r.isolatedMigrationAccess(ctx)
		if err != nil {
			return nil, err
		}
		if access.Prepare || access.Review || access.Rollback {
			out["isolated_migration"] = access
		}
	}
	return out, nil
}
func (r *UserRepository) IAMSaveMenus(ctx context.Context, in IAMMenuCommand) (json.RawMessage, error) {
	tx, e := r.iamBegin(ctx, "space.menus.manage")
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	previous, hash, e := iamReceipt(ctx, tx, in.RequestID, "menus.configure", in)
	if e != nil || previous != nil {
		return previous, e
	}
	if e = iamBaselineTx(ctx, tx, in.Baseline); e != nil {
		return nil, e
	}
	known := map[string]bool{}
	for _, m := range iam.ReadCatalog().Menus {
		known[m.ID] = true
	}
	seen := map[string]bool{}
	orders := map[int]bool{}
	if len(in.Configuration.Items) != len(known) {
		return nil, iamInput("菜单配置必须完整包含11个稳定菜单，不能增加任意地址")
	}
	for _, m := range in.Configuration.Items {
		if !known[m.ID] || seen[m.ID] || orders[m.Order] || m.Order < 0 || strings.TrimSpace(m.Label) == "" || len([]rune(m.Label)) > 40 || strings.TrimSpace(m.Group) == "" || len([]rune(m.Group)) > 30 {
			return nil, iamInput("菜单编号、分类、名称或排序无效")
		}
		seen[m.ID] = true
		orders[m.Order] = true
	}
	if in.Configuration.Preferred != "" && !known[in.Configuration.Preferred] {
		return nil, iamInput("默认首页无效")
	}
	var version int64
	e = tx.QueryRow(ctx, `SELECT COALESCE((SELECT version FROM sys_iam_menu_configuration WHERE workspace_id=$1),0)`, WorkspaceID(ctx)).Scan(&version)
	if e != nil {
		return nil, e
	}
	if version != in.Configuration.Version {
		return nil, iamConflict("菜单版本已变化")
	}
	_, e = tx.Exec(ctx, `INSERT INTO sys_iam_menu_configuration(workspace_id,version,items,preferred_menu,updated_by) VALUES($1,1,$2::jsonb,NULLIF($3,''),$4) ON CONFLICT(workspace_id) DO UPDATE SET version=sys_iam_menu_configuration.version+1,items=EXCLUDED.items,preferred_menu=EXCLUDED.preferred_menu,updated_by=EXCLUDED.updated_by,updated_at=now()`, WorkspaceID(ctx), string(canonicalReportJSON(in.Configuration.Items)), in.Configuration.Preferred, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	if e = iamBump(ctx, tx); e != nil {
		return nil, e
	}
	return iamCommit(ctx, tx, in.RequestID, "menus.configure", hash, map[string]any{"version": version + 1, "configuration": in.Configuration, "permissions_changed": false})
}
func (r *UserRepository) IAMHome(ctx context.Context, menuID string) error {
	c, _ := auth.ClaimsFromContext(ctx)
	menus, e := r.IAMMenuConfiguration(ctx)
	if e != nil {
		return e
	}
	allowed := false
	for _, m := range iam.ReadCatalog().Menus {
		if m.ID == menuID && auth.HasAction(c, m.Capability) {
			for _, cfg := range menus.Items {
				if cfg.ID == m.ID && cfg.Visible {
					allowed = true
				}
			}
		}
	}
	if !allowed {
		return iamDenied("首页必须是当前可见且有权访问的菜单")
	}
	_, e = r.pool.maintenance.Exec(ctx, `INSERT INTO sys_iam_home_preferences(workspace_id,user_id,menu_id) SELECT $1,$2,$3 WHERE EXISTS(SELECT 1 FROM sys_workspace_members WHERE workspace_id=$1 AND user_id=$2 AND active) ON CONFLICT(workspace_id,user_id) DO UPDATE SET menu_id=EXCLUDED.menu_id`, WorkspaceID(ctx), ActorID(ctx), menuID)
	return e
}

type IAMRestrictionCommand struct {
	RequestID         string      `json:"request_id"`
	Baseline          IAMBaseline `json:"baseline"`
	DelegationID      string      `json:"delegation_id"`
	DelegationVersion int64       `json:"delegation_version"`
	Kind              string      `json:"kind"`
	ID                string      `json:"id"`
	Version           int64       `json:"version"`
	TargetID          int64       `json:"target_id"`
	Action            string      `json:"action,omitempty"`
	ExitMode          string      `json:"exit_mode,omitempty"`
	Reason            string      `json:"reason"`
}

func (r *UserRepository) IAMRestrict(ctx context.Context, in IAMRestrictionCommand) (json.RawMessage, error) {
	capability := iam.RestrictionAction(in.Kind)
	if capability == "" {
		return nil, iamInput("不支持该收紧动作；解除限制必须重新审批")
	}
	if in.ExitMode != "" && (in.Kind != "member.freeze" || in.ExitMode != "planned") {
		return nil, iamInput("退出模式无效")
	}
	tx, e := r.iamReadBegin(ctx, capability)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	previous, hash, e := iamReceipt(ctx, tx, in.RequestID, "governance.restrict", in)
	if e != nil || previous != nil {
		return previous, e
	}
	if strings.TrimSpace(in.Reason) == "" || len([]rune(in.Reason)) > 1000 {
		return nil, iamInput("请填写收紧原因")
	}
	if e = iamBaselineTx(ctx, tx, in.Baseline); e != nil {
		return nil, e
	}
	d, e := iamDelegationTx(ctx, tx, in.DelegationID, in.DelegationVersion, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	op := "restriction.revoke"
	targets := []int64{}
	actions := []string{}
	if in.Kind == "member.freeze" {
		op = "member.freeze"
		targets = []int64{in.TargetID}
	}
	if in.Kind == "action.revoke" {
		targets = []int64{in.TargetID}
		if cap, known := iam.LookupAction(in.Action); !known || cap.Scope != "workspace" {
			return nil, iamInput("许可无效")
		}
		actions = []string{in.Action}
	}
	if e = iamCeiling(d, op, actions, targets, nil, time.Now()); e != nil {
		return nil, e
	}
	if in.ExitMode == "planned" {
		if e = r.checkPlannedCustodyExit(ctx, tx, in.TargetID); e != nil {
			return nil, e
		}
	}
	fenceHeld, e := iamTryExclusive(ctx, tx, WorkspaceID(ctx))
	if e != nil {
		return nil, e
	}
	fenceConfirmed := fenceHeld && r.iamFleet(ctx, tx) == nil
	var sequence int64
	switch in.Kind {
	case "member.freeze", "action.revoke":
		var member bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_workspace_members WHERE workspace_id=$1 AND user_id=$2)`, WorkspaceID(ctx), in.TargetID).Scan(&member)
		if e != nil {
			return nil, e
		}
		if !member {
			return nil, ErrNotFound
		}
		kind := "member_freeze"
		var action any
		if in.Kind == "action.revoke" {
			kind = "action_revoke"
			action = in.Action
		}
		e = tx.QueryRow(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,action_id,reason,evidence) VALUES($1,$2,$3,$4::uuid,$5,$6,$7,$8::jsonb) RETURNING sequence`, WorkspaceID(ctx), ActorID(ctx), in.TargetID, in.RequestID, kind, action, in.Reason, string(canonicalReportJSON(map[string]any{"delegation_id": in.DelegationID, "delegation_version": in.DelegationVersion}))).Scan(&sequence)
		if e != nil {
			return nil, e
		}
		if kind == "member_freeze" {
			_, e = tx.Exec(ctx, `INSERT INTO sys_iam_custody(workspace_id,user_id,freeze_sequence,assets) SELECT $1,$2,$3,jsonb_build_object('reports',(SELECT count(*) FROM sys_managed_reports WHERE workspace_id=$1 AND owner_id=$2),'dashboards',(SELECT count(*) FROM sys_managed_dashboards WHERE workspace_id=$1 AND owner_id=$2),'personal_credentials_transferred',false,'business_signatures_reused',false)`, WorkspaceID(ctx), in.TargetID, sequence)
			if e != nil {
				return nil, e
			}
			if e = r.captureContentCustody(ctx, tx, in.TargetID, sequence); e != nil {
				return nil, e
			}
			if _, e = tx.Exec(ctx, `UPDATE sys_iam_assignments SET revoked_at=now(),version=version+1 WHERE workspace_id=$1 AND revoked_at IS NULL AND approved_members @> ARRAY[$2::bigint] AND subject_type='user'`, WorkspaceID(ctx), in.TargetID); e != nil {
				return nil, e
			}
			if _, e = tx.Exec(ctx, `UPDATE sys_iam_delegations SET revoked_at=now(),version=version+1 WHERE workspace_id=$1 AND revoked_at IS NULL AND (issuer_id=$2 OR delegate_id=$2)`, WorkspaceID(ctx), in.TargetID); e != nil {
				return nil, e
			}
			if _, e = tx.Exec(ctx, `UPDATE sys_source_tasks SET status=CASE WHEN status='running' THEN 'cancel_requested' ELSE 'stale' END,phase='成员已冻结，需重新确认' WHERE workspace_id=$1 AND actor_id=$2 AND status IN('queued','running')`, WorkspaceID(ctx), in.TargetID); e != nil {
				return nil, e
			}
			if _, e = tx.Exec(ctx, `UPDATE sys_access_requests SET status='cancelled',decision_reason='成员已冻结',version=version+1 WHERE workspace_id=$1 AND target_user_id=$2 AND status IN('pending','processing','approved_pending_release')`, WorkspaceID(ctx), in.TargetID); e != nil {
				return nil, e
			}
		}
	case "role.disable":
		if !iamUUID.MatchString(in.ID) {
			return nil, iamInput("角色编号无效")
		}
		var affected []int64
		e = tx.QueryRow(ctx, `SELECT COALESCE(array_agg(DISTINCT uid),'{}') FROM sys_iam_assignments a CROSS JOIN LATERAL unnest(a.approved_members) uid WHERE workspace_id=$1 AND role_id=$2::uuid AND revoked_at IS NULL`, WorkspaceID(ctx), in.ID).Scan(&affected)
		if e != nil {
			return nil, e
		}
		if !subset(affected, d.Targets) {
			return nil, iamDenied("影响成员超过委派范围")
		}
		tag, e := tx.Exec(ctx, `UPDATE sys_iam_role_heads SET enabled=false,version=version+1 WHERE workspace_id=$1 AND role_id=$2::uuid AND version=$3`, WorkspaceID(ctx), in.ID, in.Version)
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() != 1 {
			return nil, iamConflict("角色版本已变化")
		}
		if _, e = tx.Exec(ctx, `UPDATE sys_iam_assignments SET revoked_at=now(),version=version+1 WHERE workspace_id=$1 AND role_id=$2::uuid AND revoked_at IS NULL`, WorkspaceID(ctx), in.ID); e != nil {
			return nil, e
		}
	case "assignment.revoke", "delegation.revoke":
		if !iamUUID.MatchString(in.ID) || in.Version <= 0 {
			return nil, iamInput("授权编号或版本无效")
		}
		var targets []int64
		var query string
		if in.Kind == "assignment.revoke" {
			query = `SELECT approved_members FROM sys_iam_assignments WHERE workspace_id=$1 AND assignment_id=$2::uuid AND version=$3 AND revoked_at IS NULL FOR UPDATE`
		} else {
			query = `SELECT target_ids||ARRAY[delegate_id] FROM sys_iam_delegations WHERE workspace_id=$1 AND delegation_id=$2::uuid AND version=$3 AND revoked_at IS NULL FOR UPDATE`
		}
		if e = tx.QueryRow(ctx, query, WorkspaceID(ctx), in.ID, in.Version).Scan(&targets); e != nil {
			return nil, iamConflict("授权版本已变化")
		}
		if !subset(targets, d.Targets) {
			return nil, iamDenied("影响成员超过委派范围")
		}
		table, col := "sys_iam_assignments", "assignment_id"
		if in.Kind == "delegation.revoke" {
			table, col = "sys_iam_delegations", "delegation_id"
		}
		if _, e = tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET revoked_at=now(),version=version+1 WHERE workspace_id=$1 AND %s=$2::uuid AND version=$3`, table, col), WorkspaceID(ctx), in.ID, in.Version); e != nil {
			return nil, e
		}
	default:
		return nil, iamInput("不支持该收紧动作；解除限制必须重新审批")
	}
	if e = iamBump(ctx, tx); e != nil {
		return nil, e
	}
	fence := "pending"
	if fenceConfirmed {
		fence = "confirmed"
		if e = iamRecordSettlement(ctx, tx, WorkspaceID(ctx), ActorID(ctx), in.RequestID, r.iamRouteFingerprint); e != nil {
			return nil, e
		}
	}
	return iamCommit(ctx, tx, in.RequestID, "governance.restrict", hash, map[string]any{"kind": in.Kind, "id": in.ID, "target_id": in.TargetID, "sequence": sequence, "state": "applied", "execution_fence": fence, "status_url": fmt.Sprintf("/iam-command-status/%d/%s", WorkspaceID(ctx), in.RequestID), "custody_only": in.Kind == "member.freeze", "old_rights_restored": false})
}
