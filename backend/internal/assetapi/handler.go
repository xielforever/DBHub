// Package assetapi 数据资产模块：元数据同步、资产目录与详情、人工标注、
// 收藏、全局搜索与查询热度。只读元数据库 + 请求级目标库短连接。
package assetapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/target"
)

// Handler 数据资产处理器。
type Handler struct {
	conns   *db.ConnectionRepository
	tunnels *db.TunnelRepository
	meta    *db.MetadataRepository
	users   *db.UserRepository
	history *db.HistoryRepository
	log     *slog.Logger
}

func NewHandler(conns *db.ConnectionRepository, tunnels *db.TunnelRepository,
	meta *db.MetadataRepository, users *db.UserRepository,
	history *db.HistoryRepository, log *slog.Logger) *Handler {
	return &Handler{conns: conns, tunnels: tunnels, meta: meta, users: users,
		history: history, log: log}
}

// ---------------- 同步字典 ----------------

type syncResult struct {
	ConnectionID   int64        `json:"connection_id"`
	ConnectionName string       `json:"connection_name"`
	Type           string       `json:"type"`
	Skipped        bool         `json:"skipped,omitempty"` // 非关系型数据源跳过
	Databases      []string     `json:"databases"`
	Tables         int          `json:"tables"`
	Views          int          `json:"views"`
	Errors         []string     `json:"errors,omitempty"`
	SkippedDBs     []string     `json:"skipped_databases,omitempty"`
	ByDatabase     []dbSyncStat `json:"by_database"`
}

type dbSyncStat struct {
	Database string `json:"database"`
	Schemas  int    `json:"schemas"`
	Tables   int    `json:"tables"`
	Views    int    `json:"views"`
}

type syncSummary struct {
	Connections []*syncResult `json:"connections"`
	Tables      int           `json:"tables"`
	Views       int           `json:"views"`
}

// Sync POST /api/v1/assets/sync （请求体可空：空=同步全部关系型数据源；
// 传 {"connection_id":N} 仅同步指定数据源）。
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	// 请求体允许为空（EOF）；有内容时必须是合法 JSON。
	var in struct {
		ConnectionID int64 `json:"connection_id"`
	}
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			httpx.Fail(w, httpx.BadRequest("请求参数格式错误: "+err.Error()))
			return
		}
	}

	var targets []*db.Connection
	if in.ConnectionID > 0 {
		conn, _, err := h.conns.GetSecrets(r.Context(), in.ConnectionID)
		if err != nil {
			httpx.Fail(w, mapNotFound(err))
			return
		}
		targets = []*db.Connection{conn}
	} else {
		list, err := h.conns.List(r.Context(), "", "")
		if err != nil {
			httpx.Fail(w, httpx.Internal("加载数据源失败", err))
			return
		}
		for i := range list {
			targets = append(targets, &list[i])
		}
	}

	// 同步整体上限 4 分钟（串行采集）。
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()

	summary := &syncSummary{Connections: []*syncResult{}}
	for _, conn := range targets {
		res := h.syncConnection(ctx, conn)
		summary.Connections = append(summary.Connections, res)
		summary.Tables += res.Tables
		summary.Views += res.Views
	}
	httpx.OK(w, summary)
}

