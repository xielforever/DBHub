package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/user/dbhub/internal/automation"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func freezeCustodyOwner(t *testing.T, a *apiTest, candidateID, reviewerID int64) (string, string) {
	t.Helper()
	ctx := context.Background()
	nomination, approval := testRequestID(), testRequestID()
	for _, d := range []struct {
		id               string
		issuer, delegate int64
	}{{nomination, reviewerID, 1}, {approval, 1, reviewerID}} {
		if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_delegations(workspace_id,delegation_id,issuer_id,delegate_id,
			governance_actions,grantable_actions,target_ids,source_ids,valid_until,proposal_id)
			VALUES(1,$1::uuid,$2,$3,ARRAY['member.freeze','assignment.propose','assignment.review'],
			ARRAY(SELECT action_id FROM sys_iam_capabilities WHERE scope='workspace' AND implementation_state='legacy' AND 'readonly'=ANY(legacy_roles)),
			ARRAY[2,$4]::bigint[],'{}',now()+interval '1 hour',$5::uuid)`,
			d.id, d.issuer, d.delegate, candidateID, testRequestID()); err != nil {
			t.Fatal(err)
		}
	}
	freeze := map[string]any{"request_id": testRequestID(), "kind": "member.freeze", "target_id": 2,
		"exit_mode": "planned", "reason": "隔离托管验收", "delegation_id": nomination, "delegation_version": 1}
	var configVersion, epoch int64
	if err := a.pool.QueryRow(ctx, `SELECT config_version,authorization_epoch FROM sys_iam_workspace_state WHERE workspace_id=1`).
		Scan(&configVersion, &epoch); err != nil {
		t.Fatal(err)
	}
	freeze["baseline"] = map[string]any{"config_version": configVersion, "authorization_epoch": epoch}
	a.call("POST", "/iam/restrictions", a.admin, freeze, 202)
	return nomination, approval
}

func verifyContentCustody(t *testing.T, a *apiTest, f *managedFixture, report, dashboard, pendingReport, pendingDashboard, candidateID, reviewerID int64, candidate, reviewer string) {
	ctx := context.Background()
	const workspace = "/workspaces/1"
	originalLogin := a.call("POST", "/auth/login", "", map[string]any{"username": "developer", "password": "TestPassword123!"}, 200)
	connection := a.connectionFor(a.developer)
	a.call("POST", "/report-subscriptions", a.developer, map[string]any{"request_id": testRequestID(),
		"definition": map[string]any{"name": "原成员订阅", "report_id": report, "publication_no": 1, "interval_minutes": 5, "enabled": true,
			"params": map[string]string{"category": "red"}},
	}, 200)
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	if count, err := service.Subscriptions.GenerateDue(ctx); err != nil || count != 1 {
		t.Fatalf("original subscription fixture did not enqueue: %d %v", count, err)
	}
	run, err := service.RunSubscription(ctx)
	if err != nil || run.DeliveryID <= 0 {
		t.Fatalf("original subscription did not produce a real snapshot: %v", err)
	}
	delivery := fmt.Sprintf("/report-deliveries/%d", run.DeliveryID)
	a.call("GET", delivery, a.developer, nil, 200)
	nomination, approval := freezeCustodyOwner(t, a, candidateID, reviewerID)
	a.call("GET", workspace+"/connections", a.developer, nil, 404)
	a.call("GET", fmt.Sprintf("/published-dashboards/%d", dashboard), a.readonly, nil, 200)
	refresh := a.call("POST", fmt.Sprintf("/managed-dashboards/%d/refreshes", dashboard), a.readonly, map[string]any{"mode": "published"}, 200)["refresh_id"].(string)
	a.call("POST", "/dashboard-refreshes/"+refresh+"/tiles/needed/execute", a.readonly, map[string]any{}, 200)
	f.run(report, 1, 200)
	verifyCustodyReportDelivery(t, a, f, report, dashboard, candidate, service)

	got := a.call("GET", "/iam/custody-items", a.admin, nil, 200)["items"].([]any)
	if len(got) != 3 {
		t.Fatalf("expected two reports and one dashboard, got %d", len(got))
	}
	items := map[string]int64{}
	for _, raw := range got {
		row := raw.(map[string]any)
		capture := row["captured"].(map[string]any)
		if row["state"] != "held" || capture["publication_no"] != float64(1) ||
			capture["active_review_id"] == nil || capture["sql_text"] != nil {
			t.Fatal("incorrect custody inventory")
		}
		if row["report_id"] == float64(report) {
			items["report"] = int64(row["id"].(float64))
			if reviews := capture["pending_reviews"].([]any); len(reviews) != 1 || reviews[0].(map[string]any)["review_id"] != float64(pendingReport) {
				t.Fatal("pending report review missing from inventory")
			}
		}
		if row["dashboard_id"] == float64(dashboard) {
			items["dashboard"] = int64(row["id"].(float64))
			if len(capture["report_dependencies"].([]any)) != 2 {
				t.Fatal("missing dashboard dependency metadata")
			}
		}
	}
	for _, path := range []string{
		fmt.Sprintf("/managed-reports/%d/transfer", report),
		fmt.Sprintf("/managed-dashboards/%d/transfer", dashboard),
	} {
		a.call("POST", path, a.admin, map[string]any{"request_id": testRequestID(), "version": 1, "owner_id": candidateID}, 409)
	}
	a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", report), a.admin, map[string]any{
		"request_id": testRequestID(), "kind": "publish", "review_id": pendingReport, "expected_publication_no": 1,
	}, 409)
	command := func(path, actor, action string, version int64, delegation string, want int) map[string]any {
		t.Helper()
		body := map[string]any{"request_id": testRequestID(), "action": action, "version": version, "reason": "隔离验收"}
		if action == "nominate" {
			body["candidate_id"] = candidateID
		}
		if delegation != "" {
			body["delegation_id"], body["delegation_version"] = delegation, 1
		}
		return a.call("POST", path, actor, body, want)
	}
	for _, asset := range []string{"report", "dashboard"} {
		path := fmt.Sprintf("/iam/custody-items/%d/handover", items[asset])
		command(path, a.admin, "nominate", 1, nomination, 200)
		command(path, reviewer, "approve", 2, approval, 409)
		command(path, a.readonly, "accept", 2, "", 404)
		if asset == "report" {
			command(path, candidate, "reject", 2, "", 200)
			command(path, a.admin, "nominate", 3, nomination, 200)
			command(path, candidate, "accept", 2, "", 409)
			command(path, candidate, "accept", 4, "", 200)
			command(path, a.admin, "approve", 5, nomination, 403)
			command(path, reviewer, "approve", 5, approval, 200)
		} else {
			command(path, candidate, "accept", 2, "", 200)
			command(path, a.admin, "approve", 3, nomination, 403)
			command(path, reviewer, "approve", 3, approval, 200)
		}
	}
	f.run(report, 1, 200)
	a.call("GET", fmt.Sprintf("/published-dashboards/%d", dashboard), a.readonly, nil, 200)
	a.call("GET", workspace+"/connections", a.developer, nil, 404)
	var ownerReport, ownerDashboard, pubReport, pubDashboard int64
	var reviewReport, reviewDashboard string
	err = a.pool.QueryRow(ctx, `SELECT r.owner_id,d.owner_id,r.publication_no,d.publication_no,rr.status,dr.status
		FROM sys_managed_reports r,sys_managed_dashboards d,sys_report_reviews rr,sys_dashboard_reviews dr
		WHERE r.id=$1 AND d.id=$2 AND rr.id=$3 AND dr.id=$4`, report, dashboard, pendingReport, pendingDashboard).
		Scan(&ownerReport, &ownerDashboard, &pubReport, &pubDashboard, &reviewReport, &reviewDashboard)
	if err != nil || ownerReport != candidateID || ownerDashboard != candidateID ||
		pubReport != 1 || pubDashboard != 1 || reviewReport != "withdrawn" || reviewDashboard != "withdrawn" {
		t.Fatalf("handover did not preserve release and withdraw only pending review: %v", err)
	}
	setCustodyViewerScope(a, "dash-a", 3, "dept-a", "dept-b")
	if data := f.run(report, 1, 409); len(data) != 0 {
		t.Fatal("handover released rows outside the preserved approval")
	}
	setCustodyViewerScope(a, "dash-a", 4, "dept-a")
	rejoin := func(token, action, delegation, proposal string, want int) map[string]any {
		t.Helper()
		in := map[string]any{"request_id": testRequestID(), "action": action, "target_id": 2, "reason": "同一平台账户重新加入验收",
			"delegation_id": delegation, "delegation_version": 1}
		if proposal != "" {
			in["proposal_id"] = proposal
		}
		return a.call("POST", "/iam/custody-rejoins", token, in, want)
	}
	proposal := rejoin(a.admin, "propose", nomination, "", 200)["id"].(string)
	rejoin(a.admin, "approve", nomination, proposal, 403)
	rejoined := rejoin(reviewer, "approve", approval, proposal, 200)
	if rejoined["member_id"] == float64(2) || rejoined["role"] != "readonly" {
		t.Fatal("rejoin reused the old member identity or role")
	}
	a.call("GET", workspace+"/connections", a.developer, nil, 404)
	refreshed := a.call("POST", "/auth/refresh", "", map[string]any{"refresh_token": originalLogin["refresh_token"]}, 200)
	a.call("GET", workspace+"/connections", refreshed["access_token"].(string), nil, 404)
	fresh := a.call("POST", "/auth/login", "", map[string]any{"username": "developer", "password": "TestPassword123!"}, 200)["access_token"].(string)
	a.call("GET", workspace+"/connections", fresh, nil, 200)
	a.call("GET", "/workspaces/999999/connections", fresh, nil, 404)
	a.call("GET", fmt.Sprintf("%s/connections/%d", workspace, connection), fresh, nil, 404)
	a.call("GET", workspace+delivery, fresh, nil, 404)
	a.call("GET", fmt.Sprintf("%s/published-reports/%d", workspace, report), fresh, nil, 404)
	if _, err = a.pool.Exec(ctx, `UPDATE sys_report_subscriptions SET next_run_at=now()-interval '1 minute'
		WHERE workspace_id=1 AND user_id=2`); err != nil {
		t.Fatal(err)
	}
	if count, err := service.Subscriptions.GenerateDue(ctx); err != nil || count != 0 {
		t.Fatalf("old personal subscriptions resumed after rejoin: %d %v", count, err)
	}
	var freezes, inherited int
	err = a.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM sys_iam_revocations WHERE workspace_id=1 AND target_id=2 AND kind='member_freeze'),
		(SELECT count(*) FROM sys_workspace_members WHERE workspace_id=1 AND user_id=$1 AND cardinality(capabilities)>0)`,
		int64(rejoined["member_id"].(float64))).Scan(&freezes, &inherited)
	if err != nil || freezes != 1 || inherited != 0 {
		t.Fatalf("rejoin changed historical freeze or copied capabilities: %v", err)
	}
}

