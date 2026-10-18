package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/user/dbhub/internal/db"
)

func TestReviewFixTransactionRechecksSourceBeforeCommit(t *testing.T) {
	a := setupAPI(t)
	base, password, err := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"source-grant", "group-membership", "disabled-definition", "connection-config", "deleted-connection"} {
		for _, operation := range []string{"commit", "rollback"} {
			t.Run(change+"/"+operation, func(t *testing.T) {
				a.t = t
				if _, err := a.pool.Exec(context.Background(), "UPDATE public."+a.table+" SET secret='one' WHERE id=1"); err != nil {
					t.Fatal(err)
				}
				source := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": change + operation, "visibility": "global", "access_mode": "restricted", "config": db.ConfigOf(base)}, 201)
				path := fmt.Sprintf("/datasource-definitions/%.0f", source["id"])
				users, groups := []int64{2}, []int64{}
				var groupID int64
				if change == "group-membership" {
					group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": change + operation, "member_ids": []int64{2}}, 200)
					groupID = int64(group["id"].(float64))
					users, groups = []int64{}, []int64{groupID}
				}
				a.call("PUT", path+"/access", a.admin, map[string]any{"mode": "restricted", "user_ids": users, "group_ids": groups, "expected_version": 1}, 200)
				connection := a.call("POST", path+"/connections", a.developer, map[string]any{"username": base.Username, "password": password, "confirmed": true}, 201)
				cid := int64(connection["id"].(float64))
				transaction := a.call("POST", "/query/transaction/begin", a.developer, map[string]any{"connection_id": cid}, 200)
				input := map[string]any{"connection_id": cid, "transaction_id": transaction["transaction_id"]}
				a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": cid, "transaction_id": transaction["transaction_id"], "sql": "UPDATE " + a.table + " SET secret='must-rollback' WHERE id=1"}, 200)
				switch change {
				case "source-grant":
					a.call("PUT", path+"/access", a.admin, map[string]any{"mode": "restricted", "user_ids": []int64{}, "group_ids": []int64{}, "expected_version": 2}, 200)
				case "group-membership":
					a.call("PUT", fmt.Sprintf("/user-groups/%d", groupID), a.admin, map[string]any{"name": change + operation, "member_ids": []int64{}, "expected_version": 1}, 200)
				case "disabled-definition":
					a.call("PATCH", path+"/status", a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": 1}, 200)
				case "connection-config":
					a.call("POST", fmt.Sprintf("/connections/%d/revision", cid), a.developer, map[string]any{"definition_revision_id": source["id"], "name": "updated transaction binding", "username": base.Username, "password": password, "confirmed": true}, 200)
				case "deleted-connection":
					a.call("DELETE", fmt.Sprintf("/connections/%d", cid), a.developer, nil, 200)
				}
				want := 200
				if operation == "commit" {
					want = 409
				}
				a.call("POST", "/query/transaction/"+operation, a.developer, input, want)
				action := "TRANSACTION_COMMIT"
				if operation == "rollback" {
					action = "TRANSACTION_ROLLBACK"
				}
				row := expectAudit(t, a, action, "rolled_back")
				if row.Context.TransactionID != transaction["transaction_id"] || operation == "commit" && row.Status != 0 {
					t.Fatal("revoked commit was audited as a successful commit")
				}
				var secret string
				if err := a.pool.QueryRow(context.Background(), "SELECT secret FROM public."+a.table+" WHERE id=1").Scan(&secret); err != nil || secret != "one" {
					t.Fatal("revoked transaction changed target data", secret, err)
				}
				status := a.call("GET", fmt.Sprintf("/query/transaction/status?connection_id=%d", cid), a.developer, nil, 200)
				if status["active"] != false {
					t.Fatal("finished transaction retained its target session")
				}
			})
		}
	}
	t.Run("rollback-result-unknown", func(t *testing.T) {
		a.t = t
		cid := a.connectionFor(a.developer)
		transaction := a.call("POST", "/query/transaction/begin", a.developer, map[string]any{"connection_id": cid}, 200)
		a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": cid, "transaction_id": transaction["transaction_id"], "sql": "UPDATE " + a.table + " SET secret='must-rollback' WHERE id=1"}, 200)
		result := a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": cid, "transaction_id": transaction["transaction_id"], "sql": "SELECT pg_backend_pid()"}, 200)
		pid := int(result["rows"].([]any)[0].([]any)[0].(float64))
		if _, err := a.pool.Exec(context.Background(), "SELECT pg_terminate_backend($1)", pid); err != nil {
			t.Fatal(err)
		}
		a.call("PATCH", fmt.Sprintf("/datasource-definitions/%d/status", *base.DefinitionRevisionID), a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": 3}, 200)
		a.call("POST", "/query/transaction/commit", a.developer, map[string]any{"connection_id": cid, "transaction_id": transaction["transaction_id"]}, 503)
		row := expectAudit(t, a, "TRANSACTION_COMMIT", "unknown")
		if row.Status != 0 {
			t.Fatal("unconfirmed rollback was audited as success")
		}
	})
}

func TestReviewFixPublishedAudienceVersionCanBeRestored(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	required, _ := dashboardReport(f)
	report, _ := dashboardReportAudience(f, []int64{1, 2})
	dashboard := publishTestDashboard(f, required, report)
	original := a.call("GET", fmt.Sprintf("/managed-reports/%d/versions", report), a.admin, nil, 200)["items"].([]any)[0].(map[string]any)["review_id"]
	request := a.call("POST", fmt.Sprintf("/published-dashboards/%d/access-requests", dashboard), a.readonly, map[string]any{"request_id": testRequestID(), "tile_key": "optional", "reason": "恢复验收"}, 200)
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "恢复受众", "member_ids": []int64{3}}, 200)
	gid := int64(group["id"].(float64))
	proposal := a.call("POST", fmt.Sprintf("/managed-reports/%d/audience-changes", report), a.developer, map[string]any{"request_id": testRequestID(), "base_publication_no": 1, "user_ids": []int64{1, 2, 3}, "group_ids": []int64{gid}, "request_ids": []any{request["id"]}}, 200)
	review := int64(proposal["id"].(float64))
	validation := f.validate(review, []int64{1, 2, 3}, []int64{gid})
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(report, 1, review, "publish")
	var status string
	if err := a.pool.QueryRow(context.Background(), `SELECT status FROM sys_access_requests WHERE id=$1`, request["id"]).Scan(&status); err != nil || status != "fulfilled" {
		t.Fatal("request did not complete with first publication", status, err)
	}
	if _, err := a.pool.Exec(context.Background(), `UPDATE sys_access_requests SET expires_at=now()-interval '1 day' WHERE id=$1`, request["id"]); err != nil {
		t.Fatal(err)
	}
	a.call("PUT", fmt.Sprintf("/user-groups/%d", gid), a.admin, map[string]any{"name": "恢复受众", "member_ids": []int64{2, 3}, "expected_version": 1}, 200)
	f.release(report, 2, 0, "unpublish")
	f.release(report, 3, review, "publish")
	a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", report), a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 4, "review_id": original, "kind": "rollback"}, 200)
	f.run(report, 5, 404)
	f.release(report, 5, review, "rollback")
	f.run(report, 6, 200)
	// Restoring history must still reject stale commands and unavailable accounts.
	a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", report), a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 5, "review_id": original, "kind": "rollback"}, 409)
	f.release(report, 6, 0, "unpublish")
	a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": 1, "enabled": false, "expected_reports": 1}, 200)
	ctx, err := a.deps.UserRepo.BindWorkspace(a.actor(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.deps.Conns.ManagedReports().Release(ctx, testRequestID(), report, 7, review, "rollback", 0); err == nil {
		t.Fatal("historical recovery bypassed disabled report account")
	}
}

func reviewFixValidateDashboard(a *apiTest, id int64, review any, token string) string {
	refresh := a.call("POST", fmt.Sprintf("/managed-dashboards/%d/refreshes", id), token, map[string]any{"mode": "review", "review_id": review}, 200)
	rid := refresh["refresh_id"].(string)
	for _, item := range refresh["tiles"].([]any) {
		a.call("POST", "/dashboard-refreshes/"+rid+"/tiles/"+item.(map[string]any)["key"].(string)+"/execute", token, map[string]any{}, 200)
	}
	a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/validate", review), token, map[string]any{"request_id": testRequestID(), "refresh_id": rid}, 200)
	return rid
}

func reviewFixPublishDashboard(a *apiTest, report int64, users, groups []int64) int64 {
	def := map[string]any{"name": "独立审批验收", "tiles": []any{map[string]any{"key": "stock", "report_id": report, "required": true, "width": "full"}}, "user_ids": users, "group_ids": groups, "quality": map[string]any{"max_watermark_skew_seconds": 300}}
	created := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": def}, 200)
	id := int64(created["id"].(float64))
	path := fmt.Sprintf("/managed-dashboards/%d", id)
	review := a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": 1}, 200)["id"]
	reviewFixValidateDashboard(a, id, review, a.admin)
	a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
	a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 0, "review_id": review, "kind": "publish"}, 200)
	return id
}