// syncConnection 采集单个数据源（非关系型标记 skipped；连接失败记入 errors）。
func (h *Handler) syncConnection(ctx context.Context, conn *db.Connection) *syncResult {
	res := &syncResult{
		ConnectionID:   conn.ID,
		ConnectionName: conn.Name,
		Type:           conn.Type,
		Databases:      []string{},
		ByDatabase:     []dbSyncStat{},
	}
	if conn.Type != "postgres" && conn.Type != "mysql" {
		res.Skipped = true
		return res
	}
	full, password, err := h.conns.GetSecrets(ctx, conn.ID)
	if err != nil {
		res.Errors = []string{"读取数据源口令失败: " + safeErr(err)}
		return res
	}
	conn = full
	var tunnel *db.SSHTunnel
	if conn.SSHTunnelID != nil {
		t, err := h.tunnels.GetSecrets(ctx, *conn.SSHTunnelID)
		if err != nil {
			res.Errors = []string{"读取 SSH 隧道失败: " + safeErr(err)}
			return res
		}
		tunnel = t
	}

	openConn := *conn
	rootHandle, err := target.OpenRelational(ctx, &openConn, password, tunnel)
	if err != nil {
		res.Errors = append(res.Errors, "打开目标数据库失败: "+safeErr(err))
		return res
	}
	dbNames, err := target.Databases(ctx, rootHandle)
	rootHandle.Close()
	if err != nil {
		res.Errors = append(res.Errors, "获取数据库列表失败: "+safeErr(err))
		return res
	}

	for _, item := range dbNames {
		dbName := item.Name
		if !target.IsUserDatabase(conn.Type, dbName) {
			res.SkippedDBs = append(res.SkippedDBs, dbName)
			continue
		}
		dbConn := *conn
		dbConn.Database = dbName
		handle, err := target.OpenRelational(ctx, &dbConn, password, tunnel)
		if err != nil {
			res.Errors = append(res.Errors, dbName+": 连接失败 "+safeErr(err))
			continue
		}
		stat := dbSyncStat{Database: dbName}
		schemas := []string{""}
		if conn.Type == "postgres" {
			list, err := target.Schemas(ctx, handle)
			if err != nil {
				handle.Close()
				res.Errors = append(res.Errors, dbName+": 获取 schema 失败 "+safeErr(err))
				continue
			}
			schemas = schemas[:0]
			for _, s := range list {
				if target.IsUserSchema(conn.Type, s) {
					schemas = append(schemas, s)
				}
			}
		} else {
			schemas = []string{dbName}
		}
		keep := make([]string, 0)
		for _, schema := range schemas {
			metas, err := target.IntrospectSchema(ctx, handle, dbName, schema)
			if err != nil {
				res.Errors = append(res.Errors, dbName+"/"+schema+": "+safeErr(err))
				continue
			}
			stat.Schemas++
			for i := range metas {
				m := &metas[i]
				if schema == "" && conn.Type == "mysql" {
					m.Schema = dbName
				}
				if m.Schema == "" {
					m.Schema = schema
				}
				snap := &db.MetaSnapshot{
					ConnectionID:  conn.ID,
					DatabaseName:  dbName,
					SchemaName:    m.Schema,
					TableName:     m.Name,
					TableType:     m.Type,
					TableComment:  truncate(m.Comment, 512),
					EstimatedRows: m.EstimatedRows,
					DataBytes:     m.DataBytes,
					RawColumns:    jsonRaw(m.Columns),
					RawIndexes:    jsonRaw(m.Indexes),
					RawKeys:       jsonRaw(m.Keys),
					DDLText:       m.DDL,
				}
				if err := h.meta.UpsertSnapshot(ctx, snap); err != nil {
					res.Errors = append(res.Errors, dbName+"."+m.Name+": 快照失败 "+safeErr(err))
					continue
				}
				keep = append(keep, m.Name)
				if m.Type == "view" {
					res.Views++
					stat.Views++
				} else {
					res.Tables++
					stat.Tables++
				}
			}
		}
		handle.Close()
		if err := h.meta.PruneDatabase(ctx, conn.ID, dbName, keep); err != nil {
			h.log.Warn("清理过期快照失败", "error", err)
		}
		res.Databases = append(res.Databases, dbName)
		res.ByDatabase = append(res.ByDatabase, stat)
	}
	return res
}

// ---------------- 概览统计 ----------------

// Overview GET /api/v1/assets/overview
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.meta.Overview(r.Context())
	if err != nil {
		httpx.Fail(w, httpx.Internal("资产统计失败", err))
		return
	}
	httpx.OK(w, o)
}

// ---------------- 资产树 ----------------

