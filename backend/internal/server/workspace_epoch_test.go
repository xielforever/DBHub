package server

import (
	"context"
	"fmt"
	"github.com/user/dbhub/internal/auth"
	"testing"
)

func TestMembershipRevokeRestoreInvalidatesOldQueryAndTransaction(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	id, _ := dashboardReport(f)
	did := publishTestDashboard(f, id, 0)
	oldCtx, e := a.deps.UserRepo.BindWorkspace(context.WithValue(a.actor(), auth.ClaimsContextKey, &auth.Claims{Subject: 3, Role: "readonly"}), 1)
	if e != nil {
		t.Fatal(e)
	}
	execution, e := a.deps.Conns.ManagedReports().ResolvePublished(oldCtx, id, 1)
	if e != nil {
		t.Fatal(e)
	}
	refresh := a.call("POST", fmt.Sprintf("/managed-dashboards/%d/refreshes", did), a.readonly, map[string]any{"mode": "published"}, 200)
	a.call("PATCH", "/workspaces/1/members/3", a.admin, map[string]any{"expected_version": 1, "active": false}, 200)
	a.call("PATCH", "/workspaces/1/members/3", a.admin, map[string]any{"expected_version": 2, "active": true}, 200)
	if e = a.deps.Conns.ManagedReports().CheckExecution(oldCtx, execution); e == nil {
		t.Fatal("old execution regained authority after a revoke/restore")
	}
	a.call("POST", "/dashboard-refreshes/"+refresh["refresh_id"].(string)+"/tiles/required/execute", a.readonly, map[string]any{}, 409)
	next := a.call("POST", fmt.Sprintf("/managed-dashboards/%d/refreshes", did), a.readonly, map[string]any{"mode": "published"}, 200)
	a.call("POST", "/dashboard-refreshes/"+next["refresh_id"].(string)+"/tiles/required/execute", a.readonly, map[string]any{}, 200)
	a.call("POST", "/users", a.admin, map[string]any{"username": "epoch_admin", "password": "TestPassword123!", "role": "admin"}, 201)
	administrator := a.call("POST", "/auth/login", "", map[string]any{"username": "epoch_admin", "password": "TestPassword123!"}, 200)["access_token"].(string)
	transaction := a.call("POST", "/query/transaction/begin", a.admin, map[string]any{"connection_id": a.connID}, 200)
	a.call("POST", "/query/execute", a.admin, map[string]any{"request_id": testRequestID(), "connection_id": a.connID, "transaction_id": transaction["transaction_id"], "sql": "UPDATE " + a.table + " SET secret='epoch' WHERE id=1"}, 200)
	a.call("PATCH", "/workspaces/1/members/1", administrator, map[string]any{"expected_version": 1, "active": false}, 200)
	a.call("PATCH", "/workspaces/1/members/1", administrator, map[string]any{"expected_version": 2, "active": true}, 200)
	a.call("POST", "/query/transaction/commit", a.admin, map[string]any{"connection_id": a.connID, "transaction_id": transaction["transaction_id"]}, 404)
	var secret string
	if e = a.pool.QueryRow(context.Background(), `SELECT secret FROM public.`+a.table+` WHERE id=1`).Scan(&secret); e != nil || secret != "one" {
		t.Fatal("old transaction was committed after a membership epoch change", secret, e)
	}
}
