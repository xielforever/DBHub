package queryapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/user/dbhub/internal/auditctx"
	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/reportapi"
)

type managedRunKey struct{}
type managedRun struct {
	dashboard          *db.ManagedDashboardRepository
	dashboardExecution *db.DashboardExecution
	repo               *db.ManagedReportRepository
	execution          *db.ManagedExecution
	requestID          string
}

func (h *Handler) TestReportAccount(repo *db.ManagedReportRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, e := positiveID(r.PathValue("id"))
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		var body struct{}
		if _, e = httpx.DecodeOptionalJSON(w, r, &body); e != nil {
			httpx.Fail(w, e)
			return
		}
		c, p, e := repo.AccountConnection(r.Context(), id)
		if e != nil {
			reportapi.ManagedError(w, e)
			return
		}
		auditConnection(r.Context(), c, false)
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.ReportAccountID = id })
		if e = auditctx.Checkpoint(r.Context()); e != nil {
			httpx.Fail(w, httpx.New(50300, "审计存储暂不可用", e))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		handle, e := h.drivers.OpenRelational(ctx, c, p, nil)
		if e != nil {
			httpx.Fail(w, httpx.BadRequest("执行账号连接失败，请检查账号、网络及数据库授权"))
			return
		}
		defer handle.Close()
		_, e = target.ExecuteWithOptions(ctx, handle, "SELECT 1", true, target.ExecuteOptions{MaxRows: 1, Timeout: 5 * time.Second})
		if e != nil {
			httpx.Fail(w, httpx.BadRequest("执行账号未通过只读查询验证"))
			return
		}
		httpx.OK(w, map[string]bool{"connected": true, "read_only": true})
	}
}

func positiveID(value string) (int64, error) {
	id, e := strconv.ParseInt(value, 10, 64)
	if e != nil || id < 1 {
		return 0, httpx.BadRequest("ID 非法")
	}
	return id, nil
}
func managedRunOf(r *http.Request) *managedRun {
	value, _ := r.Context().Value(managedRunKey{}).(*managedRun)
	return value
}
func (h *Handler) runManaged(w http.ResponseWriter, r *http.Request, repo *db.ManagedReportRepository, x *db.ManagedExecution, requestID string) {
	ctx := context.WithValue(r.Context(), managedRunKey{}, &managedRun{repo: repo, execution: x, requestID: requestID})
	h.execute(w, r.WithContext(ctx), true, nil)
}
func (h *Handler) ExecutePublished(repo *db.ManagedReportRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, e := positiveID(r.PathValue("id"))
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		var in struct {
			RequestID     string `json:"request_id"`
			PublicationNo int64  `json:"publication_no"`
		}
		if e = httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		if !requestIDPattern.MatchString(in.RequestID) || in.RequestID == "00000000-0000-0000-0000-000000000000" || in.PublicationNo < 1 {
			httpx.Fail(w, httpx.BadRequest("request_id 和 publication_no 必填"))
			return
		}
		x, e := repo.ResolvePublished(r.Context(), id, in.PublicationNo)
		if e != nil {
			reportapi.ManagedError(w, e)
			return
		}
		h.runManaged(w, r, repo, x, in.RequestID)
	}
}
func (h *Handler) ValidateReportReview(repo *db.ManagedReportRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, e := positiveID(r.PathValue("id"))
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		var in struct {
			RequestID string  `json:"request_id"`
			AccountID int64   `json:"account_id"`
			UserIDs   []int64 `json:"user_ids"`
			GroupIDs  []int64 `json:"group_ids"`
		}
		if e = httpx.DecodeJSON(w, r, &in); e != nil {
			httpx.Fail(w, e)
			return
		}
		if !requestIDPattern.MatchString(in.RequestID) || in.RequestID == "00000000-0000-0000-0000-000000000000" || in.AccountID < 1 {
			httpx.Fail(w, httpx.BadRequest("request_id 和 account_id 必填"))
			return
		}
		x, e := repo.ValidationCandidate(r.Context(), id, in.AccountID, in.UserIDs, in.GroupIDs)
		if e != nil {
			reportapi.ManagedError(w, e)
			return
		}
		h.runManaged(w, r, repo, x, in.RequestID)
	}
}

func auditManagedExecution(ctx context.Context, x *db.ManagedExecution) {
	auditctx.Update(ctx, func(d *auditctx.Data) {
		d.ReportMode = "managed"
		d.ReportID = x.ReportID
		d.ReportVersion = x.Version
		d.ReportReviewID = x.ReviewID
		d.ReportPublicationNo = x.PublicationNo
		d.ReportAccountID = x.AccountID
		d.ReportAccountGeneration = x.Generation
		d.ReportManifestHash = x.Hash
		d.ReportAuthor, d.ReportApprover, d.ReportPublisher = x.Author, x.Approver, x.Publisher
	})
}
