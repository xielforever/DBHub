package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"

	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
	"github.com/user/dbhub/internal/iam"
)

type recordedMux struct {
	*http.ServeMux
	rules []iam.RouteRule
}

func (m *recordedMux) Handle(pattern string, h http.Handler) {
	m.rules = append(m.rules, iam.Rule(pattern))
	m.ServeMux.Handle(pattern, h)
}
func (m *recordedMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.Handle(pattern, http.HandlerFunc(h))
}

func functionalGuard(users *db.UserRepository, next http.Handler, w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFromContext(r.Context())
	if c == nil || c.IAM == nil {
		httpx.Fail(w, httpx.Forbidden("当前权限不可用"))
		return
	}
	rule := iam.Rule(r.Pattern)
	governance := strings.Contains(r.Pattern, "/iam/") || strings.Contains(r.Pattern, "/members") || strings.Contains(r.Pattern, "/policy") || strings.Contains(r.Pattern, "/user-groups") || strings.Contains(r.Pattern, "/users")
	if r.Method == http.MethodGet && strings.Contains(r.Pattern, "/iam/") {
		governance = false
	}
	deny := func(message string) {
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = "insufficient_action" })
		httpx.Fail(w, httpx.Forbidden(message))
	}
	if !rule.Known {
		deny("此接口尚未完成权限登记")
		return
	}
	if c.IAM.GovernanceOnly && !strings.Contains(r.Pattern, "/iam/") {
		deny("当前会话仅用于有限期治理恢复")
		return
	}
	if c.IAM.Target || len(c.IAM.DeniedActions) > 0 {
		legacyAllowed := func(action string) bool {
			if cap, known := iam.LookupAction(action); known && cap.State == "legacy" {
				return auth.HasAction(c, action)
			}
			return !slices.Contains(c.IAM.DeniedActions, action)
		}
		for _, action := range rule.All {
			if c.IAM.Target && !auth.HasAction(c, action) || !c.IAM.Target && !legacyAllowed(action) {
				deny("此功能还需要对应的个人连接操作权限")
				return
			}
		}
		if c.IAM.Target && (!rule.Known || rule.Scope == "blocked") {
			deny("此功能尚未纳入当前权限方案")
			return
		}
		if c.IAM.Target && rule.Scope == "legacy_governance" {
			deny("请通过空间管理提交治理审批，原入口不能增加新授权")
			return
		}
		actions := rule.Any
		if rule.Selector != "" {
			var in map[string]json.RawMessage
			if rule.Selector == "source_task_kind" {
				kind := ""
				if id := r.PathValue("id"); id != "" {
					var e error
					kind, e = users.IAMSourceTaskKind(r.Context(), id)
					if e != nil {
						httpx.Fail(w, httpx.NotFound("原任务不存在或当前无权访问"))
						return
					}
				} else {
					raw, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
					if e != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &in) != nil {
						httpx.Fail(w, httpx.BadRequest("任务参数无效"))
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
					_ = json.Unmarshal(in["kind"], &kind)
				}
				switch kind {
				case "check":
					actions = []string{"connection.use", "source.definition.manage"}
				case "collect":
					actions = []string{"connection.manage", "source.definition.manage"}
				default:
					deny("任务类型无效")
					return
				}
			}
			if rule.Selector == "release_kind" || rule.Selector == "review_decision" || rule.Selector == "dashboard_refresh_mode" || rule.Selector == "restriction_kind" {
				raw, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
				if e != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &in) != nil {
					httpx.Fail(w, httpx.BadRequest("参数无效"))
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(raw))
				family := "report"
				if strings.Contains(r.Pattern, "dashboard") {
					family = "dashboard"
				}
				var value string
				switch rule.Selector {
				case "restriction_kind":
					_ = json.Unmarshal(in["kind"], &value)
					action := iam.RestrictionAction(value)
					if action == "" {
						httpx.Fail(w, httpx.BadRequest("不支持该收紧动作；解除限制必须重新审批"))
						return
					}
					actions = []string{action}
				case "release_kind":
					_ = json.Unmarshal(in["kind"], &value)
					if value == "unpublish" {
						actions = []string{family + ".offline"}
					} else if value == "publish" || value == "rollback" {
						actions = []string{family + ".publish"}
					} else {
						deny("发布动作无效")
						return
					}
				case "review_decision":
					_ = json.Unmarshal(in["decision"], &value)
					if value == "withdrawn" {
						actions = []string{family + ".design"}
					} else {
						actions = []string{family + ".review"}
					}
				case "dashboard_refresh_mode":
					_ = json.Unmarshal(in["mode"], &value)
					switch value {
					case "", "published":
						actions = []string{"dashboard.view"}
					case "draft":
						actions = []string{"dashboard.design"}
					case "review":
						actions = []string{"dashboard.review"}
					default:
						deny("刷新模式无效")
						return
					}
				}
			}
			if rule.Selector == "dashboard_receipt_mode" {
				mode, e := users.IAMRefreshMode(r.Context(), r.PathValue("refresh"))
				if e != nil {
					httpx.Fail(w, httpx.NotFound("刷新不存在或已失效"))
					return
				}
				switch mode {
				case "published":
					actions = []string{"dashboard.view"}
				case "draft":
					actions = []string{"dashboard.design"}
				case "review":
					actions = []string{"dashboard.review"}
				default:
					deny("刷新模式无效")
					return
				}
			}
		}
		allowed := len(actions) == 0
		for _, action := range actions {
			if (c.IAM.Target && auth.HasAction(c, action)) || (!c.IAM.Target && legacyAllowed(action)) {
				copy := *c
				copy.CurrentAction = action
				r = r.WithContext(auth.WithClaims(r.Context(), &copy))
				c = &copy
				allowed = true
				break
			}
		}
		if !allowed {
			deny("当前工作空间没有此操作权限")
			return
		}
	}
	if governance {
		next.ServeHTTP(w, r)
		return
	}
	// Query execution acquires its fence after queue admission. Holding a
	// database connection for every waiting request would block the queue's
	// own cancellation/inspection endpoints before they reach the coordinator.
	verifyDelivery := func() error { return users.IAMCurrent(r.Context()) }
	if r.Pattern != "POST /api/v1/query/execute" {
		control := r.Pattern == "GET /api/v1/query/jobs" || r.Pattern == "POST /api/v1/query/jobs/{id}/cancel"
		var verify func() error
		var release func()
		var e error
		if r.Pattern == "GET /api/v1/connections" {
			var ctx context.Context
			ctx, verify, release, e = users.IAMConnectionReadFence(r.Context())
			r = r.WithContext(ctx)
		} else {
			verify, release, e = users.IAMRequestFence(r.Context(), control)
		}
		if e != nil {
			httpx.Fail(w, httpx.Forbidden("权限已变化，请刷新后重试"))
			return
		}
		defer release()
		verifyDelivery = verify
	}
	response := httptest.NewRecorder()
	next.ServeHTTP(response, r)
	deliverGuardedResponse(w, r, response, verifyDelivery)
}

