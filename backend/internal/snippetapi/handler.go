package snippetapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type Handler struct {
	repo           *db.SnippetRepository
	conns          *db.ConnectionRepository
	drivers        *connectorruntime.Runtime
	verifyDatabase func(*http.Request, int64, string) error
}

func NewHandler(repo *db.SnippetRepository, conns *db.ConnectionRepository, drivers *connectorruntime.Runtime, verifyDatabase func(*http.Request, int64, string) error) *Handler {
	return &Handler{repo: repo, conns: conns, drivers: drivers, verifyDatabase: verifyDatabase}
}

func fail(w http.ResponseWriter, err error) {
	var pg *pgconn.PgError
	var app *httpx.AppError
	switch {
	case errors.As(err, &app):
		httpx.Fail(w, app)
	case errors.Is(err, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("当前空间内没有可访问的脚本、目录或成员"))
	case errors.Is(err, db.ErrScriptReadOnly):
		httpx.Fail(w, httpx.Forbidden(err.Error()))
	case errors.Is(err, db.ErrScriptVersion):
		httpx.Fail(w, httpx.Conflict(err.Error()))
	case errors.As(err, &pg) && pg.ConstraintName == "script_folder_siblings":
		httpx.Fail(w, httpx.Conflict("同级目录已使用这个名称"))
	default:
		httpx.Fail(w, httpx.Internal("工作台脚本操作失败", err))
	}
}

func routeID(w http.ResponseWriter, r *http.Request, path string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(path), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(w, httpx.BadRequest("编号无效"))
		return 0, false
	}
	return id, true
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 200 || strings.ContainsRune(name, 0) {
		return "", httpx.BadRequest("名称必填且不得超过 200 字符")
	}
	return name, nil
}

func (h *Handler) Folders(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.Folders(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if items == nil {
		items = []db.ScriptFolder{}
	}
	httpx.OK(w, map[string]any{"items": items})
}

func (h *Handler) NewFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	name, err := validateName(in.Name)
	if err != nil {
		fail(w, err)
		return
	}
	if in.ParentID != nil && *in.ParentID <= 0 {
		fail(w, httpx.BadRequest("上级目录编号无效"))
		return
	}
	folder, err := h.repo.NewFolder(r.Context(), in.ParentID, name)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, folder)
}

func (h *Handler) RenameFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := routeID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	name, err := validateName(in.Name)
	if err != nil {
		fail(w, err)
		return
	}
	folder, err := h.repo.RenameFolder(r.Context(), id, name)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, folder)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	view := q.Get("view")
	if view != "" && view != "mine" && view != "shared" {
		fail(w, httpx.BadRequest("脚本查看范围无效"))
		return
	}
	var folder *int64
	if raw := q.Get("folder_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 || view == "shared" {
			fail(w, httpx.BadRequest("目录编号无效"))
			return
		}
		folder = &id
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		fail(w, httpx.BadRequest("页码超出范围"))
		return
	}
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size < 1 || size > 100 {
		size = 20
	}
	items, total, err := h.repo.List(r.Context(), view == "shared", folder, strings.TrimSpace(q.Get("q")), size, (page-1)*size)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := routeID(w, r, "id")
	if !ok {
		return
	}
	item, err := h.repo.Get(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	h.reply(w, r, item)
}

