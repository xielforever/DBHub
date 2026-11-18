package server

import (
	"errors"
	"net/http"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// Governance errors explain the concrete constraint without a report-specific
// message or a raw database/credential diagnostic.
func iamHTTPError(w http.ResponseWriter, e error) {
	message, known := db.IAMErrorMessage(e)
	switch {
	case errors.Is(e, db.ErrNotFound):
		httpx.Fail(w, httpx.NotFound("当前空间记录不存在或已失效"))
	case errors.Is(e, db.ErrConnectionChanged):
		httpx.Fail(w, httpx.Conflict("成员资格或配置版本已变化，请刷新核对"))
	case errors.Is(e, db.ErrManagedDenied):
		if !known {
			message = "当前工作空间没有此治理操作权限"
		}
		httpx.Fail(w, httpx.Forbidden(message))
	case errors.Is(e, db.ErrManagedInput):
		if !known {
			message = "治理参数无效，请核对后提交"
		}
		httpx.Fail(w, httpx.BadRequest(message))
	case errors.Is(e, db.ErrManagedConflict):
		if !known {
			message = "治理记录已变化，请重新核对当前版本"
		}
		httpx.Fail(w, httpx.Conflict(message))
	default:
		httpx.Fail(w, httpx.Internal("空间治理服务暂不可用", e))
	}
}
