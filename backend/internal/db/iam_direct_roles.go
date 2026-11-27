package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auditctx"
)

type IAMDirectRole struct {
	Code    string   `json:"code"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type IAMDirectMember struct {
	UserID     int64    `json:"user_id"`
	Username   string   `json:"username"`
	MemberRole string   `json:"member_role"`
	Version    int64    `json:"version"`
	RoleCodes  []string `json:"role_codes"`
	Active     bool     `json:"active"`
}

type IAMDirectWorkspace struct {
	WorkspaceID      int64             `json:"workspace_id"`
	WorkspaceName    string            `json:"workspace_name"`
	Enabled          bool              `json:"enabled"`
	CanManageMembers bool              `json:"can_manage_members"`
	CanManageAdmins  bool              `json:"can_manage_admins"`
	Roles            []IAMDirectRole   `json:"roles"`
	Members          []IAMDirectMember `json:"members"`
}

type IAMDirectRoleCommand struct {
	RequestID       string   `json:"request_id"`
	ExpectedVersion int64    `json:"expected_version"`
	RoleCodes       []string `json:"role_codes"`
}

const directRoleCodes = `ARRAY(SELECT t.code FROM sys_iam_assignments a
 JOIN sys_iam_builtin_roles t ON t.role_id=a.role_id
 JOIN sys_iam_role_heads h ON h.workspace_id=a.workspace_id AND h.role_id=a.role_id AND h.revision=a.role_revision
 JOIN sys_iam_role_revisions rev ON rev.workspace_id=h.workspace_id AND rev.role_id=h.role_id AND rev.revision=h.revision
 WHERE a.workspace_id=m.workspace_id AND a.subject_type='user' AND a.subject_id=m.user_id
  AND a.revoked_at IS NULL AND a.valid_from<=statement_timestamp() AND a.valid_until>statement_timestamp()
  AND h.enabled AND rev.enabled ORDER BY t.code)`

const directMemberRole = `CASE WHEN m.matrix_admin THEN 'admin' ELSE m.role_code END`

// directWorkspaceAuthority intentionally takes the target from the path and
// only uses the authenticated subject from the request. A platform operator
// gains access to this role directory, not a business workspace session.
func directWorkspaceAuthority(ctx context.Context, tx pgx.Tx) (IAMDirectWorkspace, error) {
	out := IAMDirectWorkspace{WorkspaceID: WorkspaceID(ctx), Roles: []IAMDirectRole{}, Members: []IAMDirectMember{}}
	actor := ActorID(ctx)
	if actor <= 0 || out.WorkspaceID <= 0 {
		return out, ErrManagedDenied
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('dbhub.workspace_id',$1,true),set_config('dbhub.actor_id',$2,true)`, fmt.Sprint(out.WorkspaceID), fmt.Sprint(actor)); err != nil {
		return out, err
	}
	var global bool
	err := tx.QueryRow(ctx, `SELECT p.is_platform_admin FROM sys_users u JOIN sys_users p ON p.id=COALESCE(u.workspace_account_id,u.id)
 WHERE u.id=$1 AND u.is_active AND p.is_active FOR SHARE OF u,p`, actor).Scan(&global)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrManagedDenied
	}
	if err != nil {
		return out, err
	}
	if !global {
		var member int64
		err = tx.QueryRow(ctx, `SELECT m.user_id FROM sys_workspace_members m
 WHERE m.workspace_id=$1 AND m.user_id=$2 AND dbhub_iam_qualified(m.user_id)
 AND (m.role_code='admin' OR (m.matrix_admin AND EXISTS(SELECT 1 FROM sys_iam_workspace_state s WHERE s.workspace_id=m.workspace_id AND s.matrix_enabled)))
 FOR SHARE OF m`, out.WorkspaceID, actor).Scan(&member)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrManagedDenied
		}
		if err != nil {
			return out, err
		}
	}
	err = tx.QueryRow(ctx, `SELECT w.name,s.matrix_enabled FROM sys_workspaces w JOIN sys_iam_workspace_state s ON s.workspace_id=w.id
 WHERE w.id=$1 AND w.enabled FOR SHARE OF w,s`, out.WorkspaceID).Scan(&out.WorkspaceName, &out.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	out.CanManageMembers = out.Enabled
	out.CanManageAdmins = out.Enabled && global
	auditctx.Update(ctx, func(d *auditctx.Data) { d.WorkspaceID = out.WorkspaceID })
	return out, err
}

