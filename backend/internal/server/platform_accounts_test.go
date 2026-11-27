package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPlatformAccountLifecycle(t *testing.T) {
	a := setupAPI(t)
	create := map[string]any{"username": "enrolled-user", "email": "enrolled@example.test", "reason": "隔离验收开通"}
	// A space admin has no platform authority, either through navigation or API.
	if _, err := a.pool.Exec(a.actor(), `UPDATE sys_workspace_members SET role_code='admin'
		WHERE workspace_id=1 AND user_id=2`); err != nil {
		t.Fatal(err)
	}
	a.call("GET", "/platform/accounts", a.developer, nil, http.StatusForbidden)
	a.call("POST", "/platform/accounts", a.developer, create, http.StatusForbidden)
	a.call("GET", "/platform/workspaces", a.developer, nil, http.StatusForbidden)
	created := a.call("POST", "/platform/accounts", a.admin, create, http.StatusCreated)
	account := created["account"].(map[string]any)
	credential := created["credential"].(map[string]any)
	if account["status"] != "pending" || account["is_platform_admin"] != false || credential["purpose"] != "activate" {
		t.Fatal("new platform account has an invalid initial state")
	}
	id := int64(account["id"].(float64))
	path := fmt.Sprintf("/platform/accounts/%d", id)
	var memberships int
	if err := a.pool.QueryRow(a.actor(), `SELECT count(*) FROM sys_workspace_members WHERE user_id=$1`, id).
		Scan(&memberships); err != nil || memberships != 0 {
		t.Fatal("platform account got a first membership", memberships, err)
	}
	pending := a.call("GET", "/platform/accounts?keyword=ENROLLED&status=pending&page_size=1", a.admin, nil, http.StatusOK)
	if pending["total"] != float64(1) || len(pending["items"].([]any)) != 1 {
		t.Fatal("pending directory filters did not match the account")
	}
	a.call("PATCH", path+"/status", a.admin, map[string]any{"active": true, "expected_version": account["version"], "reason": "隔离验收"}, http.StatusConflict)

	reissued := a.call("POST", path+"/credential", a.admin, map[string]any{
		"purpose": "activate", "expected_version": account["version"], "reason": "重新开通",
	}, http.StatusOK)
	const originalPassword = "Acceptance-password-original"
	const newPassword = "Acceptance-password-reset"
	exchange := func(token any, password string, expected int) map[string]any {
		return a.call("POST", "/auth/account-credential", "", map[string]any{"token": token, "password": password}, expected)
	}
	oldToken := credential["token"]
	newToken := reissued["credential"].(map[string]any)["token"]
	exchange(oldToken, originalPassword, http.StatusBadRequest)
	if exchange(newToken, originalPassword, http.StatusOK)["purpose"] != "activate" {
		t.Fatal("activation did not return its operation")
	}
	exchange(newToken, originalPassword, http.StatusBadRequest)
	login := func(password string, expected int) map[string]any {
		return a.call("POST", "/auth/login", "", map[string]any{"username": "enrolled-user", "password": password}, expected)
	}
	oldSession := login(originalPassword, http.StatusOK)
	a.call("GET", "/platform/status", oldSession["access_token"].(string), nil, http.StatusOK)
	a.call("GET", "/workspaces/1/members", oldSession["access_token"].(string), nil, http.StatusNotFound)
	var version float64
	readAccount := func() {
		list := a.call("GET", "/platform/accounts?keyword=enrolled", a.admin, nil, http.StatusOK)
		account = list["items"].([]any)[0].(map[string]any)
		version = account["version"].(float64)
	}
	readAccount()
	if account["status"] != "active" || account["local_login"] != true {
		t.Fatal("activated account has no local login")
	}
	reset := func() any {
		data := a.call("POST", path+"/credential", a.admin, map[string]any{
			"purpose": "reset", "expected_version": version, "reason": "本人申请重置",
		}, http.StatusOK)
		readAccount()
		return data["credential"].(map[string]any)["token"]
	}
	expired := reset()
	if _, err := a.pool.Exec(a.actor(), `UPDATE sys_platform_account_credentials
		SET created_at=now()-interval '20 minutes',expires_at=now()-interval '5 minutes' WHERE user_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	exchange(expired, newPassword, http.StatusBadRequest)
	resetToken := reset()
	credentialRequest := platformTestRequest{method: "POST", path: "/auth/account-credential", body: map[string]any{
		"token": resetToken, "password": newPassword,
	}}
	statuses := parallelPlatformRequests(t, a, credentialRequest, credentialRequest)
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusBadRequest {
		t.Fatal("simultaneous credential redemption must succeed exactly once", statuses)
	}
	exchange(resetToken, newPassword, http.StatusBadRequest)
	a.call("GET", "/auth/me", oldSession["access_token"].(string), nil, http.StatusUnauthorized)
	refreshBody := func(session map[string]any) map[string]any {
		return map[string]any{"refresh_token": session["refresh_token"]}
	}
	a.call("POST", "/auth/refresh", "", refreshBody(oldSession), http.StatusUnauthorized)
	login(originalPassword, http.StatusUnauthorized)
	activeSession := login(newPassword, http.StatusOK)
	readAccount()
	disabled := a.call("PATCH", path+"/status", a.admin, map[string]any{
		"active": false, "expected_version": version, "reason": "隔离验收停用",
	}, http.StatusOK)
	if disabled["status"] != "inactive" {
		t.Fatal("account was not disabled")
	}
	a.call("GET", "/auth/me", activeSession["access_token"].(string), nil, http.StatusUnauthorized)
	a.call("POST", "/auth/refresh", "", refreshBody(activeSession), http.StatusUnauthorized)
	login(newPassword, http.StatusUnauthorized)
	a.call("PATCH", path+"/status", a.admin, map[string]any{
		"active": true, "expected_version": version, "reason": "隔离验收乐观锁",
	}, http.StatusConflict)
	enabled := a.call("PATCH", path+"/status", a.admin, map[string]any{
		"active": true, "expected_version": disabled["version"], "reason": "隔离验收恢复",
	}, http.StatusOK)
	a.call("GET", "/auth/me", activeSession["access_token"].(string), nil, http.StatusUnauthorized)
	a.call("POST", "/auth/refresh", "", refreshBody(activeSession), http.StatusUnauthorized)
	fresh := login(newPassword, http.StatusOK)
	a.call("POST", path+"/revoke-sessions", a.admin, map[string]any{
		"expected_version": enabled["version"], "reason": "安全事件撤销会话",
	}, http.StatusOK)
	a.call("GET", "/auth/me", fresh["access_token"].(string), nil, http.StatusUnauthorized)
	a.call("PATCH", "/platform/accounts/1/status", a.admin, map[string]any{
		"active": false, "expected_version": 1, "reason": "末位管理员保护",
	}, http.StatusForbidden)

	deadline := time.Now().Add(5 * time.Second)
	for {
		var reason, snapshot string
		err := a.pool.QueryRow(a.actor(), `SELECT context->>'platform_account_reason', context::text FROM sys_audit_logs
			WHERE action='PLATFORM_ACCOUNT_STATUS' AND status=1 AND workspace_id=0
			AND context->>'platform_account_id'=$1 AND context->>'platform_account_status'='inactive'
			ORDER BY id DESC LIMIT 1`, fmt.Sprint(id)).Scan(&reason, &snapshot)
		if err == nil {
			if reason != "隔离验收停用" || strings.Contains(snapshot, activeSession["access_token"].(string)) ||
				strings.Contains(snapshot, resetToken.(string)) || strings.Contains(snapshot, newPassword) {
				t.Fatal("identity audit lost the reason or exposes a credential")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("platform account audit entry was not delivered", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestPlatformAccountsProtectLastSuperAdminConcurrently(t *testing.T) {
	a := setupAPI(t)
	if _, err := a.pool.Exec(a.actor(), `UPDATE sys_users SET is_platform_admin=true WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	a.call("GET", "/platform/accounts", a.developer, nil, http.StatusOK)
	body := map[string]any{"active": false, "expected_version": 1, "reason": "隔离超管恢复保护"}
	statuses := parallelPlatformRequests(t, a,
		platformTestRequest{method: "PATCH", path: "/platform/accounts/2/status", token: a.admin, body: body},
		platformTestRequest{method: "PATCH", path: "/platform/accounts/1/status", token: a.developer, body: body})
	if statuses[0] != http.StatusOK || (statuses[1] != http.StatusForbidden && statuses[1] != http.StatusUnauthorized) {
		t.Fatal("one superadmin must keep account administration access", statuses)
	}
	var available int
	if err := a.pool.QueryRow(a.actor(), `SELECT count(*) FROM sys_users WHERE is_platform_admin AND is_active`).Scan(&available); err != nil || available != 1 {
		t.Fatal("unexpected remaining superadmins", available, err)
	}
}

type platformTestRequest struct {
	method, path, token string
	body                any
}

func parallelPlatformRequests(t *testing.T, a *apiTest, requests ...platformTestRequest) []int {
	t.Helper()
	type result struct {
		status int
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, len(requests))
	for _, in := range requests {
		raw, err := json.Marshal(in.body)
		if err != nil {
			t.Fatal("could not construct isolated account request")
		}
		req, err := http.NewRequest(in.method, a.server.URL+"/api/v1"+in.path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal("could not prepare isolated account request")
		}
		req.Header.Set("Content-Type", "application/json")
		if in.token != "" {
			req.Header.Set("Authorization", "Bearer "+in.token)
		}
		go func() {
			<-start
			res, err := a.server.Client().Do(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			res.Body.Close()
			results <- result{status: res.StatusCode}
		}()
	}
	close(start)
	statuses := make([]int, 0, len(requests))
	for range requests {
		r := <-results
		if r.err != nil {
			t.Fatal("parallel isolated account request failed", r.err)
		}
		statuses = append(statuses, r.status)
	}
	slices.Sort(statuses)
	return statuses
}

func TestPlatformWorkspacesShowsExplicitFounder(t *testing.T) {
	a := setupAPI(t)
	a.call("GET", "/platform/workspaces", a.readonly, nil, http.StatusForbidden)
	workspace := a.call("POST", "/workspaces", a.admin, map[string]any{
		"name": "Directory Acceptance", "request_id": testRequestID(),
	}, http.StatusOK)
	directory := a.call("GET", "/platform/workspaces?keyword=directory&page=1&page_size=5", a.admin, nil, http.StatusOK)
	if directory["total"] != float64(1) {
		t.Fatal("directory did not filter to the newly created workspace")
	}
	row := directory["items"].([]any)[0].(map[string]any)
	if row["id"] != workspace["id"] || row["enabled"] != true || row["created_at"] == nil {
		t.Fatal("workspace directory metadata differs from creation receipt")
	}
	founder := row["founding_admin"].(map[string]any)
	if founder["id"] != float64(1) || founder["username"] != "admin" {
		t.Fatal("workspace directory failed to name the explicit founding admin")
	}
}
