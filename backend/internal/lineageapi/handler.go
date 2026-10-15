package lineageapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/lineage"
)

// Handler 血缘 handler（表级，上游/下游/影响面），Step28 后端解析落库 + 实时兜底
// 优先读取 sys_meta_lineage（定时 pg_query_go/vitess 解析 sys_query_history 落库），
// 缓存未命中或空时回退到 FK+历史+报表实时计算，保证可用性
type Handler struct {
	meta    *db.MetadataRepository
	conns   *db.ConnectionRepository
	history *db.HistoryRepository
	reports *db.ReportRepository
	dash    *db.DashboardRepository
	lineage *db.LineageRepository
	svc     *lineage.Service
	log     *slog.Logger
}

func NewHandler(meta *db.MetadataRepository, history *db.HistoryRepository, reports *db.ReportRepository, dashboards *db.DashboardRepository, lineageRepo *db.LineageRepository, svc *lineage.Service, log *slog.Logger, conns *db.ConnectionRepository) *Handler {
	return &Handler{meta: meta, history: history, reports: reports, dash: dashboards, lineage: lineageRepo, svc: svc, log: log, conns: conns}
}

type lineageNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // table | view | report | dashboard
	DB   string `json:"db"`
	Desc string `json:"desc"`
}

type lineageEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // fk | sql | report
}

type lineageResp struct {
	Nodes []lineageNode `json:"nodes"`
	Edges []lineageEdge `json:"edges"`
	// 兼容旧前端：upstream/downstream 计数
	Upstream   int `json:"upstream"`
	Downstream int `json:"downstream"`
	// 新：影响面
	Impact map[string]int `json:"impact"`
	// 新增：是否来自缓存
	Cached bool `json:"cached"`
}

// table key: connection_id.database.schema.table (lower)
func tableKey(conn int64, dbName, schema, table string) string {
	return strings.ToLower(strings.Join([]string{strconv.FormatInt(conn, 10), dbName, schema, table}, "."))
}

// Lineage GET /api/v1/assets/lineage?connection_id=&database=&schema=&table=&depth=1|2|3
func (h *Handler) Lineage(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	_ = claims

	q := r.URL.Query()
	connID, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	dbName := strings.TrimSpace(q.Get("database"))
	schema := strings.TrimSpace(q.Get("schema"))
	table := strings.TrimSpace(q.Get("table"))
	if table == "" {
		httpx.Fail(w, httpx.BadRequest("table 必填"))
		return
	}
	depthStr := strings.TrimSpace(q.Get("depth"))
	depth := 2
	if depthStr == "1" {
		depth = 1
	} else if depthStr == "3" || depthStr == "all" {
		depth = 3
	}

	// 加载快照用于中心节点解析与报表下游
	snaps, err := h.meta.ListSnapshots(r.Context(), 0)
	if err != nil {
		httpx.Fail(w, httpx.Internal("加载快照失败", err))
		return
	}
	snapByKey := map[string]*db.MetaSnapshot{}
	snapByTable := map[string][]*db.MetaSnapshot{}
	for i := range snaps {
		s := &snaps[i]
		k := tableKey(s.ConnectionID, s.DatabaseName, s.SchemaName, s.TableName)
		snapByKey[k] = s
		low := strings.ToLower(s.TableName)
		snapByTable[low] = append(snapByTable[low], s)
	}
	targetKey := tableKey(connID, dbName, schema, table)
	targetSnap, ok := snapByKey[targetKey]
	if !ok {
		cands := snapByTable[strings.ToLower(table)]
		if len(cands) == 0 {
			httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "目标表不存在或未同步"})
			return
		}
		for _, c := range cands {
			if connID > 0 && c.ConnectionID != connID {
				continue
			}
			if dbName != "" && !strings.EqualFold(c.DatabaseName, dbName) {
				continue
			}
			targetSnap = c
			targetKey = tableKey(c.ConnectionID, c.DatabaseName, c.SchemaName, c.TableName)
			break
		}
		if targetSnap == nil {
			targetSnap = cands[0]
			targetKey = tableKey(targetSnap.ConnectionID, targetSnap.DatabaseName, targetSnap.SchemaName, targetSnap.TableName)
		}
	}

	// 尝试缓存路径：sys_meta_lineage
	if h.lineage != nil {
		if cached, ok := h.lineageCached(r, targetSnap, targetKey, snapByKey, snapByTable, depth); ok && len(cached.Nodes) > 1 {
			httpx.OK(w, cached)
			return
		}
	}

	// 回退：实时计算
	resp := h.lineageRealtime(r, targetSnap, targetKey, snapByKey, snapByTable, snaps, depth)
	httpx.OK(w, resp)
}

