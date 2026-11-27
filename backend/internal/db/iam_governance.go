package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

var iamUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type IAMBaseline struct {
	Config int64 `json:"config_version"`
	Epoch  int64 `json:"authorization_epoch"`
}
type IAMChange struct {
	Kind               string    `json:"kind"`
	RoleID             string    `json:"role_id,omitempty"`
	RoleVersion        int64     `json:"role_version,omitempty"`
	RoleRevision       int64     `json:"role_revision,omitempty"`
	Name               string    `json:"name,omitempty"`
	Actions            []string  `json:"actions,omitempty"`
	Enabled            bool      `json:"enabled,omitempty"`
	SubjectType        string    `json:"subject_type,omitempty"`
	SubjectID          int64     `json:"subject_id,omitempty"`
	GroupID            int64     `json:"group_id,omitempty"`
	GroupVersion       int64     `json:"group_version,omitempty"`
	MemberIDs          []int64   `json:"member_ids,omitempty"`
	DelegateID         int64     `json:"delegate_id,omitempty"`
	GovernanceActions  []string  `json:"governance_actions,omitempty"`
	TargetIDs          []int64   `json:"target_ids,omitempty"`
	SourceIDs          []string  `json:"source_ids,omitempty"`
	ValidUntil         time.Time `json:"valid_until"`
	BundleID           string    `json:"bundle_id,omitempty"`
	ActionID           string    `json:"action_id,omitempty"`
	RevocationSequence int64     `json:"revocation_sequence,omitempty"`
	Reason             string    `json:"reason,omitempty"`
}
type IAMProposalCommand struct {
	RequestID         string      `json:"request_id"`
	Baseline          IAMBaseline `json:"baseline"`
	DelegationID      string      `json:"delegation_id"`
	DelegationVersion int64       `json:"delegation_version"`
	ValidUntil        time.Time   `json:"valid_until"`
	Patch             IAMChange   `json:"patch"`
}
type IAMReviewCommand struct {
	RequestID         string `json:"request_id"`
	ProposalID        string `json:"proposal_id"`
	Version           int64  `json:"version"`
	Decision          string `json:"decision"`
	DelegationID      string `json:"delegation_id"`
	DelegationVersion int64  `json:"delegation_version"`
	Comment           string `json:"comment"`
}
type iamDelegation struct {
	ID                          string
	Version                     int64
	Issuer, Delegate            int64
	Actions, Grantable, Sources []string
	Targets                     []int64
	Until                       time.Time
	Parent                      *string
	ParentVersion               *int64
}

type iamError struct {
	cause   error
	message string
}

func (e iamError) Error() string { return e.message }
func (e iamError) Unwrap() error { return e.cause }
func IAMErrorMessage(e error) (string, bool) {
	var value iamError
	if errors.As(e, &value) {
		return value.message, true
	}
	return "", false
}
func iamInput(message string) error    { return iamError{ErrManagedInput, message} }
func iamDenied(message string) error   { return iamError{ErrManagedDenied, message} }
func iamConflict(message string) error { return iamError{ErrManagedConflict, message} }

func governanceCapability(kind string) string {
	switch kind {
	case "role_upsert":
		return "space.roles.manage"
	case "assignment_add", "group_members_add", "delegation_add", "action_release":
		return "space.assign.manage"
	case "bundle_activate":
		return "space.assign.manage"
	default:
		return ""
	}
}
func governanceOperation(kind, stage string) string {
	switch kind {
	case "role_upsert":
		return "role." + stage
	case "assignment_add", "action_release":
		return "assignment." + stage
	case "group_members_add":
		return "group." + stage
	case "delegation_add":
		return "delegation." + stage
	case "bundle_activate":
		return "bundle.activate"
	}
	return ""
}
func governanceOperations() []string {
	return []string{"role.propose", "role.review", "assignment.propose", "assignment.review", "group.propose", "group.review", "delegation.propose", "delegation.review", "menu.configure", "member.freeze", "restriction.revoke", "bundle.prepare", "bundle.activate", "bundle.rollback"}
}

