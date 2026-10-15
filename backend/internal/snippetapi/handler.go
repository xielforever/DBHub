package snippetapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type Handler struct{ repo *db.SnippetRepository }

func NewHandler(repo *db.SnippetRepository) *Handler { return &Handler{repo: repo} }

type input struct {
	Name         *string         `json:"name"`
	SQL          *string         `json:"sql_text"`
	Database     *string         `json:"database_name"`
	Visibility   *string         `json:"visibility"`
	ConnectionID json.RawMessage `json:"connection_id"`
}

func apply(s *db.Snippet, in input) error {
	if len(in.ConnectionID) > 0 {
		if string(in.ConnectionID) == "null" {
			s.ConnectionID = nil
		} else {
			var id int64
			if e := json.Unmarshal(in.ConnectionID, &id); e != nil || id < 1 {
				return httpx.BadRequest("connection_id 无效")
			}
			s.ConnectionID = &id
		}
	}
	if in.Name != nil {
		s.Name = strings.TrimSpace(*in.Name)
	}
	if in.SQL != nil {
		s.SQL = *in.SQL
	}
	if in.Database != nil {
		s.Database = *in.Database
	}
	if in.Visibility != nil {
		s.Visibility = *in.Visibility
	}
	if s.Visibility == "" {
		s.Visibility = "private"
	}
	if s.Name == "" || len([]rune(s.Name)) > 200 || strings.TrimSpace(s.SQL) == "" {
		return httpx.BadRequest("name 必填且不超过 200 字符，sql_text 必填")
	}
	if s.Visibility != "private" && s.Visibility != "shared" {
		return httpx.BadRequest("visibility 仅支持 private/shared")
	}
	return nil
}
func fail(w http.ResponseWriter, e error) {
	if errors.Is(e, db.ErrNotFound) {
		httpx.Fail(w, httpx.NotFound("片段不存在或无权限"))
	} else {
		httpx.Fail(w, httpx.Internal("SQL 片段操作失败", e))
	}
}
func id(r *http.Request) int64 { n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return n }
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size < 1 || size > 100 {
		size = 20
	}
	connection, _ := strconv.ParseInt(q.Get("connection_id"), 10, 64)
	items, total, e := h.repo.List(r.Context(), c.Subject, q.Get("q"), q.Get("visibility"), connection, size, (page-1)*size)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	var in input
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	s := &db.Snippet{UserID: c.Subject}
	if e := apply(s, in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if e := h.repo.Create(r.Context(), s); e != nil {
		fail(w, e)
		return
	}
	out, e := h.repo.Get(r.Context(), s.ID, c.Subject, false)
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	s, e := h.repo.Get(r.Context(), id(r), c.Subject, c.Role == "admin")
	if e != nil {
		fail(w, e)
		return
	}
	if s.UserID != c.Subject && c.Role != "admin" {
		httpx.Fail(w, httpx.Forbidden("仅所有者或管理员可修改片段"))
		return
	}
	var in input
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if e = apply(s, in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if e = h.repo.Update(r.Context(), s, c.Subject, c.Role == "admin"); e != nil {
		fail(w, e)
		return
	}
	out, e := h.repo.Get(r.Context(), s.ID, c.Subject, c.Role == "admin")
	if e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, out)
}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	if e := h.repo.Delete(r.Context(), id(r), c.Subject, c.Role == "admin"); e != nil {
		fail(w, e)
		return
	}
	httpx.OK(w, map[string]any{"id": id(r)})
}
