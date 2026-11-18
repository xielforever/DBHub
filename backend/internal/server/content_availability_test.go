package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/user/dbhub/internal/config"
)

func verifyContentAvailability(t *testing.T, a *apiTest, f *managedFixture, dashboard, report, dashReview, stopperID, reviewerID int64, stopper, business, refresh string, republishDashboard, refreshReportReviewer func()) {
	ctx := context.Background()
	dashPath, reportPath := fmt.Sprintf("/managed-dashboards/%d", dashboard), fmt.Sprintf("/managed-reports/%d", report)
	reportPublished := fmt.Sprintf("/published-reports/%d/execute", report)
	dashPublished := fmt.Sprintf("/published-dashboards/%d", dashboard)
	const reason = "应急演练与正常下线"
	request := func(action string, version int64) map[string]any {
		return map[string]any{"request_id": testRequestID(), "expected_publication_no": version, "action": action, "reason": reason}
	}
	call := func(path, token, action string, version int64, status int) map[string]any {
		t.Helper()
		return a.call("POST", path+"/availability", token, request(action, version), status)
	}
	release := func(path, token, action string, version, review int64) {
		t.Helper()
		a.call("POST", path+"/releases", token, map[string]any{
			"request_id": testRequestID(), "expected_publication_no": version, "kind": action, "review_id": review,
		}, 409)
	}
	assertVersion := func(result map[string]any, version float64) {
		t.Helper()
		if result["version"] != version {
			t.Fatalf("expected publication version %v, got %v", version, result["version"])
		}
	}
	executeReport := func(version int64, status int) map[string]any {
		t.Helper()
		return a.call("POST", reportPublished, a.readonly, map[string]any{
			"request_id": testRequestID(), "publication_no": version, "params": map[string]string{"category": "red"},
		}, status)
	}
	oldTile := "/dashboard-refreshes/" + refresh + "/tiles/needed/execute"
	for _, path := range []string{reportPath, dashPath} {
		release(path, a.admin, "unpublish", 1, 0)
		call(path, a.admin, "emergency_stop", 1, 403)
	}
	a.call("POST", dashPath+"/archive", a.developer, map[string]any{"request_id": testRequestID(), "version": 3}, 409)
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_content_operators
		(workspace_id,report_id,dashboard_id,user_id,action,valid_until) VALUES
		(1,NULL,$1,$3,'emergency_stop',now()+interval '1 hour'),
		(1,NULL,$1,$3,'emergency_restore',now()+interval '1 hour'),
		(1,NULL,$1,$4,'emergency_restore',now()+interval '1 hour'),
		(1,NULL,$1,1,'emergency_restore',now()+interval '1 hour'),
		(1,$2,NULL,$3,'emergency_stop',now()+interval '1 hour'),
		(1,$2,NULL,1,'emergency_restore',now()+interval '1 hour')`, dashboard, report, stopperID, reviewerID); err != nil {
		t.Fatal(err)
	}

	dashRequest := call(dashPath, a.developer, "request_offline", 1, 200)
	if dashRequest["dependency_count"] != float64(2) {
		t.Fatal("dashboard offline request did not record both card dependencies")
	}
	stop := request("emergency_stop", 1)
	stopped := a.call("POST", dashPath+"/availability", stopper, stop, 200)
	assertVersion(stopped, 2)
	if duplicate := a.call("POST", dashPath+"/availability", stopper, stop, 200); duplicate["id"] != stopped["id"] {
		t.Fatal("replayed emergency stop created a second release")
	}
	a.call("GET", dashPublished, a.readonly, nil, 404)
	a.call("POST", oldTile, a.readonly, map[string]any{}, 409)
	call(dashPath, stopper, "emergency_restore", 2, 403)
	revoke := func(action string) {
		t.Helper()
		for _, kind := range []string{"api", "database", "worker"} {
			if err := a.deps.UserRepo.IAMHeartbeat(ctx, kind, a.deps.IAMRouteFingerprint); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,action_id,reason)
			VALUES(1,1,1,$1::uuid,'action_revoke',$2,'验收保留显式收紧')`, testRequestID(), action); err != nil {
			t.Fatal(err)
		}
	}
	revoke("dashboard.publish")
	call(dashPath, a.admin, "emergency_restore", 2, 403)
	release(dashPath, business, "rollback", 2, dashReview)
	chooseOld := request("emergency_restore", 2)
	chooseOld["review_id"] = dashReview + 1
	a.call("POST", dashPath+"/availability", business, chooseOld, 400)
	assertVersion(call(dashPath, business, "emergency_restore", 2, 200), 3)
	a.call("GET", dashPublished, a.readonly, nil, 200)
	a.call("POST", oldTile, a.readonly, map[string]any{}, 409)
	newRefresh := a.call("POST", dashPath+"/refreshes", a.readonly, map[string]any{"mode": "published"}, 200)["refresh_id"].(string)
	a.call("POST", "/dashboard-refreshes/"+newRefresh+"/tiles/extra/execute", a.readonly, map[string]any{}, 200)

	stale := request("approve_offline", 3)
	stale["approval_id"] = dashRequest["id"]
	a.call("POST", dashPath+"/availability", business, stale, 409)
	dashRequest = call(dashPath, business, "request_offline", 3, 200)
	self := request("approve_offline", 3)
	self["approval_id"] = dashRequest["id"]
	a.call("POST", dashPath+"/availability", business, self, 403)

	reportRequest := call(reportPath, a.developer, "request_offline", 1, 200)
	if reportRequest["dependency_count"] != float64(1) {
		t.Fatal("report offline request did not record the published dashboard")
	}
	dashRequest = call(dashPath, a.developer, "request_offline", 3, 200)
	approve := request("approve_offline", 3)
	approve["approval_id"] = dashRequest["id"]
	assertVersion(a.call("POST", dashPath+"/availability", business, approve, 200), 4)
	a.call("GET", dashPublished, a.readonly, nil, 404)
	call(dashPath, business, "emergency_restore", 4, 409)
	approve = request("approve_offline", 1)
	approve["approval_id"] = reportRequest["id"]
	// The IAM restriction advances the member version. An unrelated review
	// may resume after authorized refresh of the now-stale business mapping.
	a.call("POST", reportPath+"/availability", a.admin, approve, 403)
	refreshReportReviewer()
	approve["request_id"] = testRequestID()
	a.call("POST", reportPath+"/availability", a.admin, approve, 409)
	republishDashboard()
	a.call("GET", dashPublished, a.readonly, nil, 200)

	params := map[string]string{"category": "red"}
	export := a.call("POST", fmt.Sprintf("/published-reports/%d/exports", report), a.readonly, map[string]any{
		"request_id": testRequestID(), "publication_no": 1, "format": "csv", "reason": reason, "params": params,
	}, 200)
	exportPath := fmt.Sprintf("/report-exports/%d", int64(export["id"].(float64)))
	a.call("POST", exportPath+"/review", business, map[string]any{"version": 1, "decision": "approved"}, 200)
	a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(),
		"definition": map[string]any{"name": "下线验收订阅", "report_id": report, "publication_no": 1, "interval_minutes": 5, "enabled": true, "params": params},
	}, 200)
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	if generated, err := service.Subscriptions.GenerateDue(ctx); err != nil || generated != 1 {
		t.Fatalf("expected one subscription run, got %d: %v", generated, err)
	}
	run, err := service.RunSubscription(ctx)
	if err != nil || run.DeliveryID <= 0 {
		t.Fatalf("expected successful subscription delivery: %v", err)
	}
	delivery := fmt.Sprintf("/report-deliveries/%d", run.DeliveryID)
	a.call("GET", delivery, a.readonly, nil, 200)

	// Disabling the approved execution account makes its source unavailable to
	// both content types. Emergency stopping must still commit.
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_accounts SET enabled=false WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	assertVersion(call(reportPath, stopper, "emergency_stop", 1, 200), 2)
	executeReport(1, 404)
	a.call("GET", exportPath+"/download", a.readonly, nil, 403)
	a.call("GET", delivery, a.readonly, nil, 404)
	call(reportPath, a.admin, "emergency_restore", 2, 409)
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_accounts SET enabled=true WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	assertVersion(call(reportPath, a.admin, "emergency_restore", 2, 200), 3)
	executeReport(1, 409)
	executeReport(3, 200)
	a.call("GET", exportPath+"/download", a.readonly, nil, 403)
	a.call("GET", delivery, a.readonly, nil, 409)

	// The dashboard's original approved card now follows a different report
	// release. A second stop succeeds, but fast recovery cannot reuse it.
	assertVersion(call(dashPath, stopper, "emergency_stop", 5, 200), 6)
	call(dashPath, business, "emergency_restore", 6, 409)
	a.call("GET", dashPublished, a.readonly, nil, 404)

	reportRequest = call(reportPath, a.developer, "request_offline", 3, 200)
	approve = request("approve_offline", 3)
	approve["approval_id"] = reportRequest["id"]
	approve["reason"] = "不同原因"
	a.call("POST", reportPath+"/availability", a.admin, approve, 409)
	approve["request_id"], approve["reason"] = testRequestID(), reason
	assertVersion(a.call("POST", reportPath+"/availability", a.admin, approve, 200), 4)
	executeReport(3, 404)
	call(reportPath, a.admin, "emergency_restore", 4, 409)
	revoke("report.offline")
	approve["request_id"], approve["expected_publication_no"] = testRequestID(), 4
	a.call("POST", reportPath+"/availability", a.admin, approve, 403)

	var normal, stops, restored int
	err = a.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE action='approve_offline' AND related_event_id IS NOT NULL),
		count(*) FILTER(WHERE action='emergency_stop'),count(*) FILTER(WHERE action='emergency_restore' AND related_event_id IS NOT NULL)
		FROM sys_content_availability_events WHERE dashboard_id=$1 OR report_id=$2`, dashboard, report).Scan(&normal, &stops, &restored)
	if err != nil || normal != 2 || stops != 3 || restored != 2 {
		t.Fatalf("availability ledger mismatch normal=%d stops=%d restored=%d err=%v", normal, stops, restored, err)
	}
}
