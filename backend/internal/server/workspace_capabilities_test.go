package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
)

func TestWorkspaceCapabilitiesRealDatabase(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	create := func(name string, capabilities []string) (int64, string) {
		out := a.call("POST", "/users", a.admin, map[string]any{"username": name, "password": "TestPassword123!", "role": "readonly"}, 201)
		uid := int64(out["id"].(float64))
		if err := a.deps.UserRepo.UpdateMembership(a.actor(), uid, "", nil, 1, capabilities); err != nil {
			t.Fatal(err)
		}
		token := a.call("POST", "/auth/login", "", map[string]any{"username": name, "password": "TestPassword123!"}, 200)["access_token"].(string)
		if a.call("GET", "/auth/me", token, nil, 200)["role"] != "readonly" {
			t.Fatal("delegation changed global/base role")
		}
		return uid, token
	}
	reviewerID, reviewer := create("report_reviewer", []string{"report_review"})
	_, sourceAdmin := create("source_manager", []string{"datasource_manage"})
	_, auditor := create("workspace_auditor", []string{"audit_view"})
	a.call("GET", "/managed-reports?scope=all", reviewer, nil, 200)
	a.call("GET", "/report-accounts", reviewer, nil, 200)
	a.call("GET", "/user-groups", reviewer, nil, 200)
	a.call("POST", "/user-groups", reviewer, map[string]any{}, 403)
	a.call("GET", "/audit-logs", reviewer, nil, 403)
	a.call("POST", "/users", reviewer, map[string]any{}, 403)
	a.call("POST", "/report-accounts", reviewer, map[string]any{}, 403)
	a.call("POST", "/datasource-definitions", reviewer, map[string]any{"name": "forbidden", "visibility": "global", "config": map[string]any{}}, 403)
	a.call("GET", fmt.Sprintf("/datasource-definitions/%d/access", f.definition), sourceAdmin, nil, 200)
	a.call("GET", "/report-accounts", sourceAdmin, nil, 200)
	a.call("GET", "/user-groups", sourceAdmin, nil, 200)
	a.call("POST", "/user-groups", sourceAdmin, map[string]any{}, 403)
	a.call("GET", "/managed-reports?scope=all", sourceAdmin, nil, 403)
	a.call("GET", "/audit-logs", sourceAdmin, nil, 403)
	a.call("GET", fmt.Sprintf("/connections/%d", a.connID), sourceAdmin, nil, 404)
	a.call("GET", "/audit-logs", auditor, nil, 200)
	a.call("GET", "/managed-reports?scope=all", auditor, nil, 403)
	a.call("GET", "/users", auditor, nil, 403)
	a.call("GET", fmt.Sprintf("/datasource-definitions/%d/access", f.definition), auditor, nil, 404)
	id, _ := f.draft("SELECT id,email FROM public." + f.view)
	review := f.submit(id, 1)
	validatePath := fmt.Sprintf("/report-reviews/%d/validate", review)
	payload := map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}
	a.call("POST", validatePath, sourceAdmin, payload, 403)
	a.call("POST", validatePath, auditor, payload, 403)
	validation := a.call("POST", validatePath, reviewer, payload, 200)["validation_id"]
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), reviewer, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation}, 200)
	a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", id), reviewer, map[string]any{"request_id": testRequestID(), "review_id": review, "kind": "publish", "expected_publication_no": 0}, 200)
	f.run(id, 1, 200)
	a.call("POST", fmt.Sprintf("/managed-reports/%d/transfer", id), reviewer, map[string]any{"request_id": testRequestID(), "owner_id": 1, "version": 1}, 403)
	// Dashboard assignment, real tile execution, validation and release use the
	// same delegated review capability without granting authoring or source access.
	reportID, _ := dashboardReportAudience(f, []int64{1, 2, 3, reviewerID})
	definition := map[string]any{"name": "职责分离看板", "tiles": []any{map[string]any{"key": "stock", "report_id": reportID, "required": true, "width": "full", "binding_policy": "follow_published"}}, "user_ids": []int64{3}, "group_ids": []int64{}, "quality": map[string]any{"allow_unknown_freshness": false, "max_watermark_skew_seconds": 300}}
	dashboard := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": definition}, 200)
	dashboardID := int64(dashboard["id"].(float64))
	dashboardPath := fmt.Sprintf("/managed-dashboards/%d", dashboardID)
	submitted := a.call("POST", dashboardPath+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": reviewerID}, 200)
	dashboardReview := int64(submitted["id"].(float64))
	refresh := a.call("POST", dashboardPath+"/refreshes", reviewer, map[string]any{"mode": "review", "review_id": dashboardReview}, 200)
	refreshID := refresh["refresh_id"].(string)
	a.call("POST", "/dashboard-refreshes/"+refreshID+"/tiles/stock/execute", reviewer, map[string]any{}, 200)
	a.call("POST", fmt.Sprintf("/dashboard-reviews/%d/validate", dashboardReview), reviewer, map[string]any{"request_id": testRequestID(), "refresh_id": refreshID}, 200)
	a.call("POST", fmt.Sprintf("/dashboard-reviews/%d/decision", dashboardReview), reviewer, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
	a.call("POST", dashboardPath+"/releases", reviewer, map[string]any{"request_id": testRequestID(), "kind": "publish", "review_id": dashboardReview, "expected_publication_no": 0}, 200)
	a.call("GET", fmt.Sprintf("/published-dashboards/%d", dashboardID), a.readonly, nil, 200)
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{Subject: reviewerID, Role: "readonly"})
	old, err := a.deps.UserRepo.BindWorkspace(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.deps.UserRepo.UpdateMembership(old, reviewerID, "", nil, 0, []string{"datasource_manage"}); err == nil {
		t.Fatal("reviewer escalated membership capabilities")
	}
	if err = a.deps.UserRepo.UpdateMembership(a.actor(), reviewerID, "", nil, 2, []string{}); err != nil {
		t.Fatal(err)
	}
	a.call("GET", "/managed-reports?scope=all", reviewer, nil, 403)
	if _, err = a.deps.Conns.ManagedReports().Get(old, id); err == nil {
		t.Fatal("revoked in-flight scope remained usable")
	}
	if err = a.deps.UserRepo.UpdateMembership(a.actor(), reviewerID, "", nil, 3, []string{"report_review"}); err != nil {
		t.Fatal(err)
	}
	a.call("GET", "/managed-reports?scope=all", reviewer, nil, 200)
	if _, err = a.deps.Conns.ManagedReports().Get(old, id); err == nil {
		t.Fatal("restoring a grant resurrected an old membership epoch")
	}
	if err = a.deps.UserRepo.UpdateMembership(a.actor(), 2, "", nil, 1, []string{"report_review"}); err != nil {
		t.Fatal(err)
	}
	self, _ := f.draft("SELECT id,email FROM public." + f.view)
	selfReview := f.submit(self, 1)
	a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", selfReview), a.developer, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 400)
	if !db.ActorReportReviewer(old) {
		t.Fatal("test did not retain the old delegated context")
	}
}