func setCustodyViewerScope(a *apiTest, dataset string, expected int64, departments ...string) {
	a.t.Helper()
	now := time.Now().UTC()
	scopes := make([]db.OrganizationScopeEntry, 0, len(departments))
	for _, department := range departments {
		scopes = append(scopes, db.OrganizationScopeEntry{DepartmentKey: department})
	}
	a.call("PUT", "/workspaces/1/business-identities/3/datasets/"+dataset+"/organization-scope", a.admin,
		db.OrganizationScopeInput{BusinessMappingSource: db.BusinessMappingSource{Authority: "隔离验收",
			SourceVersion: fmt.Sprintf("custody-%d", expected+1), ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)},
			ExpectedVersion: &expected, IdentityVersion: 1, DatasetVersion: 1, Scopes: scopes}, 200)
}

func verifyCustodyReportDelivery(t *testing.T, a *apiTest, f *managedFixture, report, dashboard int64, reviewer string, service *automation.Service) {
	t.Helper()
	ctx := context.Background()
	params := map[string]string{"category": "red"}
	refreshPath := fmt.Sprintf("/managed-dashboards/%d/refreshes", dashboard)
	refresh := a.call("POST", refreshPath, a.readonly, map[string]any{"mode": "published"}, 200)["refresh_id"].(string)
	exports := fmt.Sprintf("/published-reports/%d/exports", report)
	exportInput := map[string]any{"request_id": testRequestID(), "publication_no": 1, "format": "csv", "reason": "托管交付验收", "params": params}
	export := a.call("POST", exports, a.readonly, exportInput, 200)
	exportPath := fmt.Sprintf("/report-exports/%d", int64(export["id"].(float64)))
	a.call("POST", exportPath+"/review", reviewer, map[string]any{"version": 1, "decision": "approved"}, 200)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.server.URL+"/api/v1/workspaces/1"+exportPath+"/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.readonly)
	resp, err := a.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 || !strings.Contains(string(raw), "\r\n11,red,") || strings.Contains(string(raw), "\r\n99,red,") {
		t.Fatalf("CSV did not contain the original approved rows: %d %v", resp.StatusCode, err)
	}

	subscription := a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(),
		"definition": map[string]any{"name": "托管交付验收", "report_id": report, "publication_no": 1, "interval_minutes": 5, "enabled": true, "params": params},
	}, 200)
	subscriptionID := int64(subscription["id"].(float64))
	if count, err := service.Subscriptions.GenerateDue(ctx); err != nil || count != 1 {
		t.Fatalf("expected one queued viewer run: %d %v", count, err)
	}
	run, err := service.RunSubscription(ctx)
	if err != nil || run.DeliveryID <= 0 {
		t.Fatalf("valid custody run did not deliver: %v", err)
	}
	delivery := fmt.Sprintf("/report-deliveries/%d", run.DeliveryID)
	payload := a.call("GET", delivery, a.readonly, nil, 200)["snapshot"].(map[string]any)
	rows := payload["result"].(map[string]any)["rows"].([]any)
	if len(rows) != 1 || rows[0].([]any)[0] != float64(11) {
		t.Fatal("subscription did not deliver only the approved row")
	}
	a.call("POST", fmt.Sprintf("/report-subscriptions/%d/runs", subscriptionID), a.readonly,
		map[string]any{"request_id": testRequestID(), "version": 1}, 200)

	setCustodyViewerScope(a, "dash-a", 1, "dept-a", "dept-b")
	exportInput["request_id"] = testRequestID()
	newRefresh := a.call("POST", refreshPath, a.readonly, map[string]any{"mode": "published"}, 200)["refresh_id"].(string)
	if len(f.run(report, 1, 409)) != 0 ||
		len(a.call("POST", "/dashboard-refreshes/"+refresh+"/tiles/needed/execute", a.readonly, map[string]any{}, 409)) != 0 ||
		len(a.call("POST", "/dashboard-refreshes/"+newRefresh+"/tiles/needed/execute", a.readonly, map[string]any{}, 409)) != 0 ||
		len(a.call("POST", exports, a.readonly, exportInput, 409)) != 0 ||
		len(a.call("GET", exportPath+"/download", a.readonly, nil, 409)) != 0 ||
		len(a.call("GET", delivery, a.readonly, nil, 409)) != 0 {
		t.Fatal("custody boundary conflict returned result data")
	}
	failedRun, err := service.RunSubscription(ctx)
	if !errors.Is(err, db.ErrManagedConflict) || failedRun == nil {
		t.Fatalf("queued run did not stop with the custody conflict: %v", err)
	}
	var status string
	var enabled, empty bool
	var unavailableDelivery int64
	err = a.pool.QueryRow(ctx, `SELECT r.status,s.enabled,r.encrypted_snapshot='',r.delivery_id
		FROM sys_report_subscription_runs r JOIN sys_report_subscriptions s ON s.id=r.subscription_id WHERE r.id=$1`,
		failedRun.ID).Scan(&status, &enabled, &empty, &unavailableDelivery)
	if err != nil || status != "failed" || enabled || !empty {
		t.Fatalf("blocked run stored a delivery or left the subscription active: %v", err)
	}
	a.call("GET", fmt.Sprintf("/report-deliveries/%d", unavailableDelivery), a.readonly, nil, 404)
	t.Log("custody boundary rejected report, old and fresh cards, export generation and download, existing delivery and queued run")
	setCustodyViewerScope(a, "dash-a", 2, "dept-a")
	f.run(report, 1, 200)
}