func readDirectRoles(ctx context.Context, tx pgx.Tx) ([]IAMDirectRole, error) {
	rows, err := tx.Query(ctx, `SELECT t.code,t.display_name,COALESCE(r.actions,t.actions)
 FROM sys_iam_builtin_roles t
 LEFT JOIN sys_iam_role_heads h ON h.workspace_id=dbhub_workspace() AND h.role_id=t.role_id
 LEFT JOIN sys_iam_role_revisions r ON r.workspace_id=h.workspace_id AND r.role_id=h.role_id AND r.revision=h.revision
 ORDER BY t.display_order`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[IAMDirectRole])
}

func rejectBuiltinRole(ctx context.Context, tx pgx.Tx, id string) error {
	var builtin bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sys_iam_builtin_roles WHERE role_id=$1::uuid)`, id).Scan(&builtin); err != nil {
		return err
	}
	if builtin {
		return iamDenied("请在成员角色页面管理平台定义的普通角色")
	}
	return nil
}

// Historical proposals remain readable. A protected space must not turn a
// legacy assignment capability into a second role allocator for members.
func requireMatrixRoleAllocator(ctx context.Context, tx pgx.Tx, actor int64) error {
	authority, err := iamReadMatrixAuthority(ctx, tx, actor)
	if err != nil || !authority.Enabled {
		return err
	}
	if authority.SuperAdmin {
		return iamRequireMatrixAdmin(ctx, tx, actor)
	}
	var member int64
	err = tx.QueryRow(ctx, `SELECT user_id FROM sys_workspace_members
 WHERE workspace_id=$1 AND user_id=$2 AND (role_code='admin' OR matrix_admin) AND dbhub_iam_qualified(user_id) FOR SHARE`, WorkspaceID(ctx), actor).Scan(&member)
	if errors.Is(err, pgx.ErrNoRows) {
		return iamDenied("仅工作空间管理员或超级管理员可分配与撤销角色")
	}
	return err
}

func (r *UserRepository) IAMDirectMembers(ctx context.Context, workspace int64) (IAMDirectWorkspace, error) {
	ctx = WithWorkspace(ctx, workspace)
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return IAMDirectWorkspace{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	out, err := directWorkspaceAuthority(ctx, tx)
	if err != nil || !out.Enabled {
		return out, err
	}
	if out.Roles, err = readDirectRoles(ctx, tx); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT m.user_id,u.username,`+directMemberRole+`,m.version,`+directRoleCodes+`,m.active
 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
 WHERE m.workspace_id=$1 AND dbhub_iam_qualified(m.user_id) ORDER BY u.username,m.user_id`, workspace)
	if err != nil {
		return out, err
	}
	if out.Members, err = pgx.CollectRows(rows, pgx.RowToStructByPos[IAMDirectMember]); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (r *UserRepository) IAMSetDirectRoles(ctx context.Context, workspace, member int64, in IAMDirectRoleCommand) (IAMDirectMember, error) {
	out := IAMDirectMember{}
	if member <= 0 || in.ExpectedVersion <= 0 || in.RoleCodes == nil || len(in.RoleCodes) > 3 || !unique(in.RoleCodes) {
		return out, iamInput("请选择成员及有效角色，刷新后提交")
	}
	in.RoleCodes = slices.Clone(in.RoleCodes)
	slices.Sort(in.RoleCodes)
	ctx = WithWorkspace(ctx, workspace)
	tx, err := r.pool.maintenance.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err = iamExclusive(ctx, tx); err != nil {
		return out, err
	}
	space, err := directWorkspaceAuthority(ctx, tx)
	if err != nil {
		return out, err
	}
	if !space.Enabled {
		return out, iamConflict("此工作空间沿用现有角色授权规则")
	}
	const operation = "matrix.direct_roles.set"
	previous, hash, err := iamReceipt(ctx, tx, in.RequestID, operation, struct {
		Member  int64                `json:"member"`
		Command IAMDirectRoleCommand `json:"command"`
	}{member, in})
	if err != nil {
		return out, err
	}
	if previous != nil {
		// Earlier direct role receipts predate the active field.
		out.Active = true
		return out, json.Unmarshal(previous, &out)
	}
	err = tx.QueryRow(ctx, `SELECT m.user_id,u.username,`+directMemberRole+`,m.version,`+directRoleCodes+`,m.active
 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id
 WHERE m.workspace_id=$1 AND m.user_id=$2 AND dbhub_iam_qualified(m.user_id)
  AND NOT EXISTS(SELECT 1 FROM sys_iam_principal_modes p WHERE p.workspace_id=m.workspace_id AND p.user_id=m.user_id AND p.rollback_guard)
 FOR UPDATE OF m FOR SHARE OF u`, workspace, member).Scan(&out.UserID, &out.Username, &out.MemberRole, &out.Version, &out.RoleCodes, &out.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if out.MemberRole == "admin" && !space.CanManageAdmins {
		return out, iamDenied("空间管理员只能为本空间普通成员分配角色")
	}
	if out.Version != in.ExpectedVersion {
		return out, iamConflict("成员角色已变化，请刷新后重试")
	}
	var known int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sys_iam_builtin_roles WHERE code=ANY($1::text[])`, in.RoleCodes).Scan(&known); err != nil {
		return out, err
	}
	if known != len(in.RoleCodes) {
		return out, iamInput("只能分配平台定义的普通角色")
	}
	before := out.RoleCodes
	if !slices.Equal(before, in.RoleCodes) {
		if err = updateDirectSources(ctx, tx, workspace, member, in); err != nil {
			return out, err
		}
		if err = tx.QueryRow(ctx, `UPDATE sys_workspace_members SET direct_role_managed=true,version=version+1
 WHERE workspace_id=$1 AND user_id=$2 RETURNING version`, workspace, member).Scan(&out.Version); err != nil {
			return out, err
		}
		if err = iamBump(ctx, tx); err != nil {
			return out, err
		}
		out.RoleCodes = in.RoleCodes
	}
	_, err = iamCommit(ctx, tx, in.RequestID, operation, hash, struct {
		IAMDirectMember
		PreviousRoleCodes []string `json:"previous_role_codes"`
	}{out, before})
	return out, err
}

func updateDirectSources(ctx context.Context, tx pgx.Tx, workspace, member int64, in IAMDirectRoleCommand) error {
	if _, err := tx.Exec(ctx, `INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by)
 SELECT $1,role_id,1,display_name,actions,true,$2 FROM sys_iam_builtin_roles WHERE code=ANY($3::text[])
 ON CONFLICT DO NOTHING`, workspace, ActorID(ctx), in.RoleCodes); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled)
 SELECT $1,role_id,1,display_name,true FROM sys_iam_builtin_roles WHERE code=ANY($2::text[]) ON CONFLICT DO NOTHING`, workspace, in.RoleCodes); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE sys_iam_assignments a SET revoked_at=now(),version=a.version+1
 FROM sys_iam_builtin_roles t WHERE a.role_id=t.role_id AND a.workspace_id=$1 AND a.subject_type='user'
  AND a.subject_id=$2 AND a.revoked_at IS NULL AND NOT(t.code=ANY($3::text[]))`, workspace, member, in.RoleCodes); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,
 subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id)
 SELECT $1,gen_random_uuid(),t.role_id,h.revision,'user',$2,ARRAY[$2::bigint],
 TIMESTAMPTZ '9999-12-31 00:00:00+00',$4,$5::uuid
 FROM sys_iam_builtin_roles t JOIN sys_iam_role_heads h ON h.workspace_id=$1 AND h.role_id=t.role_id
 WHERE t.code=ANY($3::text[]) AND NOT EXISTS(
 SELECT 1 FROM sys_iam_assignments a WHERE a.workspace_id=$1 AND a.subject_id=$2 AND a.subject_type='user'
  AND a.role_id=t.role_id AND a.revoked_at IS NULL AND a.valid_until>now()) ORDER BY t.code`,
		workspace, member, in.RoleCodes, ActorID(ctx), in.RequestID)
	return err
}