func (r *UserRepository) iamBegin(ctx context.Context, capability string) (pgx.Tx, error) {
	return r.iamBeginControl(ctx, capability, true)
}
func (r *UserRepository) iamReadBegin(ctx context.Context, capability string) (pgx.Tx, error) {
	return r.iamBeginControl(ctx, capability, false)
}
func (r *UserRepository) iamBeginControl(ctx context.Context, capability string, exclusive bool) (pgx.Tx, error) {
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return nil, e
	}
	fail := func(err error) (pgx.Tx, error) { _ = tx.Rollback(context.WithoutCancel(ctx)); return nil, err }
	if exclusive {
		if e = iamExclusive(ctx, tx); e != nil {
			return fail(e)
		}
	}
	if e = iamScope(ctx, tx); e != nil {
		return fail(e)
	}
	if e = iamCurrent(ctx, tx); e != nil {
		return fail(e)
	}
	if capability != "" {
		if slices.Contains(iamMatrixReservedActions, capability) {
			if e = iamRequireMatrixAdmin(ctx, tx, ActorID(ctx)); e != nil {
				return fail(e)
			}
		}
		c, _ := auth.ClaimsFromContext(ctx)
		if c != nil && c.IAM != nil && c.IAM.GovernanceOnly {
			if !auth.HasAction(c, capability) {
				return fail(ErrManagedDenied)
			}
			return tx, nil
		}
		var allowed bool
		e = tx.QueryRow(ctx, `SELECT dbhub_scope_allowed() AND dbhub_iam_control_allowed(dbhub_actor(),$1)`, capability).Scan(&allowed)
		if e != nil {
			return fail(e)
		}
		if !allowed {
			return fail(iamDenied("当前治理权限不足"))
		}
	}
	return tx, nil
}
func iamBaselineTx(ctx context.Context, tx pgx.Tx, b IAMBaseline) error {
	var current IAMBaseline
	e := tx.QueryRow(ctx, `SELECT config_version,authorization_epoch FROM sys_iam_workspace_state WHERE workspace_id=$1 FOR UPDATE`, WorkspaceID(ctx)).Scan(&current.Config, &current.Epoch)
	if e != nil {
		return e
	}
	if b != current {
		return iamConflict("空间配置已变化，请重新查看差异并提交")
	}
	return nil
}
func iamReceipt(ctx context.Context, tx pgx.Tx, requestID, operation string, input any) (json.RawMessage, string, error) {
	if !iamUUID.MatchString(requestID) || requestID == "00000000-0000-0000-0000-000000000000" {
		return nil, "", iamInput("请求编号必须为UUID")
	}
	hash := reportDigest(operation + string(canonicalReportJSON(input)))
	var previous string
	var result []byte
	var op string
	e := tx.QueryRow(ctx, `SELECT request_hash,result,operation FROM sys_iam_commands WHERE workspace_id=$1 AND actor_id=$2 AND request_id=$3::uuid`, WorkspaceID(ctx), ActorID(ctx), requestID).Scan(&previous, &result, &op)
	if e == pgx.ErrNoRows {
		return nil, hash, nil
	}
	if e != nil {
		return nil, "", e
	}
	if previous != hash || op != operation {
		return nil, "", iamConflict("同一请求编号对应的内容发生变化")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.UseNumber()
	if e = decoder.Decode(&value); e != nil {
		return nil, "", e
	}
	return canonicalReportJSON(value), hash, nil
}
func iamCommit(ctx context.Context, tx pgx.Tx, requestID, operation, hash string, result any) (json.RawMessage, error) {
	if e := auditctx.Checkpoint(ctx); e != nil {
		return nil, e
	}
	raw := canonicalReportJSON(result)
	if _, e := tx.Exec(ctx, `INSERT INTO sys_iam_events(workspace_id,actor_id,request_id,operation,payload) VALUES($1,$2,$3::uuid,$4,$5::jsonb)`, WorkspaceID(ctx), ActorID(ctx), requestID, operation, string(raw)); e != nil {
		return nil, e
	}
	if _, e := tx.Exec(ctx, `INSERT INTO sys_iam_commands(workspace_id,actor_id,request_id,operation,request_hash,result) VALUES($1,$2,$3::uuid,$4,$5,$6::jsonb)`, WorkspaceID(ctx), ActorID(ctx), requestID, operation, hash, string(raw)); e != nil {
		return nil, e
	}
	auditctx.Update(ctx, func(d *auditctx.Data) { d.CommandOperation = operation; d.CommandRequestID = requestID })
	if e := tx.Commit(ctx); e != nil {
		return nil, e
	}
	return raw, nil
}
func iamBump(ctx context.Context, tx pgx.Tx) error {
	_, e := tx.Exec(ctx, `UPDATE sys_iam_workspace_state SET config_version=config_version+1,authorization_epoch=authorization_epoch+1 WHERE workspace_id=$1`, WorkspaceID(ctx))
	return e
}

func iamDelegationTx(ctx context.Context, tx pgx.Tx, id string, version, delegate int64) (*iamDelegation, error) {
	if !iamUUID.MatchString(id) || version <= 0 {
		return nil, iamInput("请选择明确的有效治理委派")
	}
	if d, found, e := iamRecoveryDelegationTx(ctx, tx, id, version, delegate); found || e != nil {
		return d, e
	}
	d := &iamDelegation{}
	e := tx.QueryRow(ctx, `SELECT delegation_id::text,version,issuer_id,delegate_id,governance_actions,grantable_actions,target_ids,source_ids::text[],valid_until,parent_id::text,parent_version
 FROM sys_iam_delegations WHERE workspace_id=$1 AND delegation_id=$2::uuid AND version=$3 AND delegate_id=$4
 AND revoked_at IS NULL AND valid_from<=now() AND valid_until>now() AND dbhub_iam_qualified(issuer_id) AND dbhub_iam_qualified(delegate_id) FOR SHARE`, WorkspaceID(ctx), id, version, delegate).Scan(&d.ID, &d.Version, &d.Issuer, &d.Delegate, &d.Actions, &d.Grantable, &d.Targets, &d.Sources, &d.Until, &d.Parent, &d.ParentVersion)
	if e == pgx.ErrNoRows {
		return nil, iamDenied("委派已到期、撤销或不属于当前主体")
	}
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{id: true}
	child := d
	for child.Parent != nil {
		if seen[*child.Parent] || len(seen) > 32 || child.ParentVersion == nil {
			return nil, iamDenied("委派来源链无效")
		}
		seen[*child.Parent] = true
		var p iamDelegation
		e = tx.QueryRow(ctx, `SELECT delegation_id::text,version,issuer_id,delegate_id,governance_actions,grantable_actions,target_ids,source_ids::text[],valid_until,parent_id::text,parent_version
  FROM sys_iam_delegations WHERE workspace_id=$1 AND delegation_id=$2::uuid AND version=$3 AND delegate_id=$4 AND revoked_at IS NULL AND valid_from<=now() AND valid_until>now() AND dbhub_iam_qualified(issuer_id) AND dbhub_iam_qualified(delegate_id) FOR SHARE`, WorkspaceID(ctx), *child.Parent, *child.ParentVersion, child.Issuer).Scan(&p.ID, &p.Version, &p.Issuer, &p.Delegate, &p.Actions, &p.Grantable, &p.Targets, &p.Sources, &p.Until, &p.Parent, &p.ParentVersion)
		if e != nil || !subset(child.Actions, p.Actions) || !subset(child.Grantable, p.Grantable) || !subset(child.Sources, p.Sources) || !subset(child.Targets, p.Targets) || child.Until.After(p.Until) {
			return nil, iamDenied("上游委派已变化或超过其边界")
		}
		child = &p
	}
	return d, nil
}
func subset[T comparable](values, ceiling []T) bool {
	for _, v := range values {
		if !slices.Contains(ceiling, v) {
			return false
		}
	}
	return true
}
func unique[T comparable](values []T) bool {
	seen := map[T]bool{}
	for _, v := range values {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func iamCeiling(d *iamDelegation, op string, actions []string, beneficiaries []int64, sources []string, until time.Time) error {
	if !slices.Contains(d.Actions, op) || !subset(actions, d.Grantable) || !subset(beneficiaries, d.Targets) || !subset(sources, d.Sources) || until.After(d.Until) {
		return iamDenied("该变更超过委派的动作、许可、主体、来源或期限上限")
	}
	return nil
}

// impact is recomputed at both submission and approval under the workspace
// barrier. Group beneficiaries are captured explicitly and never auto-grow.
func iamImpact(ctx context.Context, tx pgx.Tx, p IAMChange) ([]int64, []string, error) {
	if strings.TrimSpace(p.Kind) == "" || governanceCapability(p.Kind) == "" || !p.ValidUntil.After(time.Now()) {
		return nil, nil, iamInput("变更类型或有效期无效")
	}
	if !unique(p.Actions) || !unique(p.MemberIDs) || !unique(p.SourceIDs) {
		return nil, nil, iamInput("许可、成员和来源不能重复")
	}
	var beneficiaries []int64
	actions := p.Actions
	switch p.Kind {
	case "action_release":
		// A release suspends only the explicitly reviewed ban generation. It
		// never restores a role source, membership, data audience or DB identity.
		if p.SubjectID <= 0 || p.RevocationSequence <= 0 || strings.TrimSpace(p.Reason) == "" || len([]rune(p.Reason)) > 1000 || len(p.Actions) != 0 || len(p.SourceIDs) != 0 {
			return nil, nil, iamInput("请选择明确成员、禁止操作及记录，并填写解除原因")
		}
		var sequence int64
		var denied bool
		e := tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0),dbhub_iam_action_denied($2,$3) FROM sys_iam_revocations WHERE workspace_id=$1 AND target_id=$2 AND kind='action_revoke' AND action_id=$3`, WorkspaceID(ctx), p.SubjectID, p.ActionID).Scan(&sequence, &denied)
		if e != nil {
			return nil, nil, e
		}
		if sequence != p.RevocationSequence || !denied {
			return nil, nil, iamConflict("禁止记录已变化或已获准解除，请刷新后重新提交")
		}
		beneficiaries = []int64{p.SubjectID}
		actions = []string{p.ActionID}
	case "role_upsert":
		if !iamUUID.MatchString(p.RoleID) || strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 100 || len(p.Actions) == 0 || !p.Enabled {
			return nil, nil, iamInput("角色名称、许可及启用状态无效；停用请使用收紧入口")
		}
		if e := rejectBuiltinRole(ctx, tx, p.RoleID); e != nil {
			return nil, nil, e
		}
		var version int64
		e := tx.QueryRow(ctx, `SELECT version FROM sys_iam_role_heads WHERE workspace_id=$1 AND role_id=$2::uuid FOR UPDATE`, WorkspaceID(ctx), p.RoleID).Scan(&version)
		if e == pgx.ErrNoRows {
			if p.RoleVersion != 0 {
				return nil, nil, iamConflict("角色不存在")
			}
		} else if e != nil {
			return nil, nil, e
		} else if version != p.RoleVersion {
			return nil, nil, iamConflict("角色版本已变化")
		}
		e = tx.QueryRow(ctx, `SELECT COALESCE(array_agg(DISTINCT uid ORDER BY uid),'{}') FROM sys_iam_assignments a CROSS JOIN LATERAL unnest(a.approved_members) uid WHERE a.workspace_id=$1 AND a.role_id=$2::uuid AND a.revoked_at IS NULL AND a.valid_until>now()`, WorkspaceID(ctx), p.RoleID).Scan(&beneficiaries)
		if e != nil {
			return nil, nil, e
		}
		var overExpiry bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_iam_assignments WHERE workspace_id=$1 AND role_id=$2::uuid AND revoked_at IS NULL AND valid_until>$3)`, WorkspaceID(ctx), p.RoleID, p.ValidUntil).Scan(&overExpiry); e != nil {
			return nil, nil, e
		}
		if overExpiry {
			return nil, nil, iamDenied("角色现有分配期限超过本次委派期限；请先收紧分配或选择足够的治理期限")
		}
	case "assignment_add":
		if !iamUUID.MatchString(p.RoleID) || p.RoleVersion <= 0 || p.RoleRevision <= 0 || p.SubjectID <= 0 || (p.SubjectType != "user" && p.SubjectType != "group") {
			return nil, nil, iamInput("角色分配目标无效")
		}
		if e := requireMatrixRoleAllocator(ctx, tx, ActorID(ctx)); e != nil {
			return nil, nil, e
		}
		if e := rejectBuiltinRole(ctx, tx, p.RoleID); e != nil {
			return nil, nil, e
		}
		var enabled bool
		e := tx.QueryRow(ctx, `SELECT r.actions,h.enabled AND r.enabled FROM sys_iam_role_heads h JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision WHERE h.workspace_id=$1 AND h.role_id=$2::uuid AND h.version=$3 AND h.revision=$4 FOR SHARE OF h`, WorkspaceID(ctx), p.RoleID, p.RoleVersion, p.RoleRevision).Scan(&actions, &enabled)
		if e != nil || !enabled {
			return nil, nil, iamConflict("角色已变化或停用")
		}
		if p.SubjectType == "user" {
			beneficiaries = []int64{p.SubjectID}
		} else {
			var version int64
			e = tx.QueryRow(ctx, `SELECT g.version,COALESCE(array_agg(m.user_id ORDER BY m.user_id) FILTER(WHERE m.user_id IS NOT NULL),'{}') FROM sys_iam_group_versions g LEFT JOIN sys_user_group_members m USING(workspace_id,group_id) WHERE g.workspace_id=$1 AND g.group_id=$2 GROUP BY g.version`, WorkspaceID(ctx), p.SubjectID).Scan(&version, &beneficiaries)
			if e != nil || version != p.GroupVersion || !slices.Equal(beneficiaries, p.MemberIDs) {
				return nil, nil, iamConflict("用户组版本或成员已变化")
			}
		}
	case "group_members_add":
		if p.GroupID <= 0 || p.GroupVersion <= 0 || len(p.MemberIDs) == 0 {
			return nil, nil, iamInput("用户组和新增成员必填")
		}
		beneficiaries = append([]int64{}, p.MemberIDs...)
		var version int64
		e := tx.QueryRow(ctx, `SELECT version FROM sys_iam_group_versions WHERE workspace_id=$1 AND group_id=$2 FOR UPDATE`, WorkspaceID(ctx), p.GroupID).Scan(&version)
		if e != nil || version != p.GroupVersion {
			return nil, nil, iamConflict("用户组版本已变化")
		}
		// Functional governance cannot approve a new data audience. The original
		// resource approval must first remove this expansion dependency.
		var resourceUse bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_source_grants WHERE workspace_id=$1 AND group_id=$2) OR EXISTS(SELECT 1 FROM sys_report_reviews WHERE workspace_id=$1 AND manifest->'group_ids' @> jsonb_build_array($2::bigint)) OR EXISTS(SELECT 1 FROM sys_dashboard_versions WHERE workspace_id=$1 AND definition->'group_ids' @> jsonb_build_array($2::bigint))`, WorkspaceID(ctx), p.GroupID).Scan(&resourceUse)
		if e != nil {
			return nil, nil, e
		}
		if resourceUse {
			return nil, nil, iamDenied("该组关联数据资源，新增成员需走资源审批；阶段1不能通过改组扩大数据受众")
		}
		// Adding group membership does not edit approved_members of any assignment.
		actions = []string{}
	case "delegation_add":
		if p.DelegateID <= 0 || len(p.TargetIDs) == 0 || len(p.GovernanceActions) == 0 || !unique(p.GovernanceActions) || !unique(p.TargetIDs) || !subset(p.GovernanceActions, governanceOperations()) {
			return nil, nil, iamInput("委派必须限定动作、许可、目标和期限")
		}
		beneficiaries = []int64{p.DelegateID}
		beneficiaries = append(beneficiaries, p.TargetIDs...)
		sort.Slice(beneficiaries, func(i, j int) bool { return beneficiaries[i] < beneficiaries[j] })
		beneficiaries = slices.Compact(beneficiaries)
	case "bundle_activate":
		var e error
		beneficiaries, actions, e = iamBundleImpact(ctx, tx, p)
		if e != nil {
			return nil, nil, e
		}
	}
	if len(beneficiaries) > 500 || p.Kind == "assignment_add" && len(beneficiaries) == 0 {
		return nil, nil, iamInput("受益人范围无效")
	}
	if len(beneficiaries) > 0 {
		var valid bool
		e := tx.QueryRow(ctx, `SELECT count(*)=cardinality($1::bigint[]) FROM sys_workspace_members m WHERE m.workspace_id=$2 AND m.user_id=ANY($1) AND dbhub_iam_qualified(m.user_id)`, beneficiaries, WorkspaceID(ctx)).Scan(&valid)
		if e != nil {
			return nil, nil, e
		}
		if !valid {
			return nil, nil, iamDenied("受益人已退出、停用、冻结或不在当前工作空间")
		}
	}
	for _, action := range actions {
		var enabled bool
		e := tx.QueryRow(ctx, `SELECT runtime_enabled AND scope='workspace' FROM sys_iam_capabilities WHERE action_id=$1`, action).Scan(&enabled)
		if e != nil || !enabled {
			return nil, nil, iamInput("包含未知、跨作用域或本阶段未启用的许可：" + action)
		}
	}
	if p.Kind == "role_upsert" || p.Kind == "assignment_add" || p.Kind == "delegation_add" || p.Kind == "bundle_activate" {
		if slices.ContainsFunc(actions, func(action string) bool { return slices.Contains(iamMatrixReservedActions, action) }) {
			authority, err := iamReadMatrixAuthority(ctx, tx, ActorID(ctx))
			if err != nil {
				return nil, nil, err
			}
			if authority.Enabled {
				return nil, nil, iamDenied("超级管理员专属许可不能通过角色或委派获得")
			}
		}
	}
	if p.Kind == "role_upsert" || p.Kind == "delegation_add" {
		for _, action := range actions {
			cap, _ := iam.LookupAction(action)
			if !subset(cap.Dependencies, actions) {
				return nil, nil, iamInput("角色许可缺少依赖：" + action)
			}
		}
	}
	return beneficiaries, actions, nil
}

func (r *UserRepository) IAMPreview(ctx context.Context, in IAMProposalCommand) (any, error) {
	tx, e := r.iamBegin(ctx, governanceCapability(in.Patch.Kind))
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = iamBaselineTx(ctx, tx, in.Baseline); e != nil {
		return nil, e
	}
	beneficiaries, actions, e := iamImpact(ctx, tx, in.Patch)
	if e != nil {
		return nil, e
	}
	d, e := iamDelegationTx(ctx, tx, in.DelegationID, in.DelegationVersion, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	if e = iamCeiling(d, governanceOperation(in.Patch.Kind, "propose"), actions, beneficiaries, in.Patch.SourceIDs, in.Patch.ValidUntil); e != nil {
		return nil, e
	}
	if in.Patch.Kind == "delegation_add" && !subset(in.Patch.GovernanceActions, d.Actions) {
		return nil, iamDenied("不能委派自己没有的治理授权")
	}
	return map[string]any{"patch": in.Patch, "beneficiaries": beneficiaries, "actions": actions, "request_hash": reportDigest(string(canonicalReportJSON(in))), "baseline": in.Baseline, "independent_review_required": true, "original_data_identity_changed": false}, nil
}
func (r *UserRepository) IAMPropose(ctx context.Context, in IAMProposalCommand) (json.RawMessage, error) {
	tx, e := r.iamBegin(ctx, governanceCapability(in.Patch.Kind))
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	previous, hash, e := iamReceipt(ctx, tx, in.RequestID, "governance.propose", in)
	if e != nil || previous != nil {
		return previous, e
	}
	if e = iamBaselineTx(ctx, tx, in.Baseline); e != nil {
		return nil, e
	}
	beneficiaries, actions, e := iamImpact(ctx, tx, in.Patch)
	if e != nil {
		return nil, e
	}
	d, e := iamDelegationTx(ctx, tx, in.DelegationID, in.DelegationVersion, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	if !in.ValidUntil.After(time.Now()) || in.ValidUntil.After(d.Until) || in.ValidUntil.After(in.Patch.ValidUntil) {
		return nil, iamInput("提案有效期超过来源或变更的期限")
	}
	if e = iamCeiling(d, governanceOperation(in.Patch.Kind, "propose"), actions, beneficiaries, in.Patch.SourceIDs, in.Patch.ValidUntil); e != nil {
		return nil, e
	}
	if in.Patch.Kind == "delegation_add" && !subset(in.Patch.GovernanceActions, d.Actions) {
		return nil, iamDenied("不能委派自己没有的治理授权")
	}
	id := dashboardUUID()
	if beneficiaries == nil {
		beneficiaries = []int64{}
	}
	_, e = tx.Exec(ctx, `INSERT INTO sys_iam_proposals(workspace_id,proposal_id,request_id,request_hash,requester_id,beneficiary_ids,baseline_config,baseline_authorization,delegation_id,delegation_version,patch,state,valid_until) VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9::uuid,$10,$11::jsonb,'pending',$12)`, WorkspaceID(ctx), id, in.RequestID, hash, ActorID(ctx), beneficiaries, in.Baseline.Config, in.Baseline.Epoch, in.DelegationID, in.DelegationVersion, string(canonicalReportJSON(in.Patch)), in.ValidUntil)
	if e != nil {
		return nil, e
	}
	return iamCommit(ctx, tx, in.RequestID, "governance.propose", hash, map[string]any{"id": id, "version": 1, "state": "pending", "beneficiaries": beneficiaries, "patch": in.Patch, "baseline": in.Baseline})
}

func (r *UserRepository) IAMReview(ctx context.Context, in IAMReviewCommand) (json.RawMessage, error) {
	tx, e := r.iamBegin(ctx, "")
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	previous, hash, e := iamReceipt(ctx, tx, in.RequestID, "governance.review", in)
	if e != nil || previous != nil {
		return previous, e
	}
	if !iamUUID.MatchString(in.ProposalID) || in.Version <= 0 || !slices.Contains([]string{"approve", "reject", "withdraw"}, in.Decision) || len([]rune(in.Comment)) > 1000 {
		return nil, iamInput("审批参数无效")
	}
	var p IAMChange
	var raw []byte
	var requester, version, dversion int64
	var beneficiaries []int64
	var b IAMBaseline
	var delegation, state string
	var expiry time.Time
	e = tx.QueryRow(ctx, `SELECT requester_id,beneficiary_ids,baseline_config,baseline_authorization,delegation_id::text,delegation_version,patch,state,valid_until,version FROM sys_iam_proposals WHERE workspace_id=$1 AND proposal_id=$2::uuid FOR UPDATE`, WorkspaceID(ctx), in.ProposalID).Scan(&requester, &beneficiaries, &b.Config, &b.Epoch, &delegation, &dversion, &raw, &state, &expiry, &version)
	if e != nil {
		return nil, managedError(e)
	}
	if e = json.Unmarshal(raw, &p); e != nil {
		return nil, e
	}
	if version != in.Version || state != "pending" {
		return nil, iamConflict("提案状态或版本已变化")
	}
	if in.Decision == "withdraw" {
		if requester != ActorID(ctx) {
			return nil, iamDenied("只能撤回自己的提案")
		}
		state = "withdrawn"
	} else {
		if ActorID(ctx) == requester || slices.Contains(beneficiaries, ActorID(ctx)) {
			return nil, iamDenied("申请人及全部受益人必须回避审批")
		}
		if p.Kind == "role_upsert" {
			// Acquire identity locks in a stable order for two concurrent reviews.
			for _, id := range []int64{min(requester, ActorID(ctx)), max(requester, ActorID(ctx))} {
				if e = iamRequireMatrixAdmin(ctx, tx, id); e != nil {
					return nil, e
				}
			}
		}
		if p.Kind == "assignment_add" {
			for _, id := range []int64{min(requester, ActorID(ctx)), max(requester, ActorID(ctx))} {
				if e = requireMatrixRoleAllocator(ctx, tx, id); e != nil {
					return nil, e
				}
			}
		}
		var allowed bool
		e = tx.QueryRow(ctx, `SELECT dbhub_iam_control_allowed(dbhub_actor(),$1)`, governanceCapability(p.Kind)).Scan(&allowed)
		if e != nil {
			return nil, e
		}
		if c, ok := auth.ClaimsFromContext(ctx); ok && c.IAM != nil && c.IAM.GovernanceOnly {
			allowed = auth.HasAction(c, governanceCapability(p.Kind))
		}
		if !allowed {
			return nil, iamDenied("没有该类治理审批权限")
		}
		d, e := iamDelegationTx(ctx, tx, in.DelegationID, in.DelegationVersion, ActorID(ctx))
		if e != nil {
			return nil, e
		}
		affected, actions, e := iamImpact(ctx, tx, p)
		if e != nil {
			return nil, e
		}
		if !slices.Equal(affected, beneficiaries) {
			return nil, iamConflict("受益人或来源已变化，必须重新提交")
		}
		if e = iamCeiling(d, governanceOperation(p.Kind, "review"), actions, beneficiaries, p.SourceIDs, p.ValidUntil); e != nil {
			return nil, e
		}
		original, e := iamDelegationTx(ctx, tx, delegation, dversion, requester)
		if e != nil {
			return nil, e
		}
		if e = iamCeiling(original, governanceOperation(p.Kind, "propose"), actions, beneficiaries, p.SourceIDs, p.ValidUntil); e != nil {
			return nil, e
		}
		var requesterAllowed bool
		e = tx.QueryRow(ctx, `SELECT dbhub_iam_qualified($1) AND dbhub_iam_control_allowed($1,$2)`, requester, governanceCapability(p.Kind)).Scan(&requesterAllowed)
		if e != nil {
			return nil, e
		}
		if !requesterAllowed && original.Issuer != 0 {
			return nil, iamDenied("申请人的当前治理资格已失效")
		}
		if time.Now().After(expiry) {
			state = "expired"
		} else if e = iamBaselineTx(ctx, tx, b); e != nil {
			state = "conflict"
		} else if in.Decision == "reject" {
			if strings.TrimSpace(in.Comment) == "" {
				return nil, iamInput("退回必须填写原因")
			}
			state = "rejected"
		} else {
			if p.Kind == "delegation_add" && (!subset(p.GovernanceActions, original.Actions) || !subset(p.GovernanceActions, d.Actions)) {
				return nil, iamDenied("委派治理动作超过复核来源边界")
			}
			if p.Kind == "bundle_activate" {
				e = r.iamApplyBundle(ctx, tx, p)
			} else {
				e = iamApply(ctx, tx, in.ProposalID, requester, p, beneficiaries, original)
			}
			if e != nil {
				return nil, e
			}
			state = "applied"
			if e = iamBump(ctx, tx); e != nil {
				return nil, e
			}
		}
	}
	var reviewer any
	var reviewerDelegation any
	if in.Decision != "withdraw" {
		reviewer = ActorID(ctx)
		reviewerDelegation = in.DelegationID
	}
	_, e = tx.Exec(ctx, `UPDATE sys_iam_proposals SET state=$3,version=version+1,reviewer_id=$4,review_delegation_id=$5::uuid,reviewed_at=now() WHERE workspace_id=$1 AND proposal_id=$2::uuid`, WorkspaceID(ctx), in.ProposalID, state, reviewer, reviewerDelegation)
	if e != nil {
		return nil, e
	}
	return iamCommit(ctx, tx, in.RequestID, "governance.review", hash, map[string]any{"id": in.ProposalID, "version": version + 1, "state": state, "reviewer_id": reviewer, "comment": in.Comment})
}

func iamApply(ctx context.Context, tx pgx.Tx, proposal string, requester int64, p IAMChange, beneficiaries []int64, parent *iamDelegation) error {
	wid := WorkspaceID(ctx)
	switch p.Kind {
	case "action_release":
		// Parse the reviewed JSON timestamp and the applied timestamp identically
		// in PG (pgx binary times truncate, whereas text timestamps round micros).
		_, e := tx.Exec(ctx, `INSERT INTO sys_iam_action_releases(workspace_id,user_id,action_id,revocation_sequence,proposal_id,valid_until) VALUES($1,$2,$3,$4,$5::uuid,$6)`, wid, p.SubjectID, p.ActionID, p.RevocationSequence, proposal, p.ValidUntil.Format(time.RFC3339Nano))
		return e
	case "role_upsert":
		revision := int64(1)
		if p.RoleVersion > 0 {
			if e := tx.QueryRow(ctx, `SELECT revision+1 FROM sys_iam_role_heads WHERE workspace_id=$1 AND role_id=$2::uuid AND version=$3`, wid, p.RoleID, p.RoleVersion).Scan(&revision); e != nil {
				return iamConflict("角色版本已变化")
			}
		}
		if _, e := tx.Exec(ctx, `INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,proposal_id,created_by) VALUES($1,$2::uuid,$3,$4,$5,true,$6::uuid,$7)`, wid, p.RoleID, revision, strings.TrimSpace(p.Name), p.Actions, proposal, requester); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled) VALUES($1,$2::uuid,$3,$4,true) ON CONFLICT(workspace_id,role_id) DO UPDATE SET revision=EXCLUDED.revision,display_name=EXCLUDED.display_name,enabled=true,version=sys_iam_role_heads.version+1`, wid, p.RoleID, revision, strings.TrimSpace(p.Name)); e != nil {
			return e
		}
		// Only current, unrevoked grants participate. Disabled/revoked historical
		// sources are never revived merely by re-enabling the role.
		if p.RoleVersion > 0 {
			_, e := tx.Exec(ctx, `UPDATE sys_iam_assignments SET role_revision=$3,version=version+1,proposal_id=$4::uuid WHERE workspace_id=$1 AND role_id=$2::uuid AND revoked_at IS NULL AND valid_until>now()`, wid, p.RoleID, revision, proposal)
			return e
		}
	case "assignment_add":
		_, e := tx.Exec(ctx, `INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id) VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10::uuid)`, wid, dashboardUUID(), p.RoleID, p.RoleRevision, p.SubjectType, p.SubjectID, beneficiaries, p.ValidUntil, requester, proposal)
		return e
	case "group_members_add":
		for _, uid := range p.MemberIDs {
			if _, e := tx.Exec(ctx, `INSERT INTO sys_user_group_members(workspace_id,group_id,user_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, wid, p.GroupID, uid); e != nil {
				return e
			}
		}
		_, e := tx.Exec(ctx, `UPDATE sys_user_groups SET version=version+1,updated_at=now() WHERE workspace_id=$1 AND id=$2`, wid, p.GroupID)
		return e
	case "delegation_add":
		sources := p.SourceIDs
		if sources == nil {
			sources = []string{}
		}
		_, e := tx.Exec(ctx, `INSERT INTO sys_iam_delegations(workspace_id,delegation_id,issuer_id,delegate_id,governance_actions,grantable_actions,target_ids,source_ids,valid_until,proposal_id,parent_id,parent_version) VALUES($1,$2::uuid,$3,$4,$5,$6,$7,$8::uuid[],$9,$10::uuid,$11::uuid,$12)`, wid, dashboardUUID(), requester, p.DelegateID, p.GovernanceActions, p.Actions, p.TargetIDs, sources, p.ValidUntil, proposal, parent.ID, parent.Version)
		return e
	}
	return nil
}
