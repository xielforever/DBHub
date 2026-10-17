package target

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/connector"
	"github.com/user/dbhub/internal/db"
)

// RelDB owns one real target connection. It is never backed by demonstration data.
type RelDB struct {
	Conn           *db.Connection
	Password       string
	Tunnel         *db.SSHTunnel
	pgConn         *pgx.Conn
	sqlDB          *sql.DB
	sqlConn        *sql.Conn
	pgTx           pgx.Tx
	myTx           *sql.Tx
	readOnly       bool
	closeTransport func() error
}

func (r *RelDB) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if r.pgTx != nil {
		_ = r.pgTx.Rollback(ctx)
		r.pgTx = nil
	}
	if r.myTx != nil {
		_ = r.myTx.Rollback()
		r.myTx = nil
	}
	if r.pgConn != nil {
		_ = r.pgConn.Close(ctx)
	}
	if r.sqlConn != nil {
		_ = r.sqlConn.Close()
	}
	if r.sqlDB != nil {
		_ = r.sqlDB.Close()
	}
	if r.closeTransport != nil {
		_ = r.closeTransport()
	}
}

type RedisClient struct {
	Client         *redis.Client
	Conn           *db.Connection
	closeTransport func() error
}

func (r *RedisClient) Close() {
	if r.Client != nil {
		_ = r.Client.Close()
	}
	if r.closeTransport != nil {
		_ = r.closeTransport()
	}
}

// Driver-neutral result types retain the public API shape.
type DatabaseItem = connector.DatabaseItem
type TableItem = connector.TableItem
type ColumnItem = connector.ColumnItem
type ColumnMeta = connector.ColumnMeta
type IndexMeta = connector.IndexMeta
type KeyMeta = connector.KeyMeta
type TableMeta = connector.TableMeta
type ExecResult = connector.ExecResult
type TestInfo = connector.TestInfo
type RedisInfo = connector.RedisInfo
type RedisKeyItem = connector.RedisKeyItem
type RedisValue = connector.RedisValue
