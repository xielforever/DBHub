package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrSyncBusy = errors.New("该数据源正在采集，请稍后重试")
var ErrConnectionChanged = errors.New("数据源配置已变化，请重新发起操作")

// MetadataSync 的事务锁跨处理器和实例生效；采集和发布共用一个池连接，避免锁占满连接池后再申请连接。
type MetadataSync struct {
	tx            pgx.Tx
	connectionID  int64
	configVersion time.Time
}

func (r *MetadataRepository) BeginSync(ctx context.Context, connectionID int64, version time.Time) (*MetadataSync, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	var locked bool
	err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended(current_schema() || ':dbhub:metadata-sync:' || $1::text, 0))`, connectionID).Scan(&locked)
	if err != nil || !locked {
		_ = tx.Rollback(ctx)
		if err != nil {
			return nil, err
		}
		return nil, ErrSyncBusy
	}
	var current time.Time
	err = tx.QueryRow(ctx, `SELECT updated_at FROM sys_connections WHERE id=$1`, connectionID).Scan(&current)
	if err != nil || !current.Equal(version) {
		_ = tx.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return nil, ErrConnectionChanged
	}
	return &MetadataSync{tx: tx, connectionID: connectionID, configVersion: version}, nil
}

func (s *MetadataSync) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.tx.Rollback(ctx) // 提交、失败、取消和连接断开均释放事务锁。
}

// Publish 仅接受完整采集；快照写入和清理在同一事务中对读者一次可见。
func (s *MetadataSync) Publish(ctx context.Context, snapshots []*MetaSnapshot) error {
	var current time.Time
	if err := s.tx.QueryRow(ctx, `SELECT updated_at FROM sys_connections WHERE id=$1 FOR SHARE`, s.connectionID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !current.Equal(s.configVersion) {
		return ErrConnectionChanged
	}
	type object struct {
		Database string `json:"database"`
		Schema   string `json:"schema"`
		Table    string `json:"table"`
	}
	objects := make([]object, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.ConnectionID != s.connectionID {
			return fmt.Errorf("采集对象所属数据源不匹配")
		}
		if err := upsertSnapshot(ctx, s.tx, snapshot); err != nil {
			return err
		}
		objects = append(objects, object{snapshot.DatabaseName, snapshot.SchemaName, snapshot.TableName})
	}
	keep, err := json.Marshal(objects)
	if err != nil {
		return err
	}
	_, err = s.tx.Exec(ctx, `DELETE FROM sys_meta_snapshots s WHERE connection_id=$1 AND NOT EXISTS (
		SELECT 1 FROM jsonb_to_recordset($2::jsonb) AS k(database text, schema text, "table" text)
		WHERE k.database=s.database_name AND k.schema=s.schema_name AND k."table"=s.table_name)`, s.connectionID, string(keep))
	if err != nil {
		return err
	}
	return s.tx.Commit(ctx)
}
