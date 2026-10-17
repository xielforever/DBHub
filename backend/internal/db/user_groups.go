package db

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type UserGroup struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     int64     `json:"version"`
	MemberIDs   []int64   `json:"member_ids"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (r *UserRepository) Groups(ctx context.Context, keyword string, limit, offset int) ([]UserGroup, int64, error) {
	if e := requireCurrentActor(ctx, r.pool); e != nil {
		return nil, 0, e
	}
	if !ActorAdmin(ctx) {
		return nil, 0, ErrNotFound
	}
	var total int64
	if e := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_user_groups WHERE name ILIKE '%'||$1||'%'`, keyword).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := r.pool.Query(ctx, `SELECT g.id,g.name,g.description,g.version,COALESCE((SELECT array_agg(user_id ORDER BY user_id) FROM sys_user_group_members m WHERE m.group_id=g.id),'{}'::bigint[]),g.updated_at FROM sys_user_groups g WHERE g.name ILIKE '%'||$1||'%' ORDER BY g.name,g.id LIMIT $2 OFFSET $3`, keyword, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []UserGroup{}
	for rows.Next() {
		g := UserGroup{}
		if e = rows.Scan(&g.ID, &g.Name, &g.Description, &g.Version, &g.MemberIDs, &g.UpdatedAt); e != nil {
			return nil, 0, e
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}
func (r *UserRepository) SaveGroup(ctx context.Context, g *UserGroup, expected int64) error {
	if e := requireCurrentActor(ctx, r.pool); e != nil {
		return e
	}
	if !ActorAdmin(ctx) {
		return ErrNotFound
	}
	g.Name = strings.TrimSpace(g.Name)
	g.Description = strings.TrimSpace(g.Description)
	if g.Name == "" || len([]rune(g.Name)) > 100 || len([]rune(g.Description)) > 500 || !validateSubjects(g.MemberIDs) {
		return fmt.Errorf("%w: 用户组名称、说明或成员参数无效（最多500人）", ErrSourceInput)
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if g.ID == 0 {
		e = tx.QueryRow(ctx, `INSERT INTO sys_user_groups(name,description,created_by)VALUES($1,$2,$3) RETURNING id,version,updated_at`, g.Name, g.Description, ActorID(ctx)).Scan(&g.ID, &g.Version, &g.UpdatedAt)
	} else {
		e = tx.QueryRow(ctx, `UPDATE sys_user_groups SET name=$2,description=$3,version=version+1,updated_at=now() WHERE id=$1 AND version=$4 RETURNING version,updated_at`, g.ID, g.Name, g.Description, expected).Scan(&g.Version, &g.UpdatedAt)
		if e == pgx.ErrNoRows {
			return ErrSourceVersionConflict
		}
	}
	if e != nil {
		return catalogError(e)
	}
	var valid bool
	if e = tx.QueryRow(ctx, `SELECT count(*)=cardinality($1::bigint[]) FROM sys_users WHERE id=ANY($1::bigint[]) AND is_active`, nonNilIDs(g.MemberIDs)).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return fmt.Errorf("%w: 组成员不存在或已停用", ErrSourceInput)
	}
	if _, e = tx.Exec(ctx, `DELETE FROM sys_user_group_members WHERE group_id=$1`, g.ID); e != nil {
		return e
	}
	for _, uid := range g.MemberIDs {
		if _, e = tx.Exec(ctx, `INSERT INTO sys_user_group_members(group_id,user_id)VALUES($1,$2)`, g.ID, uid); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (r *UserRepository) DeleteGroup(ctx context.Context, id, expected int64) error {
	if e := requireCurrentActor(ctx, r.pool); e != nil {
		return e
	}
	if !ActorAdmin(ctx) {
		return ErrNotFound
	}
	result, e := r.pool.Exec(ctx, `DELETE FROM sys_user_groups WHERE id=$1 AND version=$2`, id, expected)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return ErrSourceVersionConflict
	}
	return nil
}
