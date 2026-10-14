package lineageapi

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"log/slog"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// Handler 血缘 handler（表级，上游/下游/影响面），Step27 后端落库 + 实时计算（FK + SQL + 报表）
type Handler struct {
	meta       *db.MetadataRepository
	history    *db.HistoryRepository
	reports    *db.ReportRepository
	dashboards *db.DashboardRepository
	log        *slog.Logger
}

func NewHandler(meta *db.MetadataRepository, history *db.HistoryRepository, reports *db.ReportRepository, dashboards *db.DashboardRepository, log *slog.Logger) *Handler {
	return &Handler{meta: meta, history: history, reports: reports, dashboards: dashboards, log: log}
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
}

// table key: connection_id.database.schema.table (lower)
func tableKey(conn int64, dbName, schema, table string) string {
	// 统一小写，便于去重
	return strings.ToLower(strings.Join([]string{strconv.FormatInt(conn, 10), dbName, schema, table}, "."))
}

var (
	// 粗略提取表名：FROM / JOIN / INTO / UPDATE / TABLE后跟标识
	tableRe = regexp.MustCompile(`(?i)(?:FROM|JOIN|INTO|UPDATE|TABLE)\s+("?[a-zA-Z_][a-zA-Z0-9_]*"?\.)?("?[a-zA-Z_][a-zA-Z0-9_]*"?\.)?("?[a-zA-Z_][a-zA-Z0-9_]*"?)`)
	// 额外：INSERT INTO 后
	insertRe = regexp.MustCompile(`(?i)INSERT\s+INTO\s+("?[a-zA-Z_][\w$]*"?)`)
)

func extractTables(sql string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, m := range tableRe.FindAllStringSubmatch(sql, -1) {
		// m[3] 为表名（最末标识）
		t := strings.Trim(m[3], `"`)
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		// 过滤关键字
		if low == "select" || low == "where" || low == "values" || low == "set" {
			continue
		}
		if _, ok := seen[low]; !ok {
			seen[low] = struct{}{}
			out = append(out, low)
		}
	}
	for _, m := range insertRe.FindAllStringSubmatch(sql, -1) {
		t := strings.Trim(m[1], `"`)
		low := strings.ToLower(t)
		if _, ok := seen[low]; !ok {
			seen[low] = struct{}{}
			out = append(out, low)
		}
	}
	return out
}