type treeConnection struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Environment string   `json:"environment"`
	TableCount  int      `json:"table_count"`
	IsEmpty     bool     `json:"is_empty"`
	Databases   []treeDB `json:"databases"`
}
type treeDB struct {
	Name       string       `json:"name"`
	TableCount int          `json:"table_count"`
	Schemas    []treeSchema `json:"schemas"`
}
type treeSchema struct {
	Name       string      `json:"name"`
	TableCount int         `json:"table_count"`
	Tables     []treeTable `json:"tables"`
}
type treeTable struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Tree GET /api/v1/assets/tree?connection_id=
func (h *Handler) Tree(w http.ResponseWriter, r *http.Request) {
	connID, _ := strconv.ParseInt(r.URL.Query().Get("connection_id"), 10, 64)
	connections, err := h.conns.List(r.Context(), "", "")
	if err != nil {
		httpx.Fail(w, httpx.Internal("加载数据源失败", err))
		return
	}
	snaps, err := h.meta.ListSnapshots(r.Context(), connID)
	if err != nil {
		httpx.Fail(w, httpx.Internal("加载快照失败", err))
		return
	}
	type snapKey struct{ conn int64; db, schema, table string }
	_ = snapKey{}
	byConn := map[int64]*treeConnection{}
	order := []int64{}
	for _, c := range connections {
		if connID > 0 && c.ID != connID {
			continue
		}
		byConn[c.ID] = &treeConnection{ID: c.ID, Name: c.Name, Type: c.Type, Environment: c.Environment}
		order = append(order, c.ID)
	}
	// 两级索引：(连接,库)→库下标；(连接,库,schema)→schema 下标
	type dk struct{ conn int64; db string }
	type sk struct{ conn int64; db, schema string }
	dbIdx := map[dk]int{}
	scIdx := map[sk]int{}
	for _, s := range snaps {
		root := byConn[s.ConnectionID]
		if root == nil {
			continue
		}
		dKey := dk{s.ConnectionID, s.DatabaseName}
		di, ok := dbIdx[dKey]
		if !ok {
			root.Databases = append(root.Databases, treeDB{Name: s.DatabaseName})
			di = len(root.Databases) - 1
			dbIdx[dKey] = di
		}
		d := &root.Databases[di]
		sKey := sk{s.ConnectionID, s.DatabaseName, s.SchemaName}
		si, ok := scIdx[sKey]
		if !ok {
			d.Schemas = append(d.Schemas, treeSchema{Name: s.SchemaName})
			si = len(d.Schemas) - 1
			scIdx[sKey] = si
		}
		d.Schemas[si].Tables = append(d.Schemas[si].Tables,
			treeTable{Name: s.TableName, Type: s.TableType})
		d.Schemas[si].TableCount++
		d.TableCount++
		root.TableCount++
	}
	for _, c := range byConn {
		c.IsEmpty = c.TableCount == 0
	}
	out := make([]treeConnection, 0, len(order))
	for _, id := range order {
		out = append(out, *byConn[id])
	}
	httpx.OK(w, map[string]any{"items": out})
}

// ---------------- 资产列表 ----------------

// ListTables GET /api/v1/assets/tables
func (h *Handler) ListTables(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if page <= 0 {
		page = 1
	}
	f := db.TableListFilter{
		ConnectionID: connID,
		Database:     q.Get("database"),
		Schema:       q.Get("schema"),
		Keyword:      strings.TrimSpace(q.Get("q")),
		ObjectType:   q.Get("type"),
		Sensitivity:  q.Get("sensitivity"),
		NoOwner:      q.Get("no_owner") == "1" || q.Get("no_owner") == "true",
		StarredBy:    0,
		Offset:       (page - 1) * pageSize,
		Limit:        pageSize,
	}
	if q.Get("starred") == "1" || q.Get("starred") == "true" {
		f.StarredBy = c.Subject
	}
	items, total, err := h.meta.ListTables(r.Context(), f)
	if err != nil {
		httpx.Fail(w, httpx.Internal("查询资产列表失败", err))
		return
	}
	// 热度（全量聚合后按 key 映射，单次请求一份缓存）
	heat, err := h.meta.HeatMap(r.Context(), 30)
	if err != nil {
		h.log.Warn("聚合热度失败", "error", err)
	}
	connMap := map[int64]string{}
	if conns, err := h.conns.List(r.Context(), "", ""); err == nil {
		for _, cn := range conns {
			connMap[cn.ID] = cn.Name
		}
	}
	rows := make([]map[string]any, 0, len(items))
	for _, it := range items {
		heatN := heat[db.HeatKeyOf(it.ConnectionID, it.DatabaseName, it.SchemaName, it.TableName)]
		rows = append(rows, map[string]any{
			"snapshot":        it.MetaSnapshot,
			"connection_name": connMap[it.ConnectionID],
			"owner_user_id":   it.OwnerID,
			"owner_name":      it.OwnerName,
			"business_desc":   it.BusinessDesc,
			"tags":            it.Tags,
			"sensitivity":     it.Sensitivity,
			"starred":         it.Starred,
			"query_count_30d": heatN,
			"sensitive_columns": it.SensitiveColCount,
		})
	}
	httpx.OK(w, map[string]any{"items": rows, "total": total, "page": page,
		"page_size": pageSizeOrDefault(pageSize, 50)})
}

// ---------------- 资产详情 ----------------

