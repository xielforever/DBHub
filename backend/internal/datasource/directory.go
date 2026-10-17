package datasource

import (
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"net/http"
	"strconv"
)

func (h *Handler) DefinitionCategories(w http.ResponseWriter, r *http.Request) {
	items, e := h.conns.DefinitionCategories(r.Context())
	if e != nil {
		catalogFailure(w, e)
		return
	}
	httpx.OK(w, map[string]any{"items": items})
}
func (h *Handler) DefinitionAccess(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	if r.Method == http.MethodGet {
		a, e := h.conns.DefinitionAccess(r.Context(), id)
		if e != nil {
			catalogFailure(w, e)
			return
		}
		httpx.OK(w, a)
		return
	}
	var in struct {
		Mode            string  `json:"mode"`
		UserIDs         []int64 `json:"user_ids"`
		GroupIDs        []int64 `json:"group_ids"`
		ExpectedVersion int64   `json:"expected_version"`
	}
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if e = h.conns.SetDefinitionAccess(r.Context(), id, db.SourceAccess{Mode: in.Mode, UserIDs: in.UserIDs, GroupIDs: in.GroupIDs}, in.ExpectedVersion); e != nil {
		catalogFailure(w, e)
		return
	}
	h.definitionAudit(r, id, "access_updated")
	httpx.OK(w, map[string]any{"id": id, "version": in.ExpectedVersion + 1})
}
func (h *Handler) DefinitionProfile(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	var in struct {
		Category        string   `json:"category"`
		Tags            []string `json:"tags"`
		Maintainer      string   `json:"maintainer"`
		Contact         string   `json:"contact"`
		ExpectedVersion int64    `json:"expected_version"`
	}
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if e = h.conns.SetDefinitionProfile(r.Context(), id, db.SourceProfile{Category: in.Category, Tags: in.Tags, Maintainer: in.Maintainer, Contact: in.Contact}, in.ExpectedVersion); e != nil {
		catalogFailure(w, e)
		return
	}
	h.definitionAudit(r, id, "profile_updated")
	httpx.OK(w, map[string]any{"id": id, "version": in.ExpectedVersion + 1})
}
func (h *Handler) DefinitionFavorite(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	var in struct {
		Favorite *bool `json:"favorite"`
	}
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if in.Favorite == nil {
		httpx.Fail(w, httpx.BadRequest("favorite 必填"))
		return
	}
	if e = h.conns.SetDefinitionFavorite(r.Context(), id, *in.Favorite); e != nil {
		catalogFailure(w, e)
		return
	}
	httpx.OK(w, map[string]any{"favorite": *in.Favorite})
}
func directoryPage(r *http.Request) (db.DirectoryFilter, error) {
	q := r.URL.Query()
	p, size := 1, 20
	for k, v := range map[string]*int{"page": &p, "page_size": &size} {
		if s := q.Get(k); s != "" {
			n, e := strconv.Atoi(s)
			if e != nil || n < 1 || k == "page" && n > 1000000 || k == "page_size" && n > 100 {
				return db.DirectoryFilter{}, httpx.BadRequest("目录分页参数无效")
			}
			*v = n
		}
	}
	view := q.Get("view")
	if view != "" && view != "all" && view != "favorites" && view != "recent" {
		return db.DirectoryFilter{}, httpx.BadRequest("目录视图无效")
	}
	if len([]rune(q.Get("keyword"))) > 100 {
		return db.DirectoryFilter{}, httpx.BadRequest("搜索词过长")
	}
	return db.DirectoryFilter{Keyword: q.Get("keyword"), Category: q.Get("category"), View: view, Limit: size, Offset: (p - 1) * size}, nil
}

func (h *Handler) definitionAudit(r *http.Request, id int64, outcome string) {
	if d, e := h.conns.Definition(r.Context(), id); e == nil {
		auditctx.Update(r.Context(), func(a *auditctx.Data) {
			a.SourceFamilyID = d.FamilyID
			a.DefinitionID = d.ID
			a.Revision = d.Revision
			a.Outcome = outcome
		})
	}
}
