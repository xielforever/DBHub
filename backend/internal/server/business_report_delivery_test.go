package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func TestIsolatedBusinessReportPublicationAndDelivery(t *testing.T) {
	const isolation = "p2-formal-report-isolated"
	const syntheticValue = "sample-protected-note"
	webhook := make(chan map[string]any, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event map[string]any
		if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&event) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case webhook <- event:
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusConflict)
		}
	}))
	defer receiver.Close()
	webhookConfig := config.NotificationWebhookConfig{URL: receiver.URL, Secret: "isolated-report-fixture-signing-key"}
	a := setupConfiguredAPI(t, "", func(cfg *config.Config, _ string) {
		cfg.IAMIsolationID, cfg.Phase2ReportVerification = isolation, true
		cfg.NotificationWebhook = webhookConfig
	})
	f := setupManagedAPI(t, a)
	ctx := context.Background()
	view := a.table + "_governed"
	qualified := pgx.Identifier{"public", view}.Sanitize()
	if _, err := a.targetPool.Exec(ctx, "CREATE VIEW "+qualified+` AS
		SELECT * FROM (VALUES ('dept-a','visible','sample-protected-note'),
			('dept-b','outside-scope','outside-secret')) v(department,title,note)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := a.targetPool.Exec(ctx, "DROP VIEW "+qualified); err != nil {
			t.Error(err)
		}
	})
	if _, err := a.targetPool.Exec(ctx, "GRANT SELECT ON "+qualified+" TO "+pgx.Identifier{f.username}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	created := a.call("POST", "/managed-reports", a.developer, map[string]any{
		"request_id": testRequestID(), "name": "阶段 2 隔离报表", "connection_id": a.connectionFor(a.developer),
		"sql": `SELECT "title", "note" FROM ` + qualified, "chart_type": "table", "chart_config": map[string]any{},
		"export_policy": map[string]any{"enabled": true, "max_rows": 10},
	}, 200)
	report := int64(created["id"].(float64))
	review := f.submit(report, 1)
	f.approve(review, f.validate(review, []int64{1, 3}, nil))
	const dataset = "/workspaces/1/business-datasets/formal"
	now := time.Now().UTC()
	source := db.BusinessMappingSource{Authority: "隔离目录", SourceVersion: "v1", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}
	zero, one := int64(0), int64(1)
	a.call("PUT", dataset, a.admin, db.BusinessDatasetInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, AccountID: f.account, AccountVersion: 1,
	}, 200)
	deptA := db.OrganizationScopeEntry{DepartmentKey: "dept-a"}
	deptB := db.OrganizationScopeEntry{DepartmentKey: "dept-b"}
	for _, userID := range []int64{1, 3} {
		base := fmt.Sprintf("/workspaces/1/business-identities/%d", userID)
		a.call("PUT", base, a.admin, db.BusinessIdentityInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, BusinessKey: fmt.Sprintf("fixture-%d", userID),
			Datasets: []db.BusinessDatasetVersion{{Key: "formal", Version: 1}},
		}, 200)
		a.call("PUT", base+"/datasets/formal/organization-scope", a.admin, db.OrganizationScopeInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, IdentityVersion: 1, DatasetVersion: 1,
			Scopes: []db.OrganizationScopeEntry{deptA},
		}, 200)
	}
	a.call("PUT", dataset+"/row-column-policy", a.admin, db.BusinessRowColumnPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1,
		Definition: db.BusinessRowColumnDefinition{
			Schema: "public", View: view, Dimensions: db.OrganizationDimensionColumns{Department: "department"},
			AllowedScopes: []db.OrganizationScopeEntry{deptA, deptB}, AllowedColumns: []string{"title", "note"},
		},
	}, 200)
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolation_guard VALUES($1,current_database(),'正式报表隔离验收')`, isolation); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_business_reports(workspace_id,report_id,dataset_key,isolation_id)
		VALUES(1,$1,'formal',$2)`, report, isolation); err != nil {
		t.Fatal(err)
	}
	a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", report), a.admin,
		map[string]any{"request_id": testRequestID(), "expected_publication_no": 0, "review_id": review, "kind": "publish"}, 409)
	var events int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_report_releases WHERE report_id=$1`, report).Scan(&events); err != nil || events != 0 {
		t.Fatalf("unknown classification created a publication event: %d %v", events, err)
	}
	classification := db.BusinessOutputPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1, RowColumnPolicyVersion: 1,
		BusinessOutputDefinition: db.BusinessOutputDefinition{Fields: map[string]db.BusinessFieldClassification{
			"title": {Classification: "internal"},
		}},
	}
	a.call("PUT", dataset+"/output-policy", a.admin, classification, 400)
	classification.Fields["note"] = db.BusinessFieldClassification{Classification: "sensitive"}
	a.call("PUT", dataset+"/output-policy", a.admin, classification, 200)
	f.release(report, 0, review, "publish")
	expected := []any{[]any{"visible", nil}}
	if got := f.run(report, 1, 200)["rows"]; !reflect.DeepEqual(got, expected) {
		t.Fatalf("formal report did not apply current scope and masking: %#v", got)
	}

	path := fmt.Sprintf("/published-reports/%d/exports", report)
	export := a.call("POST", path, a.readonly, map[string]any{
		"request_id": testRequestID(), "publication_no": 1, "format": "csv", "reason": "隔离验收文件",
	}, 200)
	exportPath := fmt.Sprintf("/report-exports/%d", int64(export["id"].(float64)))
	preview := a.call("GET", exportPath+"/preview", a.admin, nil, 200)
	if got := preview["result"].(map[string]any)["rows"]; !reflect.DeepEqual(got, expected) {
		t.Fatalf("preview did not preserve masking: %#v", got)
	}
	a.call("POST", exportPath+"/review", a.readonly, map[string]any{"version": 1, "decision": "approved"}, 403)
	a.call("POST", exportPath+"/review", a.admin, map[string]any{"version": 1, "decision": "approved"}, 200)
	a.call("GET", exportPath+"/download", a.admin, nil, 403)
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
	if err != nil || resp.StatusCode != 200 || string(raw) != "\xef\xbb\xbftitle,note\r\nvisible,\r\n" {
		t.Fatalf("CSV did not contain exactly the masked authorized data: %d %v", resp.StatusCode, err)
	}

	subscription := a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(),
		"definition": map[string]any{"name": "隔离订阅", "report_id": report, "publication_no": 1, "interval_minutes": 5, "enabled": true, "webhook": true},
	}, 200)
	service := NewAutomation(&config.Config{NotificationWebhook: webhookConfig}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	if generated, err := service.Subscriptions.GenerateDue(ctx); err != nil || generated != 1 {
		t.Fatalf("subscription not queued: %d %v", generated, err)
	}
	run, err := service.RunSubscription(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deliveryPath := fmt.Sprintf("/report-deliveries/%d", run.DeliveryID)
	delivery := a.call("GET", deliveryPath, a.readonly, nil, 200)["snapshot"].(map[string]any)
	if got := delivery["result"].(map[string]any)["rows"]; !reflect.DeepEqual(got, expected) {
		t.Fatalf("subscription did not preserve scope and masking: %#v", got)
	}
	a.call("GET", deliveryPath, a.developer, nil, 404)
	inbox := a.call("GET", "/inbox", a.readonly, nil, 200)
	notice := inbox["items"].([]any)[0].(map[string]any)
	noticeRaw, _ := json.Marshal(notice)
	if notice["resource_type"] != "report_delivery" || notice["resource_id"] != float64(run.DeliveryID) ||
		strings.Contains(string(noticeRaw), syntheticValue) || strings.Contains(string(noticeRaw), "visible") {
		t.Fatal("in-app notification did not contain only a resource reference and generic text")
	}
	if err := service.RunWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-webhook:
		if len(event) != 5 || event["event_type"] != "report_ready" ||
			event["resource_path"] != fmt.Sprintf("/reports?view=subscriptions&delivery=%d", run.DeliveryID) {
			t.Fatal("unexpected webhook payload")
		}
	default:
		t.Fatal("local webhook was not delivered")
	}

	runPath := fmt.Sprintf("/report-subscriptions/%d/runs", int64(subscription["id"].(float64)))
	a.call("POST", runPath, a.readonly, map[string]any{"request_id": testRequestID(), "version": 1}, 200)
	queuedRun, err := service.RunSubscription(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.call("PUT", "/workspaces/1/business-identities/3/datasets/formal/organization-scope", a.admin, db.OrganizationScopeInput{
		BusinessMappingSource: source, ExpectedVersion: &one, IdentityVersion: 1, DatasetVersion: 1, Scopes: []db.OrganizationScopeEntry{},
	}, 200)
	f.run(report, 1, 403)
	a.call("GET", exportPath+"/download", a.readonly, nil, 403)
	a.call("GET", deliveryPath, a.readonly, nil, 403)
	if err = service.RunWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	var webhookStatus string
	if err = a.pool.QueryRow(ctx, `SELECT status FROM sys_webhook_deliveries WHERE run_id=$1 AND event_type='report_ready'`, queuedRun.ID).Scan(&webhookStatus); err != nil || webhookStatus != "failed" || len(webhook) != 0 {
		t.Fatalf("queued webhook was not stopped after organization scope was revoked: %s %v", webhookStatus, err)
	}
	a.call("POST", runPath, a.readonly, map[string]any{"request_id": testRequestID(), "version": 1}, 200)
	if _, err := service.RunSubscription(ctx); !errors.Is(err, db.ErrManagedDenied) {
		t.Fatalf("revoked organization scope did not deny the new subscription run: %v", err)
	}
}