// lineageCached 优先从 sys_meta_lineage 读取，利用 lineage.Service 定时解析的 pg_query_go/vitess 结果
func (h *Handler) lineageCached(r *http.Request, targetSnap *db.MetaSnapshot, targetKey string, snapByKey map[string]*db.MetaSnapshot, snapByTable map[string][]*db.MetaSnapshot, depth int) (lineageResp, bool) {
	// BFS 基于缓存表
	upstream := map[string]lineageNode{}
	downstream := map[string]lineageNode{}
	edges := []lineageEdge{}
	visited := map[string]bool{targetKey: true}
	type qItem struct {
		key  string
		dep  int
		snap *db.MetaSnapshot
	}
	queue := []qItem{{key: targetKey, dep: 0, snap: targetSnap}}

	// 为快速映射，预建 edge 缓存
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.dep >= depth {
			continue
		}
		// 上游：cur 为下游
		if h.lineage != nil {
			ups, _ := h.lineage.ListDirectEdges(r.Context(), cur.snap.ConnectionID, cur.snap.DatabaseName, cur.snap.SchemaName, cur.snap.TableName, "upstream")
			for _, e := range ups {
				upKey := tableKey(e.UpstreamConnectionID, e.UpstreamDatabase, e.UpstreamSchema, e.UpstreamTable)
				if !visited[upKey] {
					visited[upKey] = true
					// 尝试从快照取类型
					typ := "table"
					dbName := e.UpstreamDatabase
					if snap, ok := snapByKey[upKey]; ok {
						typ = snap.TableType
						dbName = snap.DatabaseName
					} else if cands := snapByTable[strings.ToLower(e.UpstreamTable)]; len(cands) > 0 {
						typ = cands[0].TableType
						dbName = cands[0].DatabaseName
					}
					desc := "SQL 血缘"
					if e.EdgeKind == "fk" {
						desc = "外键"
					}
					upstream[upKey] = lineageNode{ID: upKey, Name: e.UpstreamTable, Type: typ, DB: dbName, Desc: desc}
					// 入队继续扩展
					var upSnap *db.MetaSnapshot
					if s, ok := snapByKey[upKey]; ok {
						upSnap = s
					} else if cands := snapByTable[strings.ToLower(e.UpstreamTable)]; len(cands) > 0 {
						upSnap = cands[0]
						upKey = tableKey(upSnap.ConnectionID, upSnap.DatabaseName, upSnap.SchemaName, upSnap.TableName)
					} else {
						// 虚拟节点
						upSnap = &db.MetaSnapshot{ConnectionID: e.UpstreamConnectionID, DatabaseName: e.UpstreamDatabase, SchemaName: e.UpstreamSchema, TableName: e.UpstreamTable, TableType: typ}
					}
					queue = append(queue, qItem{key: upKey, dep: cur.dep + 1, snap: upSnap})
				}
				edges = append(edges, lineageEdge{From: upKey, To: cur.key, Kind: e.EdgeKind})
			}
			downs, _ := h.lineage.ListDirectEdges(r.Context(), cur.snap.ConnectionID, cur.snap.DatabaseName, cur.snap.SchemaName, cur.snap.TableName, "downstream")
			for _, e := range downs {
				downKey := tableKey(e.DownstreamConnectionID, e.DownstreamDatabase, e.DownstreamSchema, e.DownstreamTable)
				if !visited[downKey] {
					visited[downKey] = true
					typ := "table"
					dbName := e.DownstreamDatabase
					if snap, ok := snapByKey[downKey]; ok {
						typ = snap.TableType
						dbName = snap.DatabaseName
					} else if cands := snapByTable[strings.ToLower(e.DownstreamTable)]; len(cands) > 0 {
						typ = cands[0].TableType
						dbName = cands[0].DatabaseName
					}
					desc := "SQL 血缘"
					if e.EdgeKind == "fk" {
						desc = "外键"
					}
					downstream[downKey] = lineageNode{ID: downKey, Name: e.DownstreamTable, Type: typ, DB: dbName, Desc: desc}
					var downSnap *db.MetaSnapshot
					if s, ok := snapByKey[downKey]; ok {
						downSnap = s
					} else if cands := snapByTable[strings.ToLower(e.DownstreamTable)]; len(cands) > 0 {
						downSnap = cands[0]
						downKey = tableKey(downSnap.ConnectionID, downSnap.DatabaseName, downSnap.SchemaName, downSnap.TableName)
					} else {
						downSnap = &db.MetaSnapshot{ConnectionID: e.DownstreamConnectionID, DatabaseName: e.DownstreamDatabase, SchemaName: e.DownstreamSchema, TableName: e.DownstreamTable, TableType: typ}
					}
					queue = append(queue, qItem{key: downKey, dep: cur.dep + 1, snap: downSnap})
				}
				edges = append(edges, lineageEdge{From: cur.key, To: downKey, Kind: e.EdgeKind})
			}
		}
	}

	// 若缓存完全无边，返回 miss
	if len(edges) == 0 && len(upstream) == 0 && len(downstream) == 0 {
		return lineageResp{}, false
	}

	// 补充报表下游（动态，不入缓存）
	if h.reports != nil {
		if items, _, err := h.reports.List(r.Context(), db.ReportFilter{Limit: 100, Offset: 0}); err == nil {
			for _, rep := range items {
				if h.reportUses(r, rep, targetSnap) {
					id := "report." + strconv.FormatInt(rep.ID, 10)
					if !visited[id] {
						visited[id] = true
						downstream[id] = lineageNode{ID: id, Name: rep.Name, Type: "report", DB: rep.DatabaseName, Desc: "报表 #" + strconv.FormatInt(rep.ID, 10)}
						edges = append(edges, lineageEdge{From: targetKey, To: id, Kind: "report"})
					}
				}
			}
		}
	}

	upstreamNodes := make([]lineageNode, 0, len(upstream))
	for _, n := range upstream {
		upstreamNodes = append(upstreamNodes, n)
	}
	downstreamNodes := make([]lineageNode, 0, len(downstream))
	for _, n := range downstream {
		downstreamNodes = append(downstreamNodes, n)
	}
	nodes := []lineageNode{}
	nodes = append(nodes, upstreamNodes...)
	nodes = append(nodes, lineageNode{ID: targetKey, Name: targetSnap.TableName, Type: targetSnap.TableType, DB: targetSnap.DatabaseName, Desc: "当前"})
	nodes = append(nodes, downstreamNodes...)
	return lineageResp{
		Nodes:      nodes,
		Edges:      edges,
		Upstream:   len(upstreamNodes),
		Downstream: len(downstreamNodes),
		Impact: map[string]int{
			"upstream":           len(upstreamNodes),
			"downstream":         len(downstreamNodes),
			"downstream_reports": countType(downstreamNodes, "report"),
			"downstream_tables":  countType(downstreamNodes, "table"),
			"downstream_views":   countType(downstreamNodes, "view"),
			"total":              len(nodes),
		},
		Cached: true,
	}, true
}

