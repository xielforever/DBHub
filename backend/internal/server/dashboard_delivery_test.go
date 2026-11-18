package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func verifyDashboardDelivery(t *testing.T, a *apiTest, dashboard, otherID, reviewerID, partialID int64, reviewer, partial, other string) {
	t.Helper()
	ctx := context.Background()
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	createPath := fmt.Sprintf("/published-dashboards/%d/delivery-plans", dashboard)
	planPath := func(id int64) string { return fmt.Sprintf("/dashboard-delivery-plans/%d", id) }
	deliveryPath := func(id int64) string { return fmt.Sprintf("/dashboard-deliveries/%d", id) }
	create := func(kind, token string, extra map[string]any, want int) int64 {
		t.Helper()
		body := map[string]any{"request_id": testRequestID(), "kind": kind, "publication_no": 1, "params": map[string]string{"category": "red"}}
		for k, v := range extra {
			body[k] = v
		}
		r := a.call("POST", createPath, token, body, want)
		if want != 200 {
			return 0
		}
		return int64(r["id"].(float64))
	}
	approve := func(plan int64) {
		t.Helper()
		path := planPath(plan) + "/review"
		out := a.call("GET", path, reviewer, nil, 200)
		if out["snapshot"] != nil || out["rows"] != nil {
			t.Fatal("review returned deliverable values")
		}
		body := map[string]any{"request_id": testRequestID(), "version": out["version"], "basis_sha256": strings.Repeat("0", 64), "decision": "approved"}
		a.call("POST", path, reviewer, body, 409)
		body["request_id"], body["basis_sha256"] = testRequestID(), out["basis_sha256"]
		a.call("POST", path, reviewer, body, 200)
	}
	states := func(plan int64, token string) map[int64]map[string]any {
		t.Helper()
		out := map[int64]map[string]any{}
		for _, raw := range a.call("GET", planPath(plan)+"/runs", token, nil, 200)["items"].([]any) {
			item := raw.(map[string]any)
			uid := int64(item["user_id"].(float64))
			if item["snapshot"] != nil || item["payload"] != nil {
				t.Fatal("owner read delivered data")
			}
			if out[uid] == nil {
				out[uid] = item
			}
		}
		return out
	}
	drain := func(n int) {
		t.Helper()
		for range n {
			if job, err := service.RunDashboardDelivery(ctx); err != nil || job == nil {
				t.Fatalf("expected work to finish, job=%v error=%v", job, err)
			}
		}
	}
	export := map[string]any{"format": "csv", "reason": "隔离验收"}
	create("export", a.readonly, export, 404)
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_dashboard_delivery VALUES(1,$1)`, dashboard); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		user     int64
		action   string
		datasets []string
	}{
		{reviewerID, "dashboard.exportReview", []string{"dash-a", "dash-b"}},
		{reviewerID, "dashboard.subscribeReview", []string{"dash-a", "dash-b"}},
		{partialID, "dashboard.exportReview", []string{"dash-a"}},
		{partialID, "dashboard.subscribeReview", []string{"dash-a"}},
		{1, "dashboard.authorize", []string{"dash-a", "dash-b"}},
		{2, "dashboard.subscribeTeam", []string{"dash-a", "dash-b"}},
	} {
		for _, dataset := range row.datasets {
			if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_dashboard_delivery_grants VALUES(1,$1,$2,$3,statement_timestamp()+interval '1 hour')`, dataset, row.user, row.action); err != nil {
				t.Fatal(err)
			}
		}
	}
	filePlan := create("export", a.readonly, export, 200)
	a.call("GET", planPath(filePlan)+"/review", a.readonly, nil, 403)
	a.call("GET", planPath(filePlan)+"/review", partial, nil, 404)
	if len(states(filePlan, a.readonly)) != 0 {
		t.Fatal("export created output before approval")
	}
	approve(filePlan)
	drain(1)
	fileID := int64(states(filePlan, a.readonly)[3]["id"].(float64))
	if a.call("GET", deliveryPath(fileID), a.readonly, nil, 200)["format"] != "zip" {
		t.Fatal("whole dashboard export failed")
	}
	a.call("POST", deliveryPath(fileID)+"/credential", reviewer, nil, 404)
	verifyDeliveryNotice(t, a, 3, "dashboard_delivery", a.readonly, a.developer)
	file := redeemDashboardFixture(t, a, fileID, a.readonly, "zip")
	archive, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil || len(archive.File) != 3 {
		t.Fatalf("whole dashboard contains incomplete card export: %v", err)
	}
	for _, entry := range archive.File {
		if entry.Name == "02.csv" {
			r, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			_ = r.Close()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "synthetic") || !strings.Contains(string(data), "11,red,,") {
				t.Fatal("metric card lost masking in archive")
			}
		}
	}
	var lifetimeOK bool
	if err := a.pool.QueryRow(ctx, `SELECT expires_at>statement_timestamp()+interval '23 hours'
		AND expires_at<=statement_timestamp()+interval '24 hours' FROM sys_dashboard_deliveries WHERE id=$1`, fileID).Scan(&lifetimeOK); err != nil || !lifetimeOK {
		t.Fatalf("dashboard file did not use a 24-hour lifetime: %v", err)
	}
	credential := a.call("POST", deliveryPath(fileID)+"/credential", a.readonly, nil, 200)["credential"]
	if _, err = a.pool.Exec(ctx, `UPDATE sys_dashboard_deliveries SET expires_at=statement_timestamp()-interval '1 second' WHERE id=$1`, fileID); err != nil {
		t.Fatal(err)
	}
	a.call("POST", deliveryPath(fileID)+"/redeem", a.readonly, map[string]any{"credential": credential}, 404)
	if err = service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var purgedFile bool
	if err = a.pool.QueryRow(ctx, `SELECT payload='' AND sha256<>'' AND status='succeeded' FROM sys_dashboard_deliveries WHERE id=$1`, fileID).Scan(&purgedFile); err != nil || !purgedFile {
		t.Fatalf("expired dashboard file cleanup did not preserve the receipt: %v", err)
	}
	singlePlan := create("export", a.readonly, map[string]any{"format": "json", "reason": "单卡隔离验收", "tile_key": "extra"}, 200)
	approve(singlePlan)
	drain(1)
	singleID := int64(states(singlePlan, a.readonly)[3]["id"].(float64))
	if raw := redeemDashboardFixture(t, a, singleID, a.readonly, "json"); !bytes.Contains(raw, []byte(`"rows":[[11,"red",null`)) {
		t.Fatal("single card file did not contain its original masked rows")
	}
	xlsxPlan := create("export", a.readonly, map[string]any{"format": "xlsx", "reason": "单卡工作簿", "tile_key": "extra"}, 200)
	approve(xlsxPlan)
	drain(1)
	xlsxID := int64(states(xlsxPlan, a.readonly)[3]["id"].(float64))
	workbook := redeemDashboardFixture(t, a, xlsxID, a.readonly, "xlsx")
	xlsx, err := zip.NewReader(bytes.NewReader(workbook), int64(len(workbook)))
	if err != nil || len(xlsx.File) < 5 {
		t.Fatalf("single card workbook was invalid: %v", err)
	}
	team := create("team", a.developer, map[string]any{"interval_minutes": 5, "user_ids": []int64{3, otherID}}, 200)
	a.call("GET", planPath(team)+"/review", partial, nil, 404)
	approve(team)
	if err := a.pool.QueryRow(ctx, `SELECT review_until=decided_at+interval '30 days'
		FROM sys_dashboard_delivery_plans WHERE id=$1`, team).Scan(&lifetimeOK); err != nil || !lifetimeOK {
		t.Fatalf("team approval lifetime was not 30 days: %v", err)
	}
	grant := func(token, action string, uid, version int64, status int) {
		t.Helper()
		a.call("POST", planPath(team)+"/recipient-access", token, map[string]any{"request_id": testRequestID(), "recipient_id": uid, "version": version, "action": action, "reason": "隔离逐人交付"}, status)
	}
	grant(a.developer, "grant", 3, 0, 403)
	grant(a.admin, "grant", 3, 0, 200)
	grant(a.admin, "grant", otherID, 0, 200)
	due := func(id int64, offset int) {
		t.Helper()
		if _, err := a.pool.Exec(ctx, `UPDATE sys_dashboard_delivery_plans SET decided_at=date_trunc('minute',statement_timestamp())-
			make_interval(mins=>28,secs=>$2), checked_slot=NULL WHERE id=$1`, id, offset); err != nil {
			t.Fatal(err)
		}
		if err := service.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	due(team, 0)
	if err = service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_dashboard_deliveries WHERE plan_id=$1`, team).Scan(&count); err != nil || count != 2 {
		t.Fatalf("automatic slot duplicated or was missed: %d %v", count, err)
	}
	drain(2)
	teamStates := states(team, a.developer)
	aID, bID := int64(teamStates[3]["id"].(float64)), int64(teamStates[otherID]["id"].(float64))
	claim := func(id int64, token string, requiredRows int, optional string) {
		t.Helper()
		value := a.call("GET", deliveryPath(id), token, nil, 200)
		cards := value["snapshot"].(map[string]any)["cards"].([]any)
		first := cards[0].(map[string]any)["snapshot"].(map[string]any)["result"].(map[string]any)["rows"].([]any)
		second := cards[1].(map[string]any)
		if len(first) != requiredRows || second["status"] != optional || (second["snapshot"] != nil) != (optional == "ready") {
			t.Fatal("recipient did not get its own required/optional scope")
		}
	}
	claim(aID, a.readonly, 2, "ready")
	claim(bID, other, 1, "restricted")
	a.call("GET", deliveryPath(aID), other, nil, 404)
	a.call("GET", deliveryPath(aID), a.developer, nil, 404)
	due(team, 17)
	grant(a.admin, "revoke", 3, 1, 200)
	for range 2 {
		job, e := service.RunDashboardDelivery(ctx)
		if job == nil || (job.UserID == 3) == (e == nil) {
			t.Fatalf("queued revocation affected the wrong recipient: %v", e)
		}
	}
	a.call("GET", deliveryPath(aID), a.readonly, nil, 403)
	grant(a.admin, "grant", 3, 2, 200)
	a.call("GET", deliveryPath(aID), a.readonly, nil, 403)
	claim(bID, other, 1, "restricted")
	setCustodyViewerScope(a, "dash-a", 2, "dept-a")
	due(team, 34)
	drain(2)
	teamStates = states(team, a.developer)
	narrowedID := int64(teamStates[3]["id"].(float64))
	claim(narrowedID, a.readonly, 1, "ready")
	version := int64(1)
	now := time.Now().UTC()
	a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d/datasets/dash-a/organization-scope", otherID), a.admin,
		db.OrganizationScopeInput{BusinessMappingSource: db.BusinessMappingSource{Authority: "隔离验收", SourceVersion: "v2", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)},
			ExpectedVersion: &version, IdentityVersion: 1, DatasetVersion: 1,
			Scopes: []db.OrganizationScopeEntry{{DepartmentKey: "dept-a"}, {DepartmentKey: "dept-b"}}}, 200)
	due(team, 51)
	for range 2 {
		job, err := service.RunDashboardDelivery(ctx)
		if job == nil || (job.UserID == otherID) == (err == nil) {
			t.Fatalf("scope expansion affected the wrong recipient: %v", err)
		}
	}
	grant(a.admin, "grant", otherID, 2, 409)
	due(team, 68)
	lock, err := a.targetPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	table := pgx.Identifier{"public", a.table}.Sanitize()
	if _, err = lock.Exec(ctx, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	workerResult := make(chan error, 1)
	queryContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	go func() {
		_, err := service.RunDashboardDelivery(queryContext)
		workerResult <- err
	}()
	for {
		var opened bool
		err := a.targetPool.QueryRow(queryContext, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
			AND wait_event_type='Lock' AND pid<>pg_backend_pid() AND query LIKE '%'||$1||'%')`, a.table+"_card_0").Scan(&opened)
		if err != nil {
			t.Fatalf("required card did not reach its real query: %v", err)
		}
		if opened {
			break
		}
		select {
		case err := <-workerResult:
			t.Fatalf("worker stopped before withdrawal in flight: %v", err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	grant(a.admin, "revoke", 3, 3, 200)
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-workerResult; err == nil {
		t.Fatal("in-flight withdrawal still produced a delivery")
	}
	inFlight := states(team, a.developer)[3]
	if inFlight["status"] != "skipped" || int64(inFlight["id"].(float64)) == narrowedID {
		t.Fatal("in-flight withdrawal did not fence the actual recipient's new result")
	}
	a.call("GET", deliveryPath(narrowedID), a.readonly, nil, 403)
	t.Log("target query was open during recipient withdrawal; no new delivery and no old claim")
	personalInput := map[string]any{"interval_minutes": 5, "tile_key": "extra", "alert_tile": "extra",
		"alert_rule": map[string]any{"column": "quantity", "operator": "gt", "threshold": "10"}}
	create("personal", a.readonly, personalInput, 400)
	personalInput["confirmed"] = true
	personal := create("personal", a.readonly, personalInput, 200)
	if err := a.pool.QueryRow(ctx, `SELECT review_until=decided_at+interval '90 days'
		FROM sys_dashboard_delivery_plans WHERE id=$1`, personal).Scan(&lifetimeOK); err != nil || !lifetimeOK {
		t.Fatalf("personal confirmation lifetime was not 90 days: %v", err)
	}
	due(personal, 0)
	drain(1)
	firstMetricDelivery := int64(states(personal, a.readonly)[3]["id"].(float64))
	alerts := planPath(personal) + "/alerts"
	events := a.call("GET", alerts, a.readonly, nil, 200)["items"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["observed_value"] != "11" {
		t.Fatal("original dashboard metric was not recorded")
	}
	alertID := int64(events[0].(map[string]any)["id"].(float64))
	a.call("POST", fmt.Sprintf("%s/%d/disposition", alerts, alertID), a.readonly, map[string]any{"request_id": testRequestID(), "note": "核对原期次"}, 200)
	if _, err = a.targetPool.Exec(ctx, "UPDATE "+table+" SET id=0 WHERE id=1 AND owner_id=1"); err != nil {
		t.Fatal(err)
	}
	due(personal, 17)
	drain(1)
	prior := a.call("GET", deliveryPath(firstMetricDelivery), a.readonly, nil, 200)
	oldCard := prior["snapshot"].(map[string]any)["cards"].([]any)[0].(map[string]any)
	oldRow := oldCard["snapshot"].(map[string]any)["result"].(map[string]any)["rows"].([]any)[0].([]any)
	if oldRow[0] != float64(11) {
		t.Fatal("fixed historical delivery was recomputed")
	}
	if _, err = a.pool.Exec(ctx, `UPDATE sys_dashboard_deliveries SET expires_at=statement_timestamp()-interval '1 second' WHERE id=$1`, firstMetricDelivery); err != nil {
		t.Fatal(err)
	}
	a.call("GET", deliveryPath(firstMetricDelivery), a.readonly, nil, 404)
	if err = service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var purged bool
	if err = a.pool.QueryRow(ctx, `SELECT payload='' AND evidence<>'' AND payload_purged_at IS NOT NULL FROM sys_dashboard_deliveries WHERE id=$1`, firstMetricDelivery).Scan(&purged); err != nil || !purged {
		t.Fatalf("snapshot cleanup did not preserve evidence: %v", err)
	}
	events = a.call("GET", alerts, a.readonly, nil, 200)["items"].([]any)
	if len(events) != 2 || events[0].(map[string]any)["observed_value"] != "10" || events[1].(map[string]any)["observed_value"] != "11" || events[1].(map[string]any)["disposition"] != "核对原期次" {
		t.Fatal("original and recovery metric observations changed after cleanup")
	}
	if _, err = a.pool.Exec(ctx, `UPDATE sys_dashboard_delivery_plans SET review_until=statement_timestamp()-interval '1 second',
		checked_slot=NULL WHERE id=ANY($1::bigint[])`, []int64{team, personal}); err != nil {
		t.Fatal(err)
	}
	if due, err := service.Conns.ManagedDashboards().DueDashboardDeliveries(ctx); err != nil || len(due) != 0 {
		t.Fatalf("expired personal or team approvals still scheduled: %v %v", due, err)
	}
	t.Log("approved individual files, per-recipient scope and revocation, automatic slots, expiry and metric history verified")
}

func redeemDashboardFixture(t *testing.T, a *apiTest, id int64, token, format string) []byte {
	t.Helper()
	path := fmt.Sprintf("/dashboard-deliveries/%d", id)
	issued := a.call("POST", path+"/credential", token, nil, 200)
	credential := issued["credential"].(string)
	until, err := time.Parse(time.RFC3339Nano, issued["expires_at"].(string))
	if err != nil || time.Until(until) <= 0 || time.Until(until) > 10*time.Minute {
		t.Fatalf("dashboard credential lifetime was out of bounds: %v", err)
	}
	anotherSession := a.call("POST", "/auth/login", "", map[string]any{"username": "readonly", "password": "TestPassword123!"}, 200)["access_token"].(string)
	a.call("POST", path+"/redeem", anotherSession, map[string]string{"credential": credential}, 403)
	input, _ := json.Marshal(map[string]string{"credential": credential})
	request, err := http.NewRequest(http.MethodPost, a.server.URL+"/api/v1"+path+"/redeem", bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := a.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	hash := sha256.Sum256(raw)
	if err != nil || response.StatusCode != 200 || response.Header.Get("X-Report-File-SHA256") != hex.EncodeToString(hash[:]) || !strings.Contains(response.Header.Get("Content-Disposition"), "."+format) {
		t.Fatalf("dashboard redemption failed: status=%d %v %s", response.StatusCode, err, raw)
	}
	a.call("POST", path+"/redeem", token, map[string]string{"credential": credential}, 403)
	return raw
}
