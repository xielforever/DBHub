package adminapi

import (
	"errors"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"net/http"
)

func groupFailure(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, db.ErrSourceInput):
		httpx.Fail(w, httpx.BadRequest(e.Error()))
	case errors.Is(e, db.ErrCatalogConflict):
		httpx.Fail(w, httpx.Conflict("用户组名称已存在"))
	case errors.Is(e, db.ErrSourceVersionConflict):
		httpx.Fail(w, httpx.Conflict(e.Error()))
	case errors.Is(e, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("用户组不存在"))
	default:
		httpx.Fail(w, httpx.Internal("用户组操作失败", e))
	}
}
func (h *Handler) Groups(w http.ResponseWriter, r *http.Request) {
	page, size := atoiDefault(r.URL.Query().Get("page"), 1), atoiDefault(r.URL.Query().Get("page_size"), 20)
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		httpx.Fail(w, httpx.BadRequest("分页参数无效"))
		return
	}
	items, total, e := h.users.Groups(r.Context(), r.URL.Query().Get("keyword"), size, (page-1)*size)
	if e != nil {
		httpx.Fail(w, httpx.Internal("加载用户组失败", e))
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (h *Handler) SaveGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name            string  `json:"name"`
		Description     string  `json:"description"`
		MemberIDs       []int64 `json:"member_ids"`
		ExpectedVersion int64   `json:"expected_version"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	g := db.UserGroup{Name: in.Name, Description: in.Description, MemberIDs: in.MemberIDs}
	if r.PathValue("id") != "" {
		id, e := idFromPath(r)
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		g.ID = id
	}
	if e := h.users.SaveGroup(r.Context(), &g, in.ExpectedVersion); e != nil {
		groupFailure(w, e)
		return
	}
	httpx.OK(w, g)
}
func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, e := idFromPath(r)
	if e != nil {
		httpx.Fail(w, e)
		return
	}
	var in struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	if e = httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	if e = h.users.DeleteGroup(r.Context(), id, in.ExpectedVersion); e != nil {
		groupFailure(w, e)
		return
	}
	httpx.OK(w, map[string]any{"id": id})
}