func verifyDashboardCustodyBoundary(t *testing.T, a *apiTest, f *managedFixture, dashboard, report int64, previousRefresh string) {
	t.Helper()
	var reportHeld, dashboardHeld bool
	err := a.pool.QueryRow(context.Background(), `SELECT
		EXISTS(SELECT 1 FROM sys_content_custody_items WHERE report_id=$1),
		EXISTS(SELECT 1 FROM sys_content_custody_items WHERE dashboard_id=$2 AND state='held')`, report, dashboard).
		Scan(&reportHeld, &dashboardHeld)
	if err != nil || reportHeld || !dashboardHeld {
		t.Fatalf("expected the dashboard in custody and its referenced report under another owner: %v", err)
	}
	setCustodyViewerScope(a, "dash-b", 1, "dept-a", "dept-b")
	if rows := f.run(report, 1, 200)["rows"].([]any); len(rows) != 2 {
		t.Fatal("independently owned report did not exercise the changed scope")
	}
	fresh := a.call("POST", fmt.Sprintf("/managed-dashboards/%d/refreshes", dashboard), a.readonly,
		map[string]any{"mode": "published"}, 200)["refresh_id"].(string)
	for _, refresh := range []string{previousRefresh, fresh} {
		if data := a.call("POST", "/dashboard-refreshes/"+refresh+"/tiles/extra/execute", a.readonly, map[string]any{}, 409); len(data) != 0 {
			t.Fatal("optional card returned data outside the dashboard's own approval")
		}
	}
	a.call("POST", "/dashboard-refreshes/"+fresh+"/tiles/needed/execute", a.readonly, map[string]any{}, 200)
	t.Log("dashboard approval blocked expanded optional card independently from its report")
}