// lineageRealtime 回退：实时 FK + SQL + 报表
func (h *Handler) lineageRealtime(r *http.Request, targetSnap *db.MetaSnapshot, targetKey string, snapByKey map[string]*db.MetaSnapshot, snapByTable map[string][]*db.MetaSnapshot, snaps []db.MetaSnapshot, depth int) lineageResp {
	fullSnaps := map[string]*db.MetaSnapshot{}
	if full, err := h.meta.GetSnapshot(r.Context(), targetSnap.ConnectionID, targetSnap.DatabaseName, targetSnap.SchemaName, targetSnap.TableName); err == nil {
		fullSnaps[targetKey] = full
	}
	upstream := map[string]lineageNode{}
	downstream := map[string]lineageNode{}
	edges := []lineageEdge{}
	visited := map[string]bool{targetKey: true}
	queue := []struct {
		key  string
		dep  int
		snap *db.MetaSnapshot
	}{{key: targetKey, dep: 0, snap: targetSnap}}

	var reports []db.Report
	if h.reports != nil {
		if items, _, err := h.reports.List(r.Context(), db.ReportFilter{Limit: 100, Offset: 0}); err == nil {
			reports = items
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.dep >= depth {
			continue
		}
		full := fullSnaps[cur.key]
		if full == nil && cur.snap != nil {
			if f, err := h.meta.GetSnapshot(r.Context(), cur.snap.ConnectionID, cur.snap.DatabaseName, cur.snap.SchemaName, cur.snap.TableName); err == nil {
				full = f
				fullSnaps[cur.key] = f
			}
		}
		if full != nil && len(full.RawKeys) > 0 {
			var rawKeys []metaKey
			if err := parseKeys(full.RawKeys, &rawKeys); err == nil {
				for _, k := range rawKeys {
					if k.Kind != "foreign_key" || k.RefTable == "" {
						continue
					}
					upKey := ""
					cands := snapByTable[strings.ToLower(k.RefTable)]
					var upSnap *db.MetaSnapshot
					for _, c := range cands {
						if c.ConnectionID == full.ConnectionID && (k.RefSchema == "" || strings.EqualFold(c.SchemaName, k.RefSchema)) {
							upSnap = c
							upKey = tableKey(c.ConnectionID, c.DatabaseName, c.SchemaName, c.TableName)
							break
						}
					}
					if upSnap == nil && len(cands) > 0 {
						upSnap = cands[0]
						upKey = tableKey(upSnap.ConnectionID, upSnap.DatabaseName, upSnap.SchemaName, upSnap.TableName)
					}
					if upKey == "" {
						continue
					}
					if !visited[upKey] {
						visited[upKey] = true
						upstream[upKey] = lineageNode{ID: upKey, Name: upSnap.TableName, Type: upSnap.TableType, DB: upSnap.DatabaseName, Desc: "外键 " + k.Name}
						queue = append(queue, struct {
							key  string
							dep  int
							snap *db.MetaSnapshot
						}{key: upKey, dep: cur.dep + 1, snap: upSnap})
					}
					edges = append(edges, lineageEdge{From: upKey, To: cur.key, Kind: "fk"})
				}
			}
		}
		for _, s := range snaps {
			k := tableKey(s.ConnectionID, s.DatabaseName, s.SchemaName, s.TableName)
			f := fullSnaps[k]
			if f == nil {
				if cand, err := h.meta.GetSnapshot(r.Context(), s.ConnectionID, s.DatabaseName, s.SchemaName, s.TableName); err == nil {
					f = cand
					fullSnaps[k] = f
				} else {
					continue
				}
			}
			var keys []metaKey
			if err := parseKeys(f.RawKeys, &keys); err != nil {
				continue
			}
			for _, kk := range keys {
				if kk.Kind != "foreign_key" || kk.RefTable == "" {
					continue
				}
				if strings.EqualFold(kk.RefTable, cur.snap.TableName) {
					if f.ConnectionID != cur.snap.ConnectionID {
						continue
					}
					downKey := k
					if !visited[downKey] {
						visited[downKey] = true
						downstream[downKey] = lineageNode{ID: downKey, Name: f.TableName, Type: f.TableType, DB: f.DatabaseName, Desc: "外键 " + kk.Name}
						queue = append(queue, struct {
							key  string
							dep  int
							snap *db.MetaSnapshot
						}{key: downKey, dep: cur.dep + 1, snap: f})
					}
					edges = append(edges, lineageEdge{From: cur.key, To: downKey, Kind: "fk"})
				}
			}
		}
	}

	for _, rep := range reports {
		if h.reportUses(r, rep, targetSnap) {
			id := "report." + strconv.FormatInt(rep.ID, 10)
			if !visited[id] {
				visited[id] = true
				downstream[id] = lineageNode{ID: id, Name: rep.Name, Type: "report", DB: rep.DatabaseName, Desc: "报表 #" + strconv.FormatInt(rep.ID, 10)}
				edges = append(edges, lineageEdge{From: targetKey, To: id, Kind: "report"})
			}
		}
	}
	upstreamNodes := make([]lineageNode, 0, len(upstream))
	for _, n := range upstream {
		upstreamNodes = append(upstreamNodes, n)
	}
	downstreamNodes := make([]lineageNode, 0, len(downstream))
	for _, n := range downstream {
		downstreamNodes = append(downstreamNodes, n)
	}
	nodes := []lineageNode{}
	nodes = append(nodes, upstreamNodes...)
	nodes = append(nodes, lineageNode{ID: targetKey, Name: targetSnap.TableName, Type: targetSnap.TableType, DB: targetSnap.DatabaseName, Desc: "当前"})
	nodes = append(nodes, downstreamNodes...)
	return lineageResp{
		Nodes:      nodes,
		Edges:      edges,
		Upstream:   len(upstreamNodes),
		Downstream: len(downstreamNodes),
		Impact: map[string]int{
			"upstream":           len(upstreamNodes),
			"downstream":         len(downstreamNodes),
			"downstream_reports": countType(downstreamNodes, "report"),
			"downstream_tables":  countType(downstreamNodes, "table"),
			"downstream_views":   countType(downstreamNodes, "view"),
			"total":              len(nodes),
		},
		Cached: false,
	}
}

// Refresh POST /api/v1/assets/lineage/refresh 触发增量解析（admin/developer）
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		httpx.Fail(w, httpx.Internal("血缘解析服务未初始化", nil))
		return
	}
	n, err := h.svc.ParseRecentHistory(r.Context(), 500)
	if err != nil {
		httpx.Fail(w, httpx.Internal("解析失败", err))
		return
	}
	// 同步 FK 回填
	fkN, _ := h.svc.BackfillFK(r.Context())
	httpx.OK(w, map[string]any{"parsed_edges": n, "fk_edges": fkN, "total": n + fkN})
}

func countType(nodes []lineageNode, t string) int {
	c := 0
	for _, n := range nodes {
		if n.Type == t {
			c++
		}
	}
	return c
}

type metaKey struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
}

func parseKeys(raw []byte, out *[]metaKey) error {
	if len(raw) == 0 {
		return nil
	}
	if string(raw) == "null" || string(raw) == "[]" || string(raw) == "" {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (h *Handler) reportUses(r *http.Request, report db.Report, snap *db.MetaSnapshot) bool {
	if report.ConnectionID != snap.ConnectionID || h.conns == nil {
		return false
	}
	conn, e := h.conns.Get(r.Context(), report.ConnectionID)
	if e != nil {
		return false
	}
	pr, e := lineage.ParseTables(report.SQLText, conn.Type)
	if e != nil {
		return false
	}
	database := report.DatabaseName
	if database == "" {
		database = conn.Database
	}
	for _, table := range pr.All {
		d := table.Database
		if d == "" {
			d = database
		}
		s := table.Schema
		if s == "" {
			s = "public"
			if conn.Type == "mysql" {
				s = d
			}
		}
		if d == snap.DatabaseName && s == snap.SchemaName && table.Table == snap.TableName {
			return true
		}
	}
	return false
}
