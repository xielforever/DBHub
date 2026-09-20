// Package db 负责 PostgreSQL 元数据库连接池管理、迁移执行与各领域 Repository。
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Connect 创建带超时与合理池参数的 PostgreSQL 连接池并执行 Ping。
// maxConns<=0 时使用默认值 10；对接仅支持单会话的线协议代理（如开发期
// PGlite Server）时应显式传 1，避免查询交错导致结果错乱。
func Connect(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 DATABASE_URL 失败: %w", err)
	}
	if maxConns <= 0 {
		maxConns = 10
	}
	config.MaxConns = maxConns
	config.MinConns = 1
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 10 * time.Minute
	config.ConnConfig.ConnectTimeout = 8 * time.Second
	// simple 协议：参数由 pgx 客户端按标准转义后内联，天然不存在连接复用下
	// 的 prepared statement 冲突，也能兼容对扩展协议参数类型推断不完整的
	// 线协议代理（如开发期 PGlite Server）与 PgBouncer 事务池；转义由 pgx
	// 完成（要求 standard_conforming_strings=on），不引入字符串拼接注入面。
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = map[string]string{}
	}
	config.ConnConfig.RuntimeParams["standard_conforming_strings"] = "on"

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接元数据库失败: %w", err)
	}
	return pool, nil
}

// Migrate 执行 embed 的全部迁移脚本，按文件名排序、逐个在事务中执行，
// 通过 schema_migrations 表记录已执行版本，支持安全重复执行。
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	if err != nil {
		return nil, fmt.Errorf("创建迁移记录表失败: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("读取内嵌迁移文件失败: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	applied := make([]string, 0)
	for _, name := range names {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, name,
		).Scan(&exists); err != nil {
			return nil, fmt.Errorf("检查迁移版本失败: %w", err)
		}
		if exists {
			continue
		}

		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("读取迁移文件 %s 失败: %w", name, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("开启迁移事务失败: %w", err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("执行迁移 %s 失败: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, name,
		); err != nil {
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("记录迁移版本失败: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("提交迁移事务失败: %w", err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}

// ErrNotFound 仓储层统一"未找到"错误，Handler 可据此转 404。
var ErrNotFound = errors.New("记录不存在")

// ErrDuplicate 唯一约束冲突（如用户名重复）。
var ErrDuplicate = errors.New("记录已存在")
