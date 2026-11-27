package queryapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/httpx"
)

type catalogSource struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// CatalogSources lists metadata-capable connections through the current actor's existing scope.
func (h *Handler) CatalogSources(w http.ResponseWriter, r *http.Request) {
	connections, err := h.conns.List(r.Context(), "", "")
	if err != nil {
		httpx.Fail(w, httpx.Internal("读取目录连接失败", err))
		return
	}
	items := make([]catalogSource, 0, len(connections))
	for i := range connections {
		conn := &connections[i]
		if h.drivers.Capabilities(conn).Metadata {
			kind := conn.Type
			if conn.Template != nil && conn.Template.Dialect != "" {
				kind = conn.Template.Dialect
			}
			items = append(items, catalogSource{ID: conn.ID, Name: conn.Name, Type: kind})
		}
	}
	httpx.OK(w, map[string]any{"items": items})
}

type catalogObject struct {
	Schema string `json:"schema"`
	target.TableItem
}

// CatalogObjects reuses one authorized connection to read the selected database's schemas.
func (h *Handler) CatalogObjects(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	connectionID, err := strconv.ParseInt(query.Get("connection_id"), 10, 64)
	database := query.Get("database")
	if err != nil || connectionID <= 0 || database == "" {
		httpx.Fail(w, httpx.BadRequest("需要有效的连接和数据库"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	handle, _, closeTarget, err := h.openRelational(r.WithContext(ctx), connectionID, database)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer closeTarget()
	schemas, err := target.Schemas(ctx, handle)
	if err != nil {
		httpx.Fail(w, httpx.Internal("读取目录模式失败", err))
		return
	}
	if len(schemas) == 0 {
		schemas = []string{""}
	}
	items := make([]catalogObject, 0)
	successful := 0
	var readError error
	for _, schema := range schemas {
		if ctx.Err() != nil {
			readError = ctx.Err()
			break
		}
		tables, err := target.Tables(ctx, handle, database, schema)
		if err != nil {
			readError = err
			continue
		}
		successful++
		for _, table := range tables {
			if table.Type == "table" || table.Type == "view" {
				items = append(items, catalogObject{Schema: schema, TableItem: table})
			}
		}
	}
	if successful == 0 {
		httpx.Fail(w, httpx.Internal("读取目录对象失败", readError))
		return
	}
	httpx.OK(w, map[string]any{"items": items, "incomplete": successful != len(schemas)})
}
