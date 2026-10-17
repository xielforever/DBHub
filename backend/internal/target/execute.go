package target

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/sqlparse"
)

type ExecuteOptions struct {
	MaxRows          int
	Timeout          time.Duration
	Args             []any
	AllowUnsafeWrite bool
	// BeforeExecute 必须在首次发送用户 SQL 前成功，登记失败时禁止执行。
	BeforeExecute func() error
}

func (r *RelDB) Begin(ctx context.Context, readOnly bool) error {
	if r.pgTx != nil || r.myTx != nil {
		return fmt.Errorf("事务已开启")
	}
	var err error
	if r.pgConn != nil {
		mode := pgx.ReadWrite
		if readOnly {
			mode = pgx.ReadOnly
		}
		r.pgTx, err = r.pgConn.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
	} else {
		r.myTx, err = r.sqlConn.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	}
	r.readOnly = readOnly
	return err
}
func (r *RelDB) Commit(ctx context.Context) error {
	if r.pgTx != nil {
		e := r.pgTx.Commit(ctx)
		r.pgTx = nil
		r.readOnly = false
		return e
	}
	if r.myTx != nil {
		e := r.myTx.Commit()
		r.myTx = nil
		r.readOnly = false
		return e
	}
	return fmt.Errorf("事务未开启")
}
func (r *RelDB) Rollback(ctx context.Context) error {
	if r.pgTx != nil {
		e := r.pgTx.Rollback(ctx)
		r.pgTx = nil
		r.readOnly = false
		return e
	}
	if r.myTx != nil {
		e := r.myTx.Rollback()
		r.myTx = nil
		r.readOnly = false
		return e
	}
	return fmt.Errorf("事务未开启")
}

func Execute(ctx context.Context, handle *RelDB, query string, readOnly bool) (*ExecResult, error) {
	return ExecuteWithOptions(ctx, handle, query, readOnly, ExecuteOptions{})
}

func ExecuteWithOptions(ctx context.Context, r *RelDB, query string, readOnly bool, opt ExecuteOptions) (out *ExecResult, err error) {
	if r == nil || r.Conn == nil {
		return nil, fmt.Errorf("目标连接为空")
	}
	ast, err := sqlparse.Parse(query, r.Conn.Type)
	if err != nil {
		return nil, err
	}
	if readOnly && !r.readOnly && (r.pgTx != nil || r.myTx != nil) {
		return nil, fmt.Errorf("角色已变更，请重新开启只读事务")
	}
	if (readOnly || r.readOnly) && !ast.ReadOnly {
		return nil, fmt.Errorf("只读角色禁止写操作和有副作用的语句")
	}
	if ast.UnsafeWrite && !opt.AllowUnsafeWrite {
		return nil, fmt.Errorf("禁止执行没有 WHERE 条件或条件明显恒真的 UPDATE/DELETE")
	}
	if opt.MaxRows <= 0 || opt.MaxRows > 10000 {
		opt.MaxRows = 1000
	}
	if opt.Timeout <= 0 || opt.Timeout > 120*time.Second {
		opt.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	ownTx := false
	if readOnly && r.pgTx == nil && r.myTx == nil {
		if err = r.Begin(ctx, true); err != nil {
			return nil, err
		}
		ownTx = true
	}
	if ownTx {
		defer func() {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = r.Rollback(cleanup)
		}()
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if opt.BeforeExecute != nil {
		if err = opt.BeforeExecute(); err != nil {
			return nil, err
		}
	}
	start := time.Now()
	// 保留驱动错误，同时携带真实超时/取消原因；成功响应不会因后续取消被改写。
	defer func() {
		if err != nil && ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
	}()
	result := &ExecResult{Kind: "query", Columns: []string{}, Rows: [][]any{}}
	if r.pgConn != nil {
		var rows pgx.Rows
		if r.pgTx != nil {
			rows, err = r.pgTx.Query(ctx, query, opt.Args...)
		} else {
			rows, err = r.pgConn.Query(ctx, query, opt.Args...)
		}
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for _, field := range rows.FieldDescriptions() {
			result.Columns = append(result.Columns, string(field.Name))
		}
		for rows.Next() {
			if len(result.Rows) >= opt.MaxRows {
				result.Truncated = true
				break
			}
			values, e := rows.Values()
			if e != nil {
				return nil, e
			}
			result.Rows = append(result.Rows, values)
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
		if len(result.Columns) == 0 {
			result.Kind = "write"
			result.AffectedRows = rows.CommandTag().RowsAffected()
		}
	} else if r.sqlConn != nil {
		if !ast.ReadOnly {
			var resultSQL sql.Result
			if r.myTx != nil {
				resultSQL, err = r.myTx.ExecContext(ctx, query, opt.Args...)
			} else {
				resultSQL, err = r.sqlConn.ExecContext(ctx, query, opt.Args...)
			}
			if err != nil {
				return nil, err
			}
			result.Kind = "write"
			result.AffectedRows, err = resultSQL.RowsAffected()
			if err != nil {
				return nil, err
			}
		} else {
			var rows *sql.Rows
			if r.myTx != nil {
				rows, err = r.myTx.QueryContext(ctx, query, opt.Args...)
			} else {
				rows, err = r.sqlConn.QueryContext(ctx, query, opt.Args...)
			}
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			result.Columns, err = rows.Columns()
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				if len(result.Rows) >= opt.MaxRows {
					result.Truncated = true
					break
				}
				values := make([]any, len(result.Columns))
				pointers := make([]any, len(values))
				for i := range values {
					pointers[i] = &values[i]
				}
				if err = rows.Scan(pointers...); err != nil {
					return nil, err
				}
				for i, value := range values {
					if b, ok := value.([]byte); ok {
						values[i] = string(b)
					}
				}
				result.Rows = append(result.Rows, values)
			}
			if err = rows.Err(); err != nil {
				return nil, err
			}
		}
	} else {
		return nil, fmt.Errorf("数据库连接未建立")
	}
	result.DurationMS = time.Since(start).Milliseconds()
	return result, nil
}