type changes struct {
	Name     *string         `json:"name"`
	Content  *string         `json:"content"`
	FolderID json.RawMessage `json:"folder_id"`
	Target   json.RawMessage `json:"default_target"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		changes
		Type string `json:"script_type"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Type != "postgres" && in.Type != "mysql" && in.Type != "redis" {
		fail(w, httpx.BadRequest("请选择 MySQL、PostgreSQL 或 Redis 脚本"))
		return
	}
	if in.Content == nil || in.Name == nil {
		fail(w, httpx.BadRequest("name 和 content 必填"))
		return
	}
	h.save(w, r, db.Script{Type: in.Type}, in.changes)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := routeID(w, r, "id")
	if !ok {
		return
	}
	current, err := h.repo.Get(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if current.ReadOnly {
		fail(w, db.ErrScriptReadOnly)
		return
	}
	var in struct {
		changes
		Version int64 `json:"version"`
	}
	if err = httpx.DecodeJSON(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Version < 1 {
		fail(w, httpx.BadRequest("脚本版本必填"))
		return
	}
	if in.Version != current.Version {
		fail(w, db.ErrScriptVersion)
		return
	}
	h.save(w, r, current, in.changes)
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request, script db.Script, in changes) {
	var err error
	if in.Name != nil {
		script.Name, err = validateName(*in.Name)
		if err != nil {
			fail(w, err)
			return
		}
	}
	if in.Content != nil {
		if strings.ContainsRune(*in.Content, 0) {
			fail(w, httpx.BadRequest("脚本文本包含无效字符"))
			return
		}
		script.Content = in.Content
	}
	if len(in.FolderID) > 0 {
		var folder *int64
		if err = json.Unmarshal(in.FolderID, &folder); err != nil || (folder != nil && *folder <= 0) {
			fail(w, httpx.BadRequest("目录编号无效"))
			return
		}
		script.FolderID = folder
	}
	if len(in.Target) > 0 {
		script.Target = nil
		if !bytes.Equal(in.Target, []byte("null")) {
			script.Target, err = h.validateTarget(r, script.Type, in.Target)
			if err != nil {
				fail(w, err)
				return
			}
		}
	}
	saved, err := h.repo.Save(r.Context(), script)
	if err != nil {
		fail(w, err)
		return
	}
	h.reply(w, r, saved)
}

func (h *Handler) validateTarget(r *http.Request, kind string, raw json.RawMessage) (*db.ScriptTarget, error) {
	var target db.ScriptTarget
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&target); err != nil || target.ConnectionID <= 0 || target.State != "" ||
		target.Database == "" || len([]rune(target.Database)) > 200 || strings.ContainsRune(target.Database, 0) {
		return nil, httpx.BadRequest("默认数据源或数据库无效")
	}
	conn, err := h.conns.Get(r.Context(), target.ConnectionID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, httpx.BadRequest("默认数据源当前不可用")
	}
	if err != nil {
		return nil, err
	}
	if !MatchesTarget(kind, conn, target.Database, h.drivers) {
		return nil, httpx.BadRequest("默认数据源与脚本类型或固定逻辑库不匹配")
	}
	if kind != "redis" && h.verifyDatabase != nil {
		if err = h.verifyDatabase(r, conn.ID, target.Database); err != nil {
			return nil, err
		}
	}
	target.Signature = signature(conn)
	return &target, nil
}

func MatchesTarget(kind string, conn *db.Connection, database string, drivers *connectorruntime.Runtime) bool {
	caps := drivers.Capabilities(conn)
	if kind == "redis" {
		fixed := conn.Database
		if fixed == "" {
			fixed = "0"
		}
		return conn.Type == "redis" && caps.Keys && database == fixed
	}
	return caps.SQL && conn.SQLDialect() == kind
}

func signature(conn *db.Connection) string {
	return conn.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
}

func (h *Handler) reply(w http.ResponseWriter, r *http.Request, item db.Script) {
	if item.Target != nil {
		item.Target.State = "unavailable"
		conn, err := h.conns.Get(r.Context(), item.Target.ConnectionID)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			fail(w, err)
			return
		}
		if err == nil && item.Target.Signature == signature(conn) && MatchesTarget(item.Type, conn, item.Target.Database, h.drivers) {
			item.Target.State = "available"
		}
	}
	httpx.OK(w, item)
}

func (h *Handler) Readers(w http.ResponseWriter, r *http.Request)    { h.readers(w, r, false) }
func (h *Handler) Candidates(w http.ResponseWriter, r *http.Request) { h.readers(w, r, true) }

func (h *Handler) readers(w http.ResponseWriter, r *http.Request, candidates bool) {
	id, ok := routeID(w, r, "id")
	if !ok {
		return
	}
	items, err := h.repo.Readers(r.Context(), id, candidates, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		fail(w, err)
		return
	}
	if items == nil {
		items = []db.ScriptReader{}
	}
	httpx.OK(w, map[string]any{"items": items})
}

func (h *Handler) Grant(w http.ResponseWriter, r *http.Request)  { h.changeReader(w, r, true) }
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) { h.changeReader(w, r, false) }
func (h *Handler) changeReader(w http.ResponseWriter, r *http.Request, grant bool) {
	id, ok := routeID(w, r, "id")
	if !ok {
		return
	}
	uid, ok := routeID(w, r, "user_id")
	if !ok {
		return
	}
	if uid == db.ActorID(r.Context()) {
		fail(w, httpx.BadRequest("所有者无需授予自己脚本读取权限"))
		return
	}
	if err := h.repo.GrantReader(r.Context(), id, uid, grant); err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"user_id": uid, "granted": grant})
}
