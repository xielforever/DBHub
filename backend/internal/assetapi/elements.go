package assetapi

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

var technicalElementKinds = []string{"database", "schema", "table", "view", "field", "index", "constraint"}
var technicalElementLevels = []string{"normal", "sensitive", "confidential", "unclassified"}

// ListElements GET /api/v1/assets/elements
func (h *Handler) ListElements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	connectionID := int64(0)
	if q.Has("connection_id") {
		var err error
		connectionID, err = strconv.ParseInt(q.Get("connection_id"), 10, 64)
		if err != nil || connectionID < 1 {
			httpx.Fail(w, httpx.BadRequest("无效的数据源"))
			return
		}
	}
	page, ok := elementPageValue(q.Get("page"), 1, 1000000)
	pageSize, sizeOK := elementPageValue(q.Get("page_size"), 24, 100)
	keyword := strings.TrimSpace(q.Get("q"))
	kinds, levels, tags := q["type"], q["level"], q["tag"]
	if !ok || !sizeOK || utf8.RuneCountInString(keyword) > 200 ||
		!validElementOptions(kinds, technicalElementKinds) || !validElementOptions(levels, technicalElementLevels) ||
		len(tags) > 32 || slices.ContainsFunc(tags, func(tag string) bool {
		return tag == "" || utf8.RuneCountInString(tag) > 20
	}) {
		httpx.Fail(w, httpx.BadRequest("无效的技术元素筛选或分页"))
		return
	}

	database, table := q.Get("database"), q.Get("table")
	var schema *string
	if q.Has("schema") {
		name := q.Get("schema")
		schema = &name
	}
	if (q.Has("database") && database == "") || (database != "" && connectionID == 0) ||
		(schema != nil && database == "") || (q.Has("table") && (schema == nil || table == "")) {
		httpx.Fail(w, httpx.BadRequest("无效的技术元素位置"))
		return
	}
	result, err := h.meta.ListTechnicalElements(r.Context(), db.TechnicalElementFilter{
		ConnectionID: connectionID, Database: database, Schema: schema, Table: table,
		Keyword: keyword, Kinds: kinds, Levels: levels, Tags: tags, Offset: (page - 1) * pageSize, Limit: pageSize,
	})
	if err != nil {
		httpx.Fail(w, httpx.Internal("读取技术元素失败", err))
		return
	}
	httpx.OK(w, struct {
		db.TechnicalElementPage
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}{result, page, pageSize})
}

func elementPageValue(raw string, fallback, max int) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value > 0 && value <= max
}

func validElementOptions(values, allowed []string) bool {
	return len(values) <= len(allowed) && !slices.ContainsFunc(values, func(value string) bool {
		return !slices.Contains(allowed, value)
	})
}