// TableDetail GET /api/v1/assets/table
func (h *Handler) TableDetail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	database, schema, table := q.Get("database"), q.Get("schema"), q.Get("table")
	if connID <= 0 || database == "" || table == "" {
		httpx.Fail(w, httpx.BadRequest("connection_id / database / table 为必填"))
		return
	}
	snap, err := h.meta.GetSnapshot(r.Context(), connID, database, schema, table)
	if err != nil {
		httpx.Fail(w, mapNotFound(err))
		return
	}
	annos, err := h.meta.ListAnnotations(r.Context(), connID, database)
	if err != nil {
		httpx.Fail(w, httpx.Internal("读取标注失败", err))
		return
	}
	type colAnno struct {
		ColumnName   string   `json:"column_name"`
		OwnerUserID  *int64   `json:"owner_user_id"`
		BusinessDesc string   `json:"business_desc"`
		Tags         []string `json:"tags"`
		Sensitivity  string   `json:"sensitivity"`
	}
	var tableAnno *db.AssetAnnotation
	starred := false
	cols := []colAnno{}
	for i := range annos {
		a := &annos[i]
		if a.SchemaName != schema || a.TableName != table {
			continue
		}
		if a.ColumnName == "" {
			tableAnno = a
			for _, uid := range a.StarredBy {
				if uid == claimsOf(r).Subject {
					starred = true
				}
			}
			continue
		}
		cols = append(cols, colAnno{a.ColumnName, a.OwnerUserID, a.BusinessDesc, a.Tags, a.Sensitivity})
	}
	conn, _ := h.conns.Get(r.Context(), connID)
	connName := ""
	env := "dev"
	if conn != nil {
		connName = conn.Name
		env = conn.Environment
	}
	heat, _ := h.meta.HeatMap(r.Context(), 30)
	httpx.OK(w, map[string]any{
		"snapshot":      snap,
		"connection_name": connName,
		"environment":   env,
		"table_annotation":   tableAnno,
		"column_annotations": cols,
		"starred":            starred,
		"query_count_30d":    heat[db.HeatKeyOf(connID, database, snap.SchemaName, table)],
	})
}

// ---------------- 标注与收藏 ----------------

type annotationRequest struct {
	ConnectionID int64    `json:"connection_id"`
	Database     string   `json:"database"`
	Schema       string   `json:"schema"`
	Table        string   `json:"table"`
	Column       string   `json:"column"`
	OwnerUserID  *int64   `json:"owner_user_id"`
	BusinessDesc string   `json:"business_desc"`
	Tags         []string `json:"tags"`
	Sensitivity  string   `json:"sensitivity"`
}

// PutAnnotation PUT /api/v1/assets/annotations
func (h *Handler) PutAnnotation(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in annotationRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.ConnectionID <= 0 || in.Database == "" || in.Table == "" {
		httpx.Fail(w, httpx.BadRequest("connection_id / database / table 为必填"))
		return
	}
	if len(in.BusinessDesc) > 1000 {
		httpx.Fail(w, httpx.BadRequest("业务说明不能超过 1000 字"))
		return
	}
	if in.Sensitivity == "" {
		in.Sensitivity = "normal"
	}
	if in.Sensitivity != "normal" && in.Sensitivity != "sensitive" && in.Sensitivity != "confidential" {
		httpx.Fail(w, httpx.BadRequest("敏感分级非法"))
		return
	}
	if len(in.Tags) > 8 {
		httpx.Fail(w, httpx.BadRequest("标签最多 8 个"))
		return
	}
	for _, t := range in.Tags {
		if len([]rune(t)) > 20 {
			httpx.Fail(w, httpx.BadRequest("单个标签不能超过 20 字"))
			return
		}
	}
	// Owner 必须为存在的用户
	if in.OwnerUserID != nil {
		if _, err := h.users.GetByID(r.Context(), *in.OwnerUserID); err != nil {
			httpx.Fail(w, httpx.BadRequest("指定的 Owner 不存在"))
			return
		}
	}
	// developer 只能标注自己负责/创建范围？v1 不细化；admin/developer 均开放。
	err := h.meta.UpsertAnnotation(r.Context(), &db.AnnotationInput{
		ConnectionID: in.ConnectionID,
		DatabaseName: in.Database,
		SchemaName:   in.Schema,
		TableName:    in.Table,
		ColumnName:   in.Column,
		OwnerUserID:  in.OwnerUserID,
		BusinessDesc: strings.TrimSpace(in.BusinessDesc),
		Tags:         in.Tags,
		Sensitivity:  in.Sensitivity,
		UpdatedBy:    c.Subject,
	})
	if err != nil {
		httpx.Fail(w, httpx.Internal("保存标注失败", err))
		return
	}
	httpx.OK(w, map[string]any{"ok": true})
}

type starRequest struct {
	ConnectionID int64  `json:"connection_id"`
	Database     string `json:"database"`
	Schema       string `json:"schema"`
	Table        string `json:"table"`
	Star         bool   `json:"star"`
}

