package queryapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

func (h *Handler) PreviewReport(w http.ResponseWriter, r *http.Request) {
	h.execute(w, r, true, nil)
}

func (h *Handler) ExecuteReport(reports *db.ReportRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.execute(w, r, true, reports) }
}

func (h *Handler) reportRequest(w http.ResponseWriter, r *http.Request, reports *db.ReportRepository) (executeRequest, int64, int64, error) {
	if reports == nil {
		var in struct {
			RequestID    string `json:"request_id"`
			ConnectionID int64  `json:"connection_id"`
			Database     string `json:"database"`
			SQL          string `json:"sql"`
		}
		err := httpx.DecodeJSON(w, r, &in)
		if len(in.SQL) > 128*1024 {
			err = httpx.BadRequest("SQL 不能超过128KB")
		}
		return executeRequest{RequestID: in.RequestID, ConnectionID: in.ConnectionID, Database: in.Database, SQL: in.SQL}, 0, 0, err
	}
	var in struct {
		RequestID string `json:"request_id"`
		Version   int64  `json:"version"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return executeRequest{}, 0, 0, err
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return executeRequest{}, 0, 0, httpx.BadRequest("报表 ID 非法")
	}
	rep, err := reports.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			err = httpx.NotFound("报表不存在或无权访问")
		} else {
			err = httpx.Internal("读取报表失败", err)
		}
		return executeRequest{}, 0, 0, err
	}
	if in.Version != rep.Version {
		return executeRequest{}, 0, 0, httpx.Conflict("报表版本已变化，请重新加载后执行")
	}
	if rep.ConnectionID < 1 {
		return executeRequest{}, 0, 0, httpx.Conflict("请先从数据源目录添加此报表对应修订的个人连接，填写自己的数据库账号")
	}
	return executeRequest{RequestID: in.RequestID, ConnectionID: rep.ConnectionID, Database: rep.DatabaseName, SQL: rep.SQLText}, rep.ID, rep.Version, nil
}
