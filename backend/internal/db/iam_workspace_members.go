package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// IAMMemberCandidate is an active platform account without a membership in the target workspace.
type IAMMemberCandidate struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
}

// IAMJoinMemberCommand creates a fresh, ordinary membership for this platform account.
type IAMJoinMemberCommand struct {
	UserID    int64  `json:"user_id"`
	RequestID string `json:"request_id"`
}

// IAMMembershipCommand updates the member identity without editing account or role definitions.
type IAMMembershipCommand struct {
	Operation       string `json:"operation"`
	ExpectedVersion int64  `json:"expected_version"`
	RequestID       string `json:"request_id"`
}

// Include independently rebound subjects in the duplicate check. An account
// with a qualified rejoin must continue through that workspace subject.
const workspaceHasPlatformMember = `EXISTS(SELECT 1 FROM sys_workspace_members m
	 JOIN sys_users subject ON subject.id=m.user_id
	 WHERE m.workspace_id=$1 AND COALESCE(subject.workspace_account_id,subject.id)=u.id)`

// IAMMemberCandidates returns only the identity fields needed to confirm an ordinary workspace join.
func (r *UserRepository) IAMMemberCandidates(ctx context.Context, workspace int64, keyword string) ([]IAMMemberCandidate, error) {
	keyword = strings.TrimSpace(keyword)
	if utf8.RuneCountInString(keyword) > 100 {
		return nil, iamInput("查询关键字最多 100 个字符")
	}
	ctx = WithWorkspace(ctx, workspace)
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	space, err := directWorkspaceAuthority(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !space.CanManageMembers {
		return nil, iamConflict("此工作空间沿用现有成员维护规则")
	}
	rows, err := tx.Query(ctx, `SELECT u.id,u.username FROM sys_users u
	 WHERE u.is_active AND u.workspace_account_id IS NULL AND strpos(lower(u.username),lower($2))>0
	 AND NOT `+workspaceHasPlatformMember+`
	 ORDER BY lower(u.username),u.id LIMIT 30`, workspace, keyword)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[IAMMemberCandidate])
	if err != nil {
		return nil, err
	}
	return append([]IAMMemberCandidate{}, items...), tx.Commit(ctx)
}

func lockMatrixMembership(ctx context.Context, tx pgx.Tx) (IAMDirectWorkspace, error) {
	var space IAMDirectWorkspace
	if err := iamExclusive(ctx, tx); err != nil {
		return space, err
	}
	space, err := directWorkspaceAuthority(ctx, tx)
	if err != nil {
		return space, err
	}
	if !space.CanManageMembers {
		return space, iamConflict("此工作空间沿用现有成员维护规则")
	}
	// Serialize the last administrator check with the original workspace writer.
	var workspace int64
	err = tx.QueryRow(ctx, `SELECT id FROM sys_workspaces WHERE id=$1 FOR UPDATE`, WorkspaceID(ctx)).Scan(&workspace)
	return space, err
}