// deliverGuardedResponse 保持权限复核与结果交付隔离，取消不伪装成撤权。
func deliverGuardedResponse(w http.ResponseWriter, r *http.Request, response *httptest.ResponseRecorder, verifyDelivery func() error) {
	if stoppedGuardedRequest(w, r) {
		return
	}
	if e := verifyDelivery(); e != nil {
		if stoppedGuardedRequest(w, r) {
			return
		}
		if r.Pattern == "POST /api/v1/query/execute" {
			httpx.Fail(w, httpx.New(httpx.CodeQueryDeliveryBlocked, "成员资格或权限已变化，执行结果停止返回。请保留原请求编号核实。", nil))
			return
		}
		httpx.Fail(w, httpx.Conflict("成员资格或权限已变化，结果已停止返回"))
		return
	}
	if stoppedGuardedRequest(w, r) {
		return
	}
	for key, values := range response.Header() {
		w.Header()[key] = values
	}
	w.WriteHeader(response.Code)
	_, _ = io.Copy(w, response.Body)
}

func stoppedGuardedRequest(w http.ResponseWriter, r *http.Request) bool {
	err := r.Context().Err()
	if err == nil {
		return false
	}
	status, message := 499, "请求已取消，结果未返回；不表示操作已回滚"
	if errors.Is(err, context.DeadlineExceeded) {
		status, message = http.StatusGatewayTimeout, "请求处理超时，结果未返回；请先核实操作状态"
	}
	httpx.JSON(w, status, httpx.Envelope{Code: status * 100, Message: message})
	return true
}
