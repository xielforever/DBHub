package lineage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
)

// Service 血缘解析服务：定时扫描 sys_query_history → 解析实际 SQL AST→ 落库 sys_meta_lineage
type Service struct {
	lineage *db.LineageRepository
	history *db.HistoryRepository
	conns   *db.ConnectionRepository
	meta    *db.MetadataRepository
	log     *slog.Logger
}

func NewService(lineage *db.LineageRepository, history *db.HistoryRepository, conns *db.ConnectionRepository, meta *db.MetadataRepository, log *slog.Logger) *Service {
	return &Service{lineage: lineage, history: history, conns: conns, meta: meta, log: log}
}

// ParseRecentHistory 增量解析最近 limit 条成功历史，返回新增边数
func (s *Service) ParseRecentHistory(ctx context.Context, limit int) (int, error) {
	if db.ActorID(ctx) == 0 {
		actors, e := s.history.ActiveActors(ctx)
		if e != nil {
			return 0, e
		}
		total := 0
		for i := range actors {
			if e = ctx.Err(); e != nil {
				return total, e
			}
			n, err := s.ParseRecentHistory(context.WithValue(db.WithWorkspace(ctx, actors[i].WorkspaceID), auth.ClaimsContextKey, &actors[i]), limit)
			if err != nil {
				return total, err
			}
			total += n
		}
		return total, nil
	}
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	// 取最近成功历史（不区分用户，admin 视角）
	hists, _, err := s.history.List(ctx, 0, true, 0, "success", limit, 0)
	if err != nil {
		return 0, fmt.Errorf("list history failed: %w", err)
	}
	inserted := 0
	for _, h := range hists {
		n, err := s.parseOne(ctx, &h)
		if err != nil {
			s.log.Warn("解析单条历史失败", "history_id", h.ID, "error", err)
			continue
		}
		inserted += n
	}
	return inserted, nil
}

func (s *Service) parseOne(ctx context.Context, h *db.QueryHistory) (int, error) {
	sqlText := strings.TrimSpace(h.SQLText)
	if sqlText == "" {
		return 0, nil
	}
	// 尝试获取连接类型以选择解析器（失败则按通用）
	dbType := ""
	if s.conns != nil {
		if conn, err := s.conns.Get(ctx, h.ConnectionID); err == nil && conn != nil {
			dbType = conn.Type // postgres | mysql | redis
		}
	}
	if dbType == "redis" {
		return 0, nil // Redis 无表血缘
	}

	pr, parseErr := ParseTables(sqlText, dbType)
	if parseErr != nil {
		return 0, parseErr
	}
	fp := Fingerprint(sqlText)

	// 只有明确的来源 → 写入目标构成数据血缘；SELECT 共现不生成虚假方向。
	// 若有目标（INSERT/CREATE），则 source → target
	var edges []struct {
		up   TableRef
		down TableRef
	}
	if len(pr.Targets) > 0 {
		for _, src := range pr.Sources {
			for _, tgt := range pr.Targets {
				edges = append(edges, struct {
					up   TableRef
					down TableRef
				}{up: src, down: tgt})
			}
		}
		// 若 INSERT 无来源（如 VALUES），则不建边
	}

	if len(edges) == 0 {
		return 0, nil
	}

	count := 0
	for _, e := range edges {
		upConn, downConn := h.ConnectionID, h.ConnectionID
		upDB, downDB := h.DatabaseName, h.DatabaseName
		if e.up.Database != "" {
			upDB = e.up.Database
		}
		if e.down.Database != "" {
			downDB = e.down.Database
		}
		upSchema, downSchema := "public", "public"
		if dbType == "mysql" {
			upSchema = upDB
			downSchema = downDB
		}
		if e.up.Schema != "" {
			upSchema = e.up.Schema
		}
		if e.down.Schema != "" {
			downSchema = e.down.Schema
		}
		upTable, downTable := e.up.Table, e.down.Table

		// 过滤自环
		if upTable == downTable && upConn == downConn && upDB == downDB && upSchema == downSchema {
			continue
		}

		le := &db.LineageEdge{
			UpstreamConnectionID:   upConn,
			UpstreamDatabase:       upDB,
			UpstreamSchema:         upSchema,
			UpstreamTable:          upTable,
			DownstreamConnectionID: downConn,
			DownstreamDatabase:     downDB,
			DownstreamSchema:       downSchema,
			DownstreamTable:        downTable,
			EdgeKind:               "sql",
			Fingerprint:            fp,
			SourceHistoryID:        &h.ID,
		}
		if err := s.lineage.UpsertEdge(ctx, le); err != nil {
			s.log.Warn("upsert lineage edge failed", "history_id", h.ID, "up", upTable, "down", downTable, "error", err)
			continue
		}
		count++
	}
	return count, nil
}