// Lineage GET /api/v1/assets/lineage?connection_id=&database=&schema=&table=&depth=1|2|3|all
func (h *Handler) Lineage(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	_ = claims // 仅鉴权，不按用户过滤（登录可见）

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
	} else if depthStr == "2" || depthStr == "" {
		depth = 2
	}

	// 加载全部快照（按连接过滤时仍加载该连接全量，便于跨库 FK）
	snaps, err := h.meta.ListSnapshots(r.Context(), 0)
	if err != nil {
		httpx.Fail(w, httpx.Internal("加载快照失败", err))
		return
	}
	// 索引：table -> snap, 以及按小写表名聚合
	snapByKey := map[string]*db.MetaSnapshot{}
	snapByTable := map[string][]*db.MetaSnapshot{}
	for i := range snaps {
		s := &snaps[i]
		k := tableKey(s.ConnectionID, s.DatabaseName, s.SchemaName, s.TableName)
		snapByKey[k] = s
		low := strings.ToLower(s.TableName)
		snapByTable[low] = append(snapByTable[low], s)
	}

	// 目标键：优先精确，若 connection/database 未指定则按表名模糊匹配第一个
	targetKey := tableKey(connID, dbName, schema, table)
	targetSnap, ok := snapByKey[targetKey]
	if !ok {
		// 回退：按表名匹配（不区分库）
		cands := snapByTable[strings.ToLower(table)]
		if len(cands) == 0 {
			httpx.Fail(w, &httpx.AppError{Code: httpx.CodeNotFound, Message: "目标表不存在或未同步"})
			return
		}
		// 优先同 connection/database 的
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

	// 构建 FK 图
	// raw_keys JSONB 已在 ListSnapshots 被置空（轻量），需按需重新取完整快照的 keys
	// 为简化，直接用 ListSnapshots 的快照若 raw_keys 为空则跳过，需重新查询完整快照按需
	// 这里改用遍历时若需要 keys，则通过 meta.GetSnapshot 补全（仅对涉及的表）
	// 为性能，首次遍历先收集涉及的表对应的完整快照
	type fk struct{ from, to string; fromSnap, toSnap *db.MetaSnapshot }
	// 先收集所有完整快照的 keys（对当前连接的库）
	fullSnaps := map[string]*db.MetaSnapshot{}
	// 已有 targetSnap 的完整信息（GetSnapshot）
	if targetSnap != nil {
		// 需要完整 raw_keys，先取一次
		if full, err := h.meta.GetSnapshot(r.Context(), targetSnap.ConnectionID, targetSnap.DatabaseName, targetSnap.SchemaName, targetSnap.TableName); err == nil {
			fullSnaps[targetKey] = full
		}
	}

	// BFS 上下游
	upstream := map[string]lineageNode{}
	downstream := map[string]lineageNode{}
	edges := []lineageEdge{}
	visited := map[string]bool{targetKey: true}
	queue := []struct {
		key   string
		dep int
		snap  *db.MetaSnapshot
	}{ {key: targetKey, dep: 0, snap: targetSnap} }

	// 预加载所有报告（用于下游报表引用）
	var reports []db.Report
	if h.reports != nil {
		// 取全部报表（限制 100）
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
		// 确保当前节点的完整快照
		full := fullSnaps[cur.key]
		if full == nil {
			if cur.snap != nil {
				if f, err := h.meta.GetSnapshot(r.Context(), cur.snap.ConnectionID, cur.snap.DatabaseName, cur.snap.SchemaName, cur.snap.TableName); err == nil {
					full = f
					fullSnaps[cur.key] = f
				}
			}
		}
		// 1) FK 上游：当前表的外键指向的表
		if full != nil && len(full.RawKeys) > 0 {
	
			// 直接解析 RawKeys
			// RawKeys 存储为 JSON 数组，需 Unmarshal
			var rawKeys []metaKey
			// MetaKey 结构与存储一致
			// 但 RawKeys 是 json.RawMessage，需先 Unmarshal
			// 这里尝试解析
			if err := parseKeys(full.RawKeys, &rawKeys); err == nil {
				for _, k := range rawKeys {
					if k.Kind != "foreign_key" || k.RefTable == "" {
						continue
					}
					// 上游表
					upKey := ""
					// 尝试在同库同连接中找上游
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
						node := lineageNode{ID: upKey, Name: upSnap.TableName, Type: upSnap.TableType, DB: upSnap.DatabaseName, Desc: "外键 " + k.Name}
						upstream[upKey] = node
						queue = append(queue, struct {
							key   string
							dep int
							snap  *db.MetaSnapshot
						}{key: upKey, dep: cur.dep + 1, snap: upSnap})
					}
					edges = append(edges, lineageEdge{From: upKey, To: cur.key, Kind: "fk"})
				}
			}
		}
		// 2) FK 下游：哪些表的外键指向当前表
		for _, s := range snaps {
			k := tableKey(s.ConnectionID, s.DatabaseName, s.SchemaName, s.TableName)
			if visited[k] && k != cur.key {
				// 已访问的下游也可能需要边，但 BFS 会处理
			}
			// 需要完整 keys
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
				// 若该表的外键指向 cur
				if strings.EqualFold(kk.RefTable, cur.snap.TableName) {
					// 且连接/库匹配（简化：同连接）
					if f.ConnectionID != cur.snap.ConnectionID {
						continue
					}
					downKey := k
					if !visited[downKey] {
						visited[downKey] = true
						node := lineageNode{ID: downKey, Name: f.TableName, Type: f.TableType, DB: f.DatabaseName, Desc: "外键 " + kk.Name}
						downstream[downKey] = node
						queue = append(queue, struct {
							key   string
							dep int
							snap  *db.MetaSnapshot
						}{key: downKey, dep: cur.dep + 1, snap: f})
					}
					edges = append(edges, lineageEdge{From: cur.key, To: downKey, Kind: "fk"})
				}
			}
		}

		// 3) SQL 上下游：扫描 query_history（近 100 条成功）
		if h.history != nil && cur.dep < depth {
			// 取近 100 条成功历史（不区分用户，取 admin 视角）
			hists, _, _ := h.history.List(r.Context(), 0, true, 0, "success", 100, 0)
			for _, he := range hists {
				tables := extractTables(he.SQLText)
				lowCur := strings.ToLower(cur.snap.TableName)
				foundCur := false
				for _, t := range tables {
					if t == lowCur {
						foundCur = true
						break
					}
				}
				if !foundCur {
					continue
				}
				for _, t := range tables {
					if t == lowCur {
						continue
					}
					// 找到与当前表同 SQL 出现的其他表
					cands := snapByTable[t]
					var other *db.MetaSnapshot
					var otherKey string
					for _, c := range cands {
						otherKey = tableKey(c.ConnectionID, c.DatabaseName, c.SchemaName, c.TableName)
						other = c
						break
					}
					if other == nil {
						// 未同步的表也作为节点（报告/外部）
						otherKey = "ext." + t
						if !visited[otherKey] {
							visited[otherKey] = true
							downstream[otherKey] = lineageNode{ID: otherKey, Name: t, Type: "table", DB: he.DatabaseName, Desc: "SQL 依赖"}
							edges = append(edges, lineageEdge{From: cur.key, To: otherKey, Kind: "sql"})
						}
						continue
					}
					if !visited[otherKey] {
						visited[otherKey] = true
						// 方向：若 SQL 为 INSERT INTO cur ... SELECT ... FROM other，则 other 为上游
						// 简化：统一视为下游（与 FK 区分用虚线）
						downstream[otherKey] = lineageNode{ID: otherKey, Name: other.TableName, Type: other.TableType, DB: other.DatabaseName, Desc: "SQL 关联"}
					}
					edges = append(edges, lineageEdge{From: cur.key, To: otherKey, Kind: "sql"})
				}
			}
		}
	}

	// 4) 报表下游：sql_text 包含目标表名
	for _, rep := range reports {
		lowSQL := strings.ToLower(rep.SQLText)
		lowTable := strings.ToLower(targetSnap.TableName)
		if strings.Contains(lowSQL, lowTable) {
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
	// 合并
	nodes := []lineageNode{}
	nodes = append(nodes, upstreamNodes...)
	// 中心节点
	nodes = append(nodes, lineageNode{ID: targetKey, Name: targetSnap.TableName, Type: targetSnap.TableType, DB: targetSnap.DatabaseName, Desc: "当前"})
	nodes = append(nodes, downstreamNodes...)

	resp := lineageResp{
		Nodes:      nodes,
		Edges:      edges,
		Upstream:   len(upstreamNodes),
		Downstream: len(downstreamNodes),
		Impact: map[string]int{
			"upstream":            len(upstreamNodes),
			"downstream":          len(downstreamNodes),
			"downstream_reports":  countType(downstreamNodes, "report"),
			"downstream_tables":   countType(downstreamNodes, "table"),
			"downstream_views":    countType(downstreamNodes, "view"),
			"total":               len(nodes),
		},
	}
	httpx.OK(w, resp)
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
