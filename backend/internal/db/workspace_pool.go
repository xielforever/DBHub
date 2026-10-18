package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type workspaceKey struct{}
type memberEpochKey struct{}

func MemberEpoch(ctx context.Context) int64 { v, _ := ctx.Value(memberEpochKey{}).(int64); return v }

type platformAuditKey struct{}

func WithPlatformAudit(ctx context.Context) context.Context {
	return context.WithValue(WithWorkspace(ctx, 0), platformAuditKey{}, true)
}
func validScope(ctx context.Context) bool {
	platform, _ := ctx.Value(platformAuditKey{}).(bool)
	return WorkspaceID(ctx) > 0 || platform
}

func WithWorkspace(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, workspaceKey{}, id)
}
func WorkspaceID(ctx context.Context) int64 { id, _ := ctx.Value(workspaceKey{}).(int64); return id }

var ErrWorkspaceRequired = errors.New("请选择有效工作空间")

// Every metadata operation uses a non-owner connection and transaction-local
// scope. Commit/rollback clears all GUCs before a connection returns to the pool.
type ScopedPool struct {
	runtime     *pgxpool.Pool
	maintenance *pgxpool.Pool
	role        string
}

var scopedRegistry sync.Map
var scopedInit sync.Mutex

func scopedPool(owner *pgxpool.Pool) *ScopedPool {
	if v, ok := scopedRegistry.Load(owner); ok {
		return v.(*ScopedPool)
	}
	return &ScopedPool{maintenance: owner}
}
func configureScopedRuntime(ctx context.Context, owner *pgxpool.Pool) error {
	scopedInit.Lock()
	defer scopedInit.Unlock()
	if _, ok := scopedRegistry.Load(owner); ok {
		return nil
	}
	var schema string
	if e := owner.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); e != nil {
		return e
	}
	key := make([]byte, 24)
	if _, e := rand.Read(key); e != nil {
		return e
	}
	role := "dbhub_runtime_" + hex.EncodeToString(key[:8])
	password := hex.EncodeToString(key)
	quoted := pgx.Identifier{role}.Sanitize()
	if _, e := owner.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'"); e != nil {
		return fmt.Errorf("创建受限元数据运行角色失败: %w", e)
	}
	cleanup := func() {
		_, _ = owner.Exec(context.WithoutCancel(ctx), "DROP OWNED BY "+quoted)
		_, _ = owner.Exec(context.WithoutCancel(ctx), "DROP ROLE "+quoted)
	}
	grants := []string{"GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + quoted,
		"GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + quoted,
		"GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + quoted,
		"REVOKE INSERT,UPDATE,DELETE ON sys_users,sys_roles,sys_workspaces,sys_workspace_members,sys_workspace_creation_commands,schema_migrations FROM " + quoted,
		"REVOKE SELECT ON sys_users FROM " + quoted,
		"GRANT SELECT(id,username,email,role_id,is_active,last_login_at,created_at,updated_at) ON sys_users TO " + quoted}
	for _, sql := range grants {
		if _, e := owner.Exec(ctx, sql); e != nil {
			cleanup()
			return e
		}
	}
	cfg := owner.Config().Copy()
	cfg.ConnConfig.User = role
	cfg.ConnConfig.Password = password
	cfg.MinConns = 0
	runtime, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		cleanup()
		return e
	}
	if e = runtime.Ping(ctx); e != nil {
		runtime.Close()
		cleanup()
		return e
	}
	scopedRegistry.Store(owner, &ScopedPool{runtime: runtime, maintenance: owner, role: role})
	return nil
}
func CloseScopedRuntime(owner *pgxpool.Pool) {
	if v, ok := scopedRegistry.LoadAndDelete(owner); ok {
		p := v.(*ScopedPool)
		p.runtime.Close()
		q := pgx.Identifier{p.role}.Sanitize()
		_, _ = owner.Exec(context.Background(), "DROP OWNED BY "+q)
		_, _ = owner.Exec(context.Background(), "DROP ROLE "+q)
	}
}
func (p *ScopedPool) Begin(ctx context.Context) (pgx.Tx, error) {
	if p.runtime == nil || !validScope(ctx) || ActorID(ctx) <= 0 {
		return nil, ErrWorkspaceRequired
	}
	return p.begin(ctx, pgx.TxOptions{})
}
func (p *ScopedPool) BeginSnapshot(ctx context.Context) (pgx.Tx, error) {
	return p.begin(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
}
func (p *ScopedPool) begin(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if p.runtime == nil || !validScope(ctx) || ActorID(ctx) <= 0 {
		return nil, ErrWorkspaceRequired
	}
	tx, e := p.runtime.BeginTx(ctx, options)
	if e != nil {
		return nil, e
	}
	role := ""
	if ActorAdmin(ctx) {
		role = "admin"
	} else if c, ok := actorRole(ctx); ok {
		role = c
	}
	_, e = tx.Exec(ctx, `SELECT set_config('dbhub.workspace_id',$1,true),set_config('dbhub.actor_id',$2,true),set_config('dbhub.actor_role',$3,true),set_config('dbhub.member_version',$4,true)`, strconv.FormatInt(WorkspaceID(ctx), 10), strconv.FormatInt(ActorID(ctx), 10), role, strconv.FormatInt(MemberEpoch(ctx), 10))
	if e != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, e
	}
	return tx, nil
}
func (p *ScopedPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx, e := p.Begin(ctx)
	if e != nil {
		return pgconn.CommandTag{}, e
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	tag, e := tx.Exec(ctx, sql, args...)
	if e == nil {
		e = tx.Commit(ctx)
	}
	return tag, e
}

type scopedRows struct {
	pgx.Rows
	tx     pgx.Tx
	ctx    context.Context
	closed bool
	err    error
}

func (r *scopedRows) Close() {
	if r.closed {
		return
	}
	r.closed = true
	r.Rows.Close()
	r.err = r.Rows.Err()
	if r.err == nil {
		r.err = r.tx.Commit(r.ctx)
	} else {
		_ = r.tx.Rollback(context.WithoutCancel(r.ctx))
	}
}
func (r *scopedRows) Next() bool {
	if r.closed {
		return false
	}
	if !r.Rows.Next() {
		r.Close()
		return false
	}
	return true
}
func (r *scopedRows) Err() error {
	if r.err != nil {
		return r.err
	}
	return r.Rows.Err()
}
func (p *ScopedPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx, e := p.Begin(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, sql, args...)
	if e != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, e
	}
	return &scopedRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

type scopedRow struct {
	rows pgx.Rows
	err  error
}

func (r scopedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if e := r.rows.Err(); e != nil {
			return e
		}
		return pgx.ErrNoRows
	}
	if e := r.rows.Scan(dest...); e != nil {
		return e
	}
	r.rows.Close()
	return r.rows.Err()
}
func (p *ScopedPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	rows, e := p.Query(ctx, sql, args...)
	return scopedRow{rows, e}
}
func (p *ScopedPool) Stat() *pgxpool.Stat { return p.runtime.Stat() }