// Star POST /api/v1/assets/star
func (h *Handler) Star(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	var in starRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if in.ConnectionID <= 0 || in.Database == "" || in.Table == "" {
		httpx.Fail(w, httpx.BadRequest("connection_id / database / table 为必填"))
		return
	}
	if err := h.meta.SetStar(r.Context(), in.ConnectionID, in.Database,
		in.Schema, in.Table, c.Subject, in.Star); err != nil {
		httpx.Fail(w, httpx.Internal("收藏失败", err))
		return
	}
	httpx.OK(w, map[string]any{"starred": in.Star})
}

// ---------------- 全局搜索 ----------------

// Search GET /api/v1/assets/search?q=
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	c := claimsOf(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := map[string]any{
		"connections": []any{}, "tables": []any{}, "columns": []any{},
		"reports": []any{}, "history": []any{},
	}
	if len([]rune(q)) < 2 {
		httpx.OK(w, out)
		return
	}
	// 连接
	conns, err := h.conns.List(r.Context(), "", q)
	if err == nil {
		connHits := make([]map[string]any, 0, len(conns))
		for _, cn := range conns {
			connHits = append(connHits, map[string]any{
				"id": cn.ID, "name": cn.Name, "type": cn.Type,
				"environment": cn.Environment,
			})
		}
		out["connections"] = connHits
	}
	// 表
	snaps, err := h.meta.ListSnapshots(r.Context(), 0)
	if err != nil {
		httpx.Fail(w, httpx.Internal("搜索失败", err))
		return
	}
	connMap := map[int64]*db.Connection{}
	if all, err := h.conns.List(r.Context(), "", ""); err == nil {
		for i := range all {
			connMap[all[i].ID] = &all[i]
		}
	}
	kw := strings.ToLower(q)
	tableHits := []map[string]any{}
	for _, s := range snaps {
		matchedTable := strings.Contains(strings.ToLower(s.TableName), kw) ||
			strings.Contains(strings.ToLower(s.TableComment), kw)
		if matchedTable && len(tableHits) < 8 {
			tableHits = append(tableHits, map[string]any{
				"connection_id":   s.ConnectionID,
				"connection_name": connNameOf(connMap, s.ConnectionID),
				"environment":     connEnvOf(connMap, s.ConnectionID),
				"database":        s.DatabaseName, "schema": s.SchemaName,
				"name": s.TableName, "type": s.TableType, "comment": s.TableComment,
			})
		}
	}
	out["tables"] = tableHits
	// 字段匹配走 JSONB 检索（ListSnapshots 为轻量结果，不含 raw_columns）。
	columnHits := []map[string]any{}
	if colRows, err := h.meta.SearchColumns(r.Context(), q, 8); err == nil {
		for _, hcol := range colRows {
			columnHits = append(columnHits, map[string]any{
				"connection_id":   hcol.ConnectionID,
				"connection_name": connNameOf(connMap, hcol.ConnectionID),
				"database":        hcol.DatabaseName,
				"schema":          hcol.SchemaName,
				"table":           hcol.TableName,
				"column":          hcol.ColumnName,
				"data_type":       hcol.DataType,
				"comment":         hcol.Comment,
			})
		}
	}
	out["columns"] = columnHits
	// 查询历史
	hist, err := h.history.SearchBySQL(r.Context(), c.Subject, c.Role == "admin", q, 6)
	if err == nil {
		out["history"] = hist
	}
	// 报表分组在 M4 注入（保持空数组）。
	httpx.OK(w, out)
}

// BriefUsers GET /api/v1/users/brief
func (h *Handler) BriefUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.users.ListBrief(r.Context())
	if err != nil {
		httpx.Fail(w, httpx.Internal("加载用户失败", err))
		return
	}
	httpx.OK(w, map[string]any{"items": users})
}

// ---------------- 辅助 ----------------

func claimsOf(r *http.Request) *auth.Claims {
	c, _ := auth.ClaimsFromContext(r.Context())
	return c
}

func jsonRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func pageSizeOrDefault(v, def int) int {
	if v <= 0 || v > 200 {
		return def
	}
	return v
}

func connNameOf(m map[int64]*db.Connection, id int64) string {
	if c := m[id]; c != nil {
		return c.Name
	}
	return ""
}

func connEnvOf(m map[int64]*db.Connection, id int64) string {
	if c := m[id]; c != nil {
		return c.Environment
	}
	return "dev"
}

func mapNotFound(err error) error {
	if errors.Is(err, db.ErrNotFound) {
		return &httpx.AppError{Code: httpx.CodeNotFound, Message: "记录不存在"}
	}
	return httpx.Internal("数据访问失败", err)
}

func safeErr(err error) string {
	msg := strings.SplitN(err.Error(), "\n", 2)[0]
	if len(msg) > 200 {
		return msg[:200]
	}
	return msg
}
