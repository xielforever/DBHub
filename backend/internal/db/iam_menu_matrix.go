package db

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/iam"
)

// IAMRoleMenuVisibility stores only explicit show/hide choices. Omitted
// roles and cells inherit the workspace menu, with the ordinary page check.
type IAMRoleMenuVisibility map[string]map[string]string

type iamMatrixAuthority struct {
	Enabled    bool
	SuperAdmin bool
}

type iamMatrixQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

var iamMatrixReservedActions = []string{"space.members.manage", "space.menus.manage", "space.roles.manage"}

var iamMatrixLegacyGovernance = []string{
	"space.assign.view", "space.assign.manage", "space.menus.view", "space.menus.manage", "space.roles.view", "space.roles.manage",
}

func iamReadMatrixAuthority(ctx context.Context, q iamMatrixQuerier, actor int64) (iamMatrixAuthority, error) {
	var authority iamMatrixAuthority
	err := q.QueryRow(ctx, `SELECT s.matrix_enabled, EXISTS(
		SELECT 1 FROM sys_users u JOIN sys_users p ON p.id=COALESCE(u.workspace_account_id,u.id)
		WHERE u.id=$2 AND u.is_active AND p.is_active AND p.is_platform_admin)
		FROM sys_iam_workspace_state s WHERE s.workspace_id=$1`, WorkspaceID(ctx), actor).Scan(&authority.Enabled, &authority.SuperAdmin)
	return authority, err
}

func iamRequireMatrixAdmin(ctx context.Context, tx pgx.Tx, actor int64) error {
	authority, err := iamReadMatrixAuthority(ctx, tx, actor)
	if err != nil || !authority.Enabled {
		return err
	}
	if !authority.SuperAdmin {
		return iamDenied("此操作仅限超级管理员")
	}
	// Keep the currently authorized principal active until the write commits.
	// A concurrent account suspension either wins first or waits for this write.
	var privileged bool
	err = tx.QueryRow(ctx, `SELECT p.is_platform_admin AND p.is_active
		FROM sys_users u JOIN sys_users p ON p.id=COALESCE(u.workspace_account_id,u.id)
		WHERE u.id=$1 AND u.is_active FOR SHARE OF p`, actor).Scan(&privileged)
	if err == pgx.ErrNoRows || err == nil && !privileged {
		return iamDenied("此操作仅限超级管理员")
	}
	return err
}

func iamRequireLegacyAccountOperation(ctx context.Context, tx pgx.Tx) error {
	authority, err := iamReadMatrixAuthority(ctx, tx, ActorID(ctx))
	if err == nil && authority.Enabled {
		return iamDenied("请通过平台管理维护账号")
	}
	return err
}

func iamValidateRoleMenuVisibility(ctx context.Context, tx pgx.Tx, choices IAMRoleMenuVisibility, known map[string]bool) error {
	var roleIDs []string
	for roleID, menus := range choices {
		if !iamUUID.MatchString(roleID) {
			return iamInput("角色编号或菜单设置无效")
		}
		roleIDs = append(roleIDs, roleID)
		for menuID, value := range menus {
			if !known[menuID] || value != "show" && value != "hide" {
				return iamInput("菜单编号或显示方式无效")
			}
		}
	}
	if len(roleIDs) == 0 {
		return nil
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT role_id FROM sys_iam_role_heads WHERE workspace_id=$1
		UNION SELECT role_id FROM sys_iam_builtin_roles
	) roles WHERE role_id::text=ANY($2::text[])`, WorkspaceID(ctx), roleIDs).Scan(&count); err != nil {
		return err
	}
	if count != len(roleIDs) {
		return iamInput("角色不在当前工作空间")
	}
	return nil
}

func iamVisibleMenuIDs(s *iam.Snapshot, menus IAMMenus, actions []string, now time.Time) []string {
	menuActions := map[string]string{}
	for _, menu := range iam.ReadCatalog().Menus {
		menuActions[menu.ID] = menu.Capability
	}
	ids := []string{}
	for _, item := range menus.Items {
		capability := menuActions[item.ID]
		if capability == "" || !item.Visible || !slices.Contains(actions, capability) {
			continue
		}
		if !s.Target || s.GovernanceOnly || menus.RoleVisibility == nil {
			ids = append(ids, item.ID)
			continue
		}
		for _, source := range s.Sources {
			if source.ExpiresAt != nil && !now.Before(*source.ExpiresAt) || !slices.Contains(source.Actions, capability) {
				continue
			}
			if (*menus.RoleVisibility)[source.RoleID][item.ID] != "hide" {
				ids = append(ids, item.ID)
				break
			}
		}
	}
	return ids
}
