package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func verifyTeamRecipientDelivery(t *testing.T, a *apiTest, report, reviewerID, second int64, reviewer, secondToken string, retention bool) {
	t.Helper()
	ctx := context.Background()
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	for _, item := range []struct {
		query string
		arg   int64
	}{
		{`INSERT INTO sys_iam_isolated_subscription_reviews VALUES(1,$1)`, report},
		{`INSERT INTO sys_iam_isolated_subscription_grants(workspace_id,dataset_key,user_id,action,valid_until)
			VALUES(1,'dash-a',$1,'create',now()+interval '1 hour')`, 2},
		{`INSERT INTO sys_iam_isolated_subscription_grants(workspace_id,dataset_key,user_id,action,valid_until)
			VALUES(1,'dash-a',$1,'review',now()+interval '1 hour')`, reviewerID},
	} {
		if _, err := a.pool.Exec(ctx, item.query, item.arg); err != nil {
			t.Fatal(err)
		}
	}
	const team = "/report-team-subscriptions"
	plan := int64(a.call("POST", team, a.developer, map[string]any{"request_id": testRequestID(), "user_ids": []int64{3, second},
		"definition": map[string]any{"name": "本地逐人交付", "report_id": report, "publication_no": 1, "interval_minutes": 5,
			"params": map[string]string{"category": "red"}}}, 200)["id"].(float64))
	path := fmt.Sprintf("%s/%d", team, plan)
	basis := a.call("GET", path+"/review", reviewer, nil, 200)["basis_sha256"]
	a.call("POST", path+"/review", reviewer, map[string]any{"request_id": testRequestID(), "version": 1,
		"basis_sha256": basis, "decision": "approved"}, 200)
	access := func(token, action string, recipient, version int64, want int) {
		t.Helper()
		a.call("POST", path+"/recipient-access", token, map[string]any{"request_id": testRequestID(), "version": version,
			"recipient_id": recipient, "action": action, "reason": "逐人授权验收"}, want)
	}
	openPeriod := func(want int) int64 {
		t.Helper()
		out := a.call("POST", path+"/periods", a.developer, map[string]any{"request_id": testRequestID(), "version": 2}, want)
		if want != 200 {
			return 0
		}
		return int64(out["id"].(float64))
	}
	statuses := func(period int64) map[int64]map[string]any {
		t.Helper()
		out := a.call("GET", fmt.Sprintf("%s/periods/%d", path, period), a.developer, nil, 200)
		rows := map[int64]map[string]any{}
		for _, raw := range out["items"].([]any) {
			item := raw.(map[string]any)
			if len(item) != 5 || item["snapshot"] != nil {
				t.Fatal("owner status included result content")
			}
			rows[int64(item["user_id"].(float64))] = item
		}
		return rows
	}
	drain := func(wantDone, wantSkipped int) {
		t.Helper()
		done, skipped := 0, 0
		for range wantDone + wantSkipped {
			job, err := service.RunTeamDelivery(ctx)
			if job == nil {
				t.Fatalf("recipient queue did not return a job: %v", err)
			}
			if err == nil {
				done++
			} else if errors.Is(err, db.ErrTeamRecipientReview) {
				skipped++
			} else {
				t.Fatal(err)
			}
		}
		if done != wantDone || skipped != wantSkipped {
			t.Fatalf("unexpected counts: done=%d paused=%d", done, skipped)
		}
	}
	claim := func(id any, token string, wantStatus, wantRows int) {
		t.Helper()
		raw := a.call("GET", fmt.Sprintf("/report-team-deliveries/%s", id), token, nil, wantStatus)
		if wantStatus == 200 {
			rows := raw["snapshot"].(map[string]any)["result"].(map[string]any)["rows"].([]any)
			if len(rows) != wantRows {
				t.Fatalf("team snapshot row count=%d want %d", len(rows), wantRows)
			}
			for _, row := range rows {
				if row.([]any)[2] != nil {
					t.Fatal("team snapshot did not retain masking")
				}
			}
		}
	}
	absent := statuses(openPeriod(200))
	if absent[3]["status"] != "skipped" || absent[second]["status"] != "skipped" {
		t.Fatal("team approval alone granted recipient access")
	}
	access(a.admin, "grant", 3, 0, 403)
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_delivery_authorizers VALUES(1,'dash-a',1,now()+interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	access(a.developer, "grant", 3, 0, 403)
	access(a.admin, "grant", reviewerID, 0, 403)
	access(a.admin, "grant", 3, 0, 200)
	access(a.admin, "grant", second, 0, 200)
	if retention {
		verifyAutomaticTeamRetention(t, a, service, plan)
	}
	first := openPeriod(200)
	drain(2, 0)
	results := statuses(first)
	firstID := results[3]["id"]
	claim(firstID, a.readonly, 200, 2)
	claim(results[second]["id"], secondToken, 200, 1)
	claim(firstID, a.developer, 404, 0)
	claim(firstID, secondToken, 404, 0)
	a.call("GET", path, a.readonly, nil, 404)

	setCustodyViewerScope(a, "dash-a", 2, "dept-a")
	claim(firstID, a.readonly, 409, 0)
	narrow := openPeriod(200)
	drain(2, 0)
	narrowID := statuses(narrow)[3]["id"]
	claim(narrowID, a.readonly, 200, 1)
	pending := openPeriod(200)
	access(a.admin, "revoke", 3, 1, 200)
	for range 2 {
		job, err := service.RunTeamDelivery(ctx)
		if job == nil || (job.UserID == 3 && !errors.Is(err, db.ErrNotFound) && !errors.Is(err, db.ErrManagedDenied)) ||
			(job.UserID == second && err != nil) {
			t.Fatalf("queued receipt did not respect the recipient revocation: %v", err)
		}
	}
	afterRevoke := statuses(pending)
	if afterRevoke[3]["status"] != "skipped" || afterRevoke[second]["status"] != "succeeded" {
		t.Fatal("queued revocation affected an unrelated recipient")
	}
	claim(narrowID, a.readonly, 404, 0)
	access(a.admin, "grant", 3, 2, 200)
	claim(narrowID, a.readonly, 403, 0)

	now := time.Now().UTC()
	one := int64(1)
	a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d/datasets/dash-a/organization-scope", second), a.admin,
		db.OrganizationScopeInput{BusinessMappingSource: db.BusinessMappingSource{Authority: "逐人交付验收", SourceVersion: "v2",
			ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}, ExpectedVersion: &one,
			IdentityVersion: 1, DatasetVersion: 1, Scopes: []db.OrganizationScopeEntry{{DepartmentKey: "dept-a"}, {DepartmentKey: "dept-b"}}}, 200)
	last := openPeriod(200)
	drain(1, 1)
	results = statuses(last)
	if results[second]["status"] != "skipped" || results[3]["status"] != "succeeded" {
		t.Fatal("scope expansion stopped the wrong recipient")
	}
	claim(results[3]["id"], a.readonly, 200, 1)
	access(a.admin, "grant", second, 2, 409)
	if _, err := a.pool.Exec(ctx, `DELETE FROM sys_iam_isolated_subscription_grants
		WHERE workspace_id=1 AND dataset_key='dash-a' AND user_id=2 AND action='create'`); err != nil {
		t.Fatal(err)
	}
	openPeriod(409)
	if retention {
		if _, err := a.pool.Exec(ctx, `UPDATE sys_report_team_subscriptions SET decided_at=statement_timestamp()-interval '31 days',
			review_until=statement_timestamp()-interval '1 day',schedule_checked_at=NULL WHERE id=$1`, plan); err != nil {
			t.Fatal(err)
		}
		if due, err := service.Subscriptions.DueTeamSchedules(ctx); err != nil || len(due) != 0 {
			t.Fatalf("expired approval was still due: %v, %v", due, err)
		}
	}
	t.Log("team deliveries were separate, scope reduction worked for new periods, grant versions fenced revoked results, and expansion required new approval")
}