func TestReviewFixDashboardBeneficiaryCheckedAtEveryGate(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	a.call("POST", "/users", a.admin, map[string]any{"username": "beneficiary_admin", "password": "TestPassword123!", "role": "admin"}, 201)
	beneficiary := a.call("POST", "/auth/login", "", map[string]any{"username": "beneficiary_admin", "password": "TestPassword123!"}, 200)["access_token"].(string)
	report, _ := dashboardReportAudience(f, []int64{1, 2, 3, 4})
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "既有看板受众", "member_ids": []int64{4}}, 200)
	gid := int64(group["id"].(float64))
	proposal := a.call("POST", fmt.Sprintf("/managed-reports/%d/audience-changes", report), a.developer, map[string]any{"request_id": testRequestID(), "base_publication_no": 1, "user_ids": []int64{1, 2, 3, 4}, "group_ids": []int64{gid}}, 200)
	rv := int64(proposal["id"].(float64))
	validation := f.validate(rv, []int64{1, 2, 3, 4}, []int64{gid})
	a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", rv), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
	f.release(report, 1, rv, "publish")
	groupVersion := func() int64 {
		var version int64
		if err := a.pool.QueryRow(context.Background(), `SELECT version FROM sys_user_groups WHERE id=$1`, gid).Scan(&version); err != nil {
			a.t.Fatal(err)
		}
		return version
	}
	t.Run("draft-submit-preflight-and-reassign", func(t *testing.T) {
		a.t = t
		for _, mode := range []string{"direct", "group", "offline"} {
			id := reviewFixPublishDashboard(a, report, []int64{3}, nil)
			path := fmt.Sprintf("/managed-dashboards/%d", id)
			publication := 1
			if mode == "offline" {
				a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 1, "kind": "unpublish"}, 200)
				publication = 2
			}
			draft := a.call("GET", path, a.developer, nil, 200)["draft"].(map[string]any)
			users, groups := []int64{3, 4}, []int64{}
			if mode == "group" {
				users, groups = []int64{3}, []int64{gid}
			}
			draft["user_ids"], draft["group_ids"] = users, groups
			a.call("PUT", path+"/draft", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "definition": draft}, 200)
			check := a.call("POST", path+"/preflight", a.developer, map[string]any{"version": 2}, 200)
			for _, candidate := range check["eligible_reviewers"].([]any) {
				if candidate.(map[string]any)["id"] == float64(4) {
					t.Fatal("new beneficiary advertised as an eligible reviewer")
				}
			}
			a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 2, "reviewer_id": 4}, 400)
			var review any
			if mode == "offline" {
				review = a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 2, "reviewer_id": 1}, 200)["id"]
			} else {
				review = a.call("POST", path+"/audience-changes", a.developer, map[string]any{"request_id": testRequestID(), "base_publication_no": 1, "reviewer_id": 1, "user_ids": users, "group_ids": groups}, 200)["id"]
			}
			a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/reassign", review), beneficiary, map[string]any{"request_id": testRequestID(), "reviewer_id": 4}, 400)
			reviewFixValidateDashboard(a, id, review, a.admin)
			a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
			a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": publication, "review_id": review, "kind": "publish"}, 200)
			a.call("GET", fmt.Sprintf("/published-dashboards/%d", id), beneficiary, nil, 200)
		}
	})
	for _, gate := range []string{"validate", "decision", "release"} {
		t.Run("group-revocation-before-"+gate, func(t *testing.T) {
			a.t = t
			a.call("PUT", fmt.Sprintf("/user-groups/%d", gid), a.admin, map[string]any{"name": "既有看板受众", "member_ids": []int64{4}, "expected_version": groupVersion()}, 200)
			id := reviewFixPublishDashboard(a, report, []int64{3}, []int64{gid})
			path := fmt.Sprintf("/managed-dashboards/%d", id)
			review := a.call("POST", path+"/audience-changes", a.developer, map[string]any{"request_id": testRequestID(), "base_publication_no": 1, "reviewer_id": 4, "user_ids": []int64{3, 4}, "group_ids": []int64{}}, 200)["id"]
			rid := reviewFixValidateDashboard(a, id, review, beneficiary)
			if gate == "release" {
				a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/decision", review), beneficiary, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 200)
			}
			a.call("PUT", fmt.Sprintf("/user-groups/%d", gid), a.admin, map[string]any{"name": "既有看板受众", "member_ids": []int64{}, "expected_version": groupVersion()}, 200)
			switch gate {
			case "validate":
				a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/validate", review), beneficiary, map[string]any{"request_id": testRequestID(), "refresh_id": rid}, 400)
			case "decision":
				a.call("POST", fmt.Sprintf("/dashboard-reviews/%.0f/decision", review), beneficiary, map[string]any{"request_id": testRequestID(), "decision": "approved"}, 400)
			case "release":
				a.call("POST", path+"/releases", a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 1, "review_id": review, "kind": "publish"}, 400)
			}
			a.call("GET", fmt.Sprintf("/published-dashboards/%d", id), beneficiary, nil, 404)
		})
	}
}
