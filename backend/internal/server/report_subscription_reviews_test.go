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

func verifySubscriptionReviews(t *testing.T, a *apiTest, report, reviewerID, newRecipient int64, reviewer, administrator string) {
	t.Helper()
	ctx := context.Background()
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	definition := map[string]any{"name": "正式订阅复核", "report_id": report, "publication_no": 1, "interval_minutes": 5,
		"params": map[string]any{"category": "blue"}, "enabled": true}
	personal := func() int64 {
		t.Helper()
		result := a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(), "definition": definition}, 200)
		return int64(result["id"].(float64))
	}
	legacy := personal()
	if count, err := service.Subscriptions.GenerateDue(ctx); err != nil || count != 1 {
		t.Fatalf("legacy subscription did not enqueue: %d %v", count, err)
	}
	if run, err := service.RunSubscription(ctx); err != nil || run.SubscriptionID != legacy {
		t.Fatalf("legacy subscription did not run: %v", err)
	}
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_subscription_reviews VALUES(1,$1)`, report); err != nil {
		t.Fatal(err)
	}
	id := personal()
	path := fmt.Sprintf("/report-subscriptions/%d", id)
	makeCommand := func(version int64) map[string]any {
		return map[string]any{"request_id": testRequestID(), "version": version}
	}
	a.call("POST", path+"/runs", a.readonly, makeCommand(1), 409)
	if count, err := service.Subscriptions.GenerateDue(ctx); err != nil || count != 0 {
		t.Fatalf("personal subscription ran before confirmation: %d %v", count, err)
	}
	a.call("POST", path+"/confirm", a.developer, makeCommand(1), 404)
	confirm := makeCommand(1)
	a.call("POST", path+"/confirm", a.readonly, confirm, 200)
	a.call("POST", path+"/confirm", a.readonly, confirm, 200)
	a.call("POST", path+"/confirm", a.readonly, makeCommand(1), 409)
	var valid bool
	if err := a.pool.QueryRow(ctx, `SELECT enabled AND review_until BETWEEN now()+interval '89 days' AND now()+interval '91 days'
		AND version=2 FROM sys_report_subscriptions WHERE id=$1`, id).Scan(&valid); err != nil || !valid {
		t.Fatalf("personal confirmation did not persist a 90-day review window: %v", err)
	}
	a.call("POST", path+"/runs", a.readonly, makeCommand(2), 200)
	run, err := service.RunSubscription(ctx)
	if err != nil || run.SubscriptionID != id {
		t.Fatalf("confirmed personal subscription did not run: %v", err)
	}
	snapshot := a.call("GET", fmt.Sprintf("/report-deliveries/%d", run.DeliveryID), a.readonly, nil, 200)["snapshot"].(map[string]any)
	rows := snapshot["result"].(map[string]any)["rows"].([]any)
	if len(rows) != 1 || rows[0].([]any)[1] != "blue" {
		t.Fatal("personal run changed frozen parameters")
	}
	if _, err = a.pool.Exec(ctx, `UPDATE sys_report_subscriptions SET review_until=now()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	a.call("POST", path+"/runs", a.readonly, makeCommand(2), 409)
	if count, err := service.Subscriptions.GenerateDue(ctx); err != nil || count != 0 {
		t.Fatalf("expired subscription was silently renewed: %d %v", count, err)
	}
	a.call("POST", path+"/confirm", a.readonly, makeCommand(2), 200)
	a.call("PUT", path, a.readonly, map[string]any{"request_id": testRequestID(), "version": 3, "definition": definition}, 200)
	a.call("POST", path+"/runs", a.readonly, makeCommand(4), 409)
	a.call("POST", path+"/confirm", a.readonly, makeCommand(4), 200)
	a.call("POST", path+"/runs", a.readonly, makeCommand(5), 200)
	setCustodyViewerScope(a, "dash-a", 1, "dept-a", "dept-b")
	if _, err := service.RunSubscription(ctx); !errors.Is(err, db.ErrManagedConflict) {
		t.Fatalf("personal run ignored scope drift: %v", err)
	}
	setCustodyViewerScope(a, "dash-a", 2, "dept-a")
	t.Log("personal plan required explicit activation and renewal, kept frozen parameters, and stopped on scope change")

	const team = "/report-team-subscriptions"
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "团队订阅合成组", "member_ids": []int64{3}}, 200)
	groupID := int64(group["id"].(float64))
	teamInput := func(previous int64) map[string]any {
		return map[string]any{"request_id": testRequestID(), "definition": definition, "group_ids": []int64{groupID}, "previous_id": previous}
	}
	a.call("POST", team, a.developer, teamInput(0), 403)
	addGrant := func(user int64, dataset, action string) {
		t.Helper()
		_, err = a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_subscription_grants(workspace_id,dataset_key,user_id,action,valid_until)
			VALUES(1,$1,$2,$3,now()+interval '2 hours')`, dataset, user, action)
		if err != nil {
			t.Fatal(err)
		}
	}
	addGrant(2, "dash-a", "create")
	addGrant(2, "dash-a", "review")
	addGrant(3, "dash-a", "review")
	addGrant(reviewerID, "dash-b", "review")
	first := int64(a.call("POST", team, a.developer, teamInput(0), 200)["id"].(float64))
	reviewPath := fmt.Sprintf("%s/%d/review", team, first)
	for _, item := range []struct {
		token string
		code  int
	}{{a.developer, 403}, {a.readonly, 403}, {administrator, 404}, {reviewer, 404}} {
		a.call("GET", reviewPath, item.token, nil, item.code)
	}
	addGrant(reviewerID, "dash-a", "review")
	scope := func(version int64, department string) {
		t.Helper()
		now := time.Now().UTC()
		a.call("PUT", fmt.Sprintf("/workspaces/1/business-identities/%d/datasets/dash-a/organization-scope", reviewerID), a.admin,
			db.OrganizationScopeInput{BusinessMappingSource: db.BusinessMappingSource{Authority: "隔离复核", SourceVersion: fmt.Sprint(version),
				ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}, ExpectedVersion: &version,
				IdentityVersion: 1, DatasetVersion: 1, Scopes: []db.OrganizationScopeEntry{{DepartmentKey: department}}}, 200)
	}
	scope(1, "dept-b")
	a.call("GET", reviewPath, reviewer, nil, 403)
	scope(2, "dept-a")
	proof := a.call("GET", reviewPath, reviewer, nil, 200)["basis_sha256"].(string)
	decide := map[string]any{"request_id": testRequestID(), "version": 1, "basis_sha256": proof, "decision": "approved"}
	a.call("POST", reviewPath, reviewer, decide, 200)
	a.call("POST", reviewPath, reviewer, decide, 200)
	if err = a.pool.QueryRow(ctx, `SELECT reviewer_id=$2 AND review_until=decided_at+interval '30 days'
		AND recipient_ids=ARRAY[3]::bigint[] AND encrypted_approval<>'' FROM sys_report_team_subscriptions WHERE id=$1`,
		first, reviewerID).Scan(&valid); err != nil || !valid {
		t.Fatalf("team review did not seal the frozen recipients and 30-day decision: %v", err)
	}
	a.call("PUT", fmt.Sprintf("/user-groups/%d", groupID), a.admin,
		map[string]any{"name": "团队订阅合成组", "member_ids": []int64{3, newRecipient}, "expected_version": group["version"]}, 200)
	old := fmt.Sprintf("%s/%d", team, first)
	out := a.call("GET", old, a.developer, nil, 200)
	if ids := out["recipient_ids"].([]any); len(ids) != 1 || ids[0] != float64(3) {
		t.Fatal("existing approval automatically added a group member")
	}
	next := int64(a.call("POST", team, a.developer, teamInput(first), 200)["id"].(float64))
	out = a.call("GET", fmt.Sprintf("%s/%d", team, next), a.developer, nil, 200)
	if len(out["recipient_ids"].([]any)) != 2 || out["status"] != "pending" || a.call("GET", old, a.developer, nil, 200)["superseded"] != true {
		t.Fatal("team resubmission did not retain history and freeze a new list")
	}
	a.call("POST", team, a.developer, teamInput(first), 409)
	nextReview := fmt.Sprintf("%s/%d/review", team, next)
	proof = a.call("GET", nextReview, reviewer, nil, 200)["basis_sha256"].(string)
	if _, err = a.pool.Exec(ctx, `DELETE FROM sys_iam_isolated_subscription_grants
		WHERE workspace_id=1 AND dataset_key='dash-a' AND user_id=2 AND action='create'`); err != nil {
		t.Fatal(err)
	}
	decide = map[string]any{"request_id": testRequestID(), "version": 1, "basis_sha256": proof, "decision": "approved"}
	a.call("POST", nextReview, reviewer, decide, 409)
	addGrant(2, "dash-a", "create")
	a.call("POST", nextReview, reviewer, decide, 200)
	if err = a.pool.QueryRow(ctx, `SELECT status='approved' AND cardinality(recipient_ids)=2
		FROM sys_report_team_subscriptions WHERE id=$1`, next).Scan(&valid); err != nil || !valid {
		t.Fatalf("independent reviewer did not approve the new two-person list: %v", err)
	}
	pending := int64(a.call("POST", team, a.developer, teamInput(next), 200)["id"].(float64))
	pendingReview := fmt.Sprintf("%s/%d/review", team, pending)
	proof = a.call("GET", pendingReview, reviewer, nil, 200)["basis_sha256"].(string)
	a.call("PUT", fmt.Sprintf("/user-groups/%d", groupID), a.admin,
		map[string]any{"name": "团队订阅合成组", "member_ids": []int64{3}, "expected_version": 2}, 200)
	a.call("POST", pendingReview, reviewer,
		map[string]any{"request_id": testRequestID(), "version": 1, "basis_sha256": proof, "decision": "approved"}, 409)
	if count, err := service.Subscriptions.GenerateDue(ctx); err != nil || count != 0 {
		t.Fatalf("team approval entered a personal delivery queue: %d %v", count, err)
	}
	t.Log("team approval required an independent dataset grant and complete scope, and group expansion left the old list unchanged")
}
