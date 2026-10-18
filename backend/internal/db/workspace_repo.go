package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
)

type Workspace struct {
	Version      int64  `json:"membership_version"`
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	AllowUnknown bool   `json:"allow_unknown_freshness"`
}

func (r *UserRepository) Workspaces(ctx context.Context) ([]Workspace, error) {
	rows, e := r.pool.maintenance.Query(ctx, `SELECT w.id,w.name,m.role_code,w.allow_unknown_freshness,m.version FROM sys_workspaces w JOIN sys_workspace_members m ON m.workspace_id=w.id JOIN sys_users u ON u.id=m.user_id WHERE m.user_id=$1 AND m.active AND u.is_active AND w.enabled ORDER BY w.id`, ActorID(ctx))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		var w Workspace
		if e = rows.Scan(&w.ID, &w.Name, &w.Role, &w.AllowUnknown, &w.Version); e != nil {
			return nil, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (r *UserRepository) BindWorkspace(ctx context.Context, id int64) (context.Context, error) {
	spaces, e := r.Workspaces(ctx)
	if e != nil {
		return ctx, e
	}
	if id == 0 {
		if len(spaces) != 1 {
			return ctx, ErrWorkspaceRequired
		}
		id = spaces[0].ID
	}
	for _, w := range spaces {
		if w.ID == id {
			c, ok := auth.ClaimsFromContext(ctx)
			if !ok || c == nil {
				return ctx, ErrWorkspaceRequired
			}
			copy := *c
			copy.Role = w.Role
			return context.WithValue(WithWorkspace(context.WithValue(ctx, auth.ClaimsContextKey, &copy), id), memberEpochKey{}, w.Version), nil
		}
	}
	return ctx, ErrNotFound
}

// Creating an organization is a platform operation. It grants only its explicit
// creator membership; no sources, reports, groups or credentials are copied.
func (r *UserRepository) CreateWorkspace(ctx context.Context, name string, keys ...string) (*Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return nil, ErrManagedInput
	}
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var ok bool
	if e = tx.QueryRow(ctx, `SELECT is_platform_admin AND is_active FROM sys_users WHERE id=$1`, ActorID(ctx)).Scan(&ok); e != nil || !ok {
		return nil, ErrManagedDenied
	}
	requestID := dashboardUUID()
	if len(keys) > 0 {
		requestID = keys[0]
	}
	auditctx.Update(ctx, func(d *auditctx.Data) { d.CommandRequestID = requestID; d.CommandOperation = "workspace-create" })
	if e = auditctx.Checkpoint(ctx); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("workspace-create:%d:%s", ActorID(ctx), requestID)); e != nil {
		return nil, e
	}
	var previous string
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT request_hash,result FROM sys_workspace_creation_commands WHERE actor_id=$1 AND request_id=$2::uuid`, ActorID(ctx), requestID).Scan(&previous, &raw)
	if e == nil {
		if previous != reportDigest(name) {
			return nil, ErrManagedConflict
		}
		var w Workspace
		if e = json.Unmarshal(raw, &w); e != nil {
			return nil, e
		}
		return &w, nil
	}
	if e != pgx.ErrNoRows {
		return nil, e
	}
	w := &Workspace{Name: name, Role: "admin"}
	if e = tx.QueryRow(ctx, `INSERT INTO sys_workspaces(name) VALUES($1) RETURNING id`, name).Scan(&w.ID); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES($1,$2,'admin')`, w.ID, ActorID(ctx)); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO sys_workspace_creation_commands(actor_id,request_id,request_hash,result) VALUES($1,$2::uuid,$3,$4::jsonb)`, ActorID(ctx), requestID, reportDigest(name), string(canonicalReportJSON(w))); e != nil {
		return nil, e
	}
	return w, tx.Commit(ctx)
}
func (r *UserRepository) AddMember(ctx context.Context, username, role string) error {
	if !ActorAdmin(ctx) {
		return ErrManagedDenied
	}
	if role != "admin" && role != "developer" && role != "readonly" {
		return ErrManagedInput
	}
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if e = workspaceAdminTx(ctx, tx); e != nil {
		return e
	}
	var uid int64
	if e = tx.QueryRow(ctx, `SELECT id FROM sys_users WHERE username=$1 AND is_active FOR SHARE`, username).Scan(&uid); e != nil {
		return managedError(e)
	}
	tag, e := tx.Exec(ctx, `INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, WorkspaceID(ctx), uid, role)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return ErrManagedConflict
	}
	return tx.Commit(ctx)
}
func workspaceAdminTx(ctx context.Context, tx pgx.Tx) error {
	if WorkspaceID(ctx) <= 0 || ActorID(ctx) <= 0 {
		return ErrManagedDenied
	}
	var uid int64
	e := tx.QueryRow(ctx, `SELECT m.user_id FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id JOIN sys_workspaces w ON w.id=m.workspace_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.active AND m.role_code='admin' AND u.is_active AND w.enabled FOR SHARE OF m,u,w`, WorkspaceID(ctx), ActorID(ctx)).Scan(&uid)
	if e != nil {
		return ErrManagedDenied
	}
	return nil
}
func (r *UserRepository) UpdateMembership(ctx context.Context, id int64, role string, active *bool, expected int64) error {
	if !ActorAdmin(ctx) {
		return ErrManagedDenied
	}
	if e := requireCurrentActor(ctx, r.pool); e != nil {
		return e
	}
	if role != "" && role != "admin" && role != "developer" && role != "readonly" {
		return ErrManagedInput
	}
	tx, e := r.pool.maintenance.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	// Serialize last-admin and departure checks for this workspace across replicas.
	var wid int64
	if e = tx.QueryRow(ctx, `SELECT id FROM sys_workspaces WHERE id=$1 FOR UPDATE`, WorkspaceID(ctx)).Scan(&wid); e != nil {
		return e
	}
	if e = workspaceAdminTx(ctx, tx); e != nil {
		return e
	}
	var prev string
	var on bool
	var version int64
	if e = tx.QueryRow(ctx, `SELECT role_code,active,version FROM sys_workspace_members WHERE workspace_id=$1 AND user_id=$2 FOR UPDATE`, wid, id).Scan(&prev, &on, &version); e != nil {
		return managedError(e)
	}
	if expected > 0 && version != expected {
		return ErrManagedConflict
	}
	if role == "" {
		role = prev
	}
	if active != nil {
		on = *active
	}
	if !on || role != "admin" {
		var n int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id<>$2 AND m.active AND m.role_code='admin' AND u.is_active`, wid, id).Scan(&n); e != nil {
			return e
		}
		if prev == "admin" && n == 0 {
			return fmt.Errorf("%w: 不能移除最后一位管理员", ErrManagedInput)
		}
	}
	if !on {
		var n int
		if e = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM sys_managed_reports WHERE workspace_id=$1 AND owner_id=$2)+(SELECT count(*) FROM sys_managed_dashboards WHERE workspace_id=$1 AND owner_id=$2 AND lifecycle='active')`, wid, id).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return ErrUserResourcesInUse
		}
	}
	_, e = tx.Exec(ctx, `UPDATE sys_workspace_members SET role_code=$3,active=$4,version=version+1 WHERE workspace_id=$1 AND user_id=$2`, wid, id, role, on)
	if e != nil {
		return e
	}
	if !on {
		_, e = tx.Exec(ctx, `WITH removed AS(DELETE FROM sys_user_group_members WHERE workspace_id=$1 AND user_id=$2 RETURNING group_id) UPDATE sys_user_groups SET version=version+1,updated_at=now() WHERE workspace_id=$1 AND id IN(SELECT group_id FROM removed)`, wid, id)
		if e != nil {
			return e
		}
	}
	if !on {
		if _, e = tx.Exec(ctx, `UPDATE sys_access_requests SET status=CASE WHEN expires_at<=now() THEN 'expired' ELSE 'cancelled' END,decision_reason='目标成员已停用',version=version+1 WHERE workspace_id=$1 AND target_user_id=$2 AND status IN('pending','processing','approved_pending_release')`, wid, id); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

func (r *UserRepository) SetWorkspacePolicy(ctx context.Context, allow bool) error {
	if !ActorAdmin(ctx) {
		return ErrManagedDenied
	}
	if e := requireCurrentActor(ctx, r.pool); e != nil {
		return e
	}
	tag, e := r.pool.maintenance.Exec(ctx, `UPDATE sys_workspaces SET allow_unknown_freshness=$2 WHERE id=$1 AND EXISTS(SELECT 1 FROM sys_workspace_members m JOIN sys_users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$3 AND m.active AND m.role_code='admin' AND u.is_active)`, WorkspaceID(ctx), allow, ActorID(ctx))
	if e == nil && tag.RowsAffected() != 1 {
		return ErrManagedDenied
	}
	return e
}

func (r *UserRepository) IsPlatformAdmin(ctx context.Context) bool {
	var ok bool
	e := r.pool.maintenance.QueryRow(ctx, `SELECT is_platform_admin AND is_active FROM sys_users WHERE id=$1`, ActorID(ctx)).Scan(&ok)
	return e == nil && ok
}