// ParseHistoryID 供外部触发单条解析（测试/手工刷新）
func (s *Service) ParseHistoryID(ctx context.Context, historyID int64) (int, error) {
	// 简化：直接查最近 1000 条中找到该 ID
	hists, _, err := s.history.List(ctx, 0, true, 0, "", 1000, 0)
	if err != nil {
		return 0, err
	}
	for _, h := range hists {
		if h.ID == historyID {
			return s.parseOne(ctx, &h)
		}
	}
	return 0, fmt.Errorf("history %d not found", historyID)
}

// BackfillFK 将外键血缘也落入缓存（按当前快照全量扫描）
func (s *Service) BackfillFK(ctx context.Context) (int, error) {
	if db.ActorID(ctx) == 0 {
		actors, e := s.history.ActiveActors(ctx)
		if e != nil {
			return 0, e
		}
		total := 0
		for i := range actors {
			n, err := s.BackfillFK(context.WithValue(db.WithWorkspace(ctx, actors[i].WorkspaceID), auth.ClaimsContextKey, &actors[i]))
			if err != nil {
				return total, err
			}
			total += n
		}
		return total, nil
	}
	if s.meta == nil {
		return 0, fmt.Errorf("meta repo is nil")
	}
	snaps, err := s.meta.ListSnapshots(ctx, 0)
	if err != nil {
		return 0, err
	}
	// 预加载完整快照的 raw_keys
	count := 0
	for i := range snaps {
		snap := &snaps[i]
		full, err := s.meta.GetSnapshot(ctx, snap.ConnectionID, snap.DatabaseName, snap.SchemaName, snap.TableName)
		if err != nil || full == nil || len(full.RawKeys) == 0 {
			continue
		}
		// 解析 raw_keys
		var keys []struct {
			Name       string   `json:"name"`
			Kind       string   `json:"kind"`
			Columns    []string `json:"columns"`
			RefSchema  string   `json:"ref_schema"`
			RefTable   string   `json:"ref_table"`
			RefColumns []string `json:"ref_columns"`
		}
		// 使用 encoding/json
		if err := jsonUnmarshal(full.RawKeys, &keys); err != nil {
			continue
		}
		for _, k := range keys {
			if k.Kind != "foreign_key" || k.RefTable == "" {
				continue
			}
			// 上游为被引用的表
			upConn := snap.ConnectionID
			upDB := snap.DatabaseName
			upSchema := k.RefSchema
			if upSchema == "" {
				upSchema = snap.SchemaName
			}
			upTable := k.RefTable
			le := &db.LineageEdge{
				UpstreamConnectionID:   upConn,
				UpstreamDatabase:       upDB,
				UpstreamSchema:         upSchema,
				UpstreamTable:          upTable,
				DownstreamConnectionID: snap.ConnectionID,
				DownstreamDatabase:     snap.DatabaseName,
				DownstreamSchema:       snap.SchemaName,
				DownstreamTable:        snap.TableName,
				EdgeKind:               "fk",
				Fingerprint:            db.LineageFingerprint(upConn, upDB, upSchema, upTable, snap.ConnectionID, snap.DatabaseName, snap.SchemaName, snap.TableName, "fk", k.Name),
			}
			if err := s.lineage.UpsertEdge(ctx, le); err != nil {
				continue
			}
			count++
		}
	}
	return count, nil
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// StartScheduler 启动定时增量解析（每 interval 一次，首次延迟 10s）
func (s *Service) StartScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		// 首次延迟
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			start := time.Now()
			n, err := s.ParseRecentHistory(ctx, 500)
			if err != nil {
				s.log.Warn("定时血缘解析失败", "error", err)
			} else if n > 0 {
				s.log.Info("定时血缘解析完成", "edges", n, "elapsed", time.Since(start))
			}
			// 额外：每日一次全量 FK 回填（简化：每次都尝试，幂等）
			if _, err := s.BackfillFK(ctx); err != nil {
				s.log.Warn("FK 回填失败", "error", err)
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}
