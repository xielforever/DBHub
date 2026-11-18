package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestIAMFoundationHTTPUsesCurrentOriginalWorkspace(t *testing.T) {
	a := setupAPI(t)
	for _, path := range []string{"/workspaces/1/iam/foundation", "/workspaces/1/members", "/workspaces/1/policy", "/workspaces/1/connections"} {
		req, e := http.NewRequest(http.MethodGet, a.server.URL+"/api/v1"+path, nil)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+a.admin)
		req.Header.Set("X-Workspace-ID", "2")
		res, e := a.server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("conflicting space sources accepted: %s %d", path, res.StatusCode)
		}
	}
	for _, tc := range []struct {
		token   string
		subject float64
		role    string
	}{{a.admin, 1, "admin"}, {a.developer, 2, "developer"}, {a.readonly, 3, "readonly"}} {
		data := a.call("GET", "/workspaces/1/iam/foundation?user_id=999&role=admin", tc.token, nil, 200)
		if data["activation_ready"] != false || data["stage"] != "FOUNDATION_ONLY" {
			t.Fatal("incomplete bridge advertised activation")
		}
		snapshot := data["snapshot"].(map[string]any)
		if snapshot["subject"] != tc.subject || snapshot["workspace_id"] != float64(1) || snapshot["legacy_data_role"] != tc.role {
			t.Fatal("client chose subject or data identity", snapshot)
		}
	}
	// The same old token loses workspace access after a durable freeze. The
	// global self/identity entry remains reachable without workspace membership.
	if _, e := a.pool.Exec(a.actor(), `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,3,'44444444-4444-4444-8444-444444444444','member_freeze','HTTP fixture freeze')`); e != nil {
		t.Fatal(e)
	}
	a.call("GET", "/workspaces/1/iam/foundation", a.readonly, nil, 404)
	a.call("GET", "/workspaces/1/connections", a.readonly, nil, 404)
	a.call("GET", "/auth/me", a.readonly, nil, 200)
	a.call("GET", "/platform/status", a.readonly, nil, 200)
}

func TestIAMFoundationHTTPPermissionStoreFailureIsUnavailable(t *testing.T) {
	a := setupAPI(t)
	// Rename only the disposable test-schema table. This models a missing or
	// unavailable authorization store without altering the live local service.
	if _, e := a.pool.Exec(a.actor(), `ALTER TABLE sys_iam_workspace_state RENAME TO iam_state_unavailable`); e != nil {
		t.Fatal(e)
	}
	defer a.pool.Exec(a.actor(), `ALTER TABLE iam_state_unavailable RENAME TO sys_iam_workspace_state`)
	req, _ := http.NewRequest(http.MethodGet, a.server.URL+"/api/v1/workspaces/1/iam/foundation", nil)
	req.Header.Set("Authorization", "Bearer "+a.admin)
	res, e := a.server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if e = json.NewDecoder(res.Body).Decode(&envelope); e != nil {
		t.Fatal(e)
	}
	if res.StatusCode != 503 || envelope.Code != 50300 || len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		t.Fatal(fmt.Sprintf("permission failure returned data: %d %d", res.StatusCode, envelope.Code))
	}
}

func TestIAMFoundationHTTPMissingGenerationFailsClosed(t *testing.T) {
	a := setupAPI(t)
	if _, e := a.pool.Exec(a.actor(), `DELETE FROM sys_iam_workspace_state WHERE workspace_id=1`); e != nil {
		t.Fatal(e)
	}
	a.call("GET", "/workspaces/1/iam/foundation", a.admin, nil, 503)
	a.call("GET", "/workspaces/1/connections", a.admin, nil, 503)
	a.call("GET", "/auth/me", a.admin, nil, 200)
}