// IAMJoinMember adds a previously unaffiliated platform account. A retired
// membership retains its history and can only use the existing rejoin flow.
func (r *UserRepository) IAMJoinMember(ctx context.Context, workspace int64, in IAMJoinMemberCommand) (IAMDirectMember, error) {
	out := IAMDirectMember{UserID: in.UserID, MemberRole: "readonly", RoleCodes: []string{}, Active: true}
	if in.UserID <= 0 {
		return out, iamInput("请选择有效的平台账号")
	}
	ctx = WithWorkspace(ctx, workspace)
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = lockMatrixMembership(ctx, tx); err != nil {
		return out, err
	}
	const operation = "matrix.members.join"
	previous, hash, err := iamReceipt(ctx, tx, in.RequestID, operation, in)
	if err != nil {
		return out, err
	}
	if previous != nil {
		return out, json.Unmarshal(previous, &out)
	}
	err = tx.QueryRow(ctx, `SELECT username FROM sys_users
	 WHERE id=$1 AND is_active AND workspace_account_id IS NULL FOR SHARE`, in.UserID).Scan(&out.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO sys_workspace_members(workspace_id,user_id,role_code,direct_role_managed)
	 SELECT $1,u.id,'readonly',true FROM sys_users u WHERE u.id=$2 AND NOT `+workspaceHasPlatformMember+`
	 ON CONFLICT DO NOTHING RETURNING version`, workspace, in.UserID).Scan(&out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, iamConflict("该账号已有工作空间成员记录，请刷新核对")
	}
	if err != nil {
		return out, err
	}
	if err = iamBump(ctx, tx); err != nil {
		return out, err
	}
	_, err = iamCommit(ctx, tx, in.RequestID, operation, hash, out)
	return out, err
}

// IAMChangeMembership changes the target workspace membership and records its
// before and after state. Appointment is kept separate from business roles.
func (r *UserRepository) IAMChangeMembership(ctx context.Context, workspace, member int64, in IAMMembershipCommand) (IAMDirectMember, error) {
	var out IAMDirectMember
	if member <= 0 || in.ExpectedVersion <= 0 {
		return out, iamInput("请选择有效成员并刷新版本")
	}
	if in.Operation != "remove" && in.Operation != "appoint_admin" && in.Operation != "revoke_admin" {
		return out, iamInput("成员操作无效")
	}
	ctx = WithWorkspace(ctx, workspace)
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	space, err := lockMatrixMembership(ctx, tx)
	if err != nil {
		return out, err
	}
	if in.Operation != "remove" && !space.CanManageAdmins {
		return out, iamDenied("仅超级管理员可任免空间管理员")
	}
	operation := "matrix.members." + in.Operation
	previous, hash, err := iamReceipt(ctx, tx, in.RequestID, operation, struct {
		Member  int64                `json:"member"`
		Command IAMMembershipCommand `json:"command"`
	}{member, in})
	if err != nil {
		return out, err
	}
	if previous != nil {
		return out, json.Unmarshal(previous, &out)
	}
	var storedRole string
	var matrixAdmin bool
	err = tx.QueryRow(ctx, `SELECT m.user_id,u.username,m.role_code,m.version,`+directRoleCodes+`,m.active,m.matrix_admin
	 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
	 WHERE m.workspace_id=$1 AND m.user_id=$2 AND dbhub_iam_qualified(m.user_id)
	 FOR UPDATE OF m FOR SHARE OF u`, workspace, member).
		Scan(&out.UserID, &out.Username, &storedRole, &out.Version, &out.RoleCodes, &out.Active, &matrixAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if out.Version != in.ExpectedVersion {
		return out, iamConflict("成员版本已变化，请刷新后重试")
	}
	out.MemberRole = storedRole
	if matrixAdmin {
		out.MemberRole = "admin"
	}
	before := out
	switch in.Operation {
	case "remove":
		if err = removeMatrixMember(ctx, tx, &out); err != nil {
			return out, err
		}
	case "appoint_admin":
		if before.MemberRole == "admin" {
			return out, iamConflict("该成员已是空间管理员")
		}
		err = tx.QueryRow(ctx, `UPDATE sys_workspace_members SET matrix_admin=true,version=version+1
		 WHERE workspace_id=$1 AND user_id=$2 RETURNING version`, workspace, member).Scan(&out.Version)
		out.MemberRole = "admin"
	case "revoke_admin":
		err = revokeMatrixAdmin(ctx, tx, storedRole, &out)
	}
	if err != nil {
		return out, err
	}
	if err = iamBump(ctx, tx); err != nil {
		return out, err
	}
	_, err = iamCommit(ctx, tx, in.RequestID, operation, hash, struct {
		IAMDirectMember
		Previous IAMDirectMember `json:"previous_membership"`
	}{out, before})
	return out, err
}

func removeMatrixMember(ctx context.Context, tx pgx.Tx, out *IAMDirectMember) error {
	if out.MemberRole == "admin" || out.UserID == ActorID(ctx) {
		return iamDenied("移出仅适用于其他普通成员，管理员资格需先由超管办理")
	}
	workspace := WorkspaceID(ctx)
	var resourceOwner bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_managed_reports WHERE workspace_id=$1 AND (owner_id=$2 OR business_owner_id=$2))
	 OR EXISTS(SELECT 1 FROM sys_managed_dashboards WHERE workspace_id=$1 AND owner_id=$2 AND lifecycle='active')`, workspace, out.UserID).Scan(&resourceOwner)
	if err != nil {
		return err
	}
	if resourceOwner {
		return iamConflict("成员仍负责报表或仪表盘，请先按原流程移交资源")
	}
	if err = tx.QueryRow(ctx, `UPDATE sys_workspace_members SET active=false,version=version+1
	 WHERE workspace_id=$1 AND user_id=$2 RETURNING version`, workspace, out.UserID).Scan(&out.Version); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sys_iam_assignments SET revoked_at=now(),version=version+1
	 WHERE workspace_id=$1 AND subject_type='user' AND subject_id=$2 AND revoked_at IS NULL`, workspace, out.UserID); err != nil {
		return err
	}
	if err = retireWorkspaceMemberRelations(ctx, tx, workspace, out.UserID); err != nil {
		return err
	}
	out.Active = false
	out.RoleCodes = []string{}
	return nil
}

func revokeMatrixAdmin(ctx context.Context, tx pgx.Tx, storedRole string, out *IAMDirectMember) error {
	if out.MemberRole != "admin" {
		return iamConflict("该成员现为普通成员")
	}
	workspace := WorkspaceID(ctx)
	var hasOther bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_workspace_members m WHERE m.workspace_id=$1 AND m.user_id<>$2
	 AND (m.role_code='admin' OR m.matrix_admin) AND dbhub_iam_qualified(m.user_id))`, workspace, out.UserID).Scan(&hasOther)
	if err != nil {
		return err
	}
	if !hasOther {
		return iamConflict("当前工作空间须保留至少一位有效管理员")
	}
	out.MemberRole = storedRole
	if storedRole == "admin" {
		out.MemberRole = "readonly"
	}
	return tx.QueryRow(ctx, `UPDATE sys_workspace_members SET matrix_admin=false,role_code=$3,
	 direct_role_managed=direct_role_managed OR role_code='admin',version=version+1
	 WHERE workspace_id=$1 AND user_id=$2 RETURNING version`, workspace, out.UserID, out.MemberRole).Scan(&out.Version)
}
