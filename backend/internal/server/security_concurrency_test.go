package server

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/user/dbhub/internal/db"
)

func TestSecurityAuditStaleAdminIdentityContext(t *testing.T) {
	a := setupAPI(t)
	reviewer := securityReviewer(a)
	old, err := a.deps.UserRepo.BindWorkspace(a.actor(), 1)
	if err != nil {
		t.Fatal(err)
	}
	a.call("PATCH", "/workspaces/1/members/1", reviewer, map[string]any{"expected_version": 1, "active": false}, 200)
	a.call("PATCH", "/workspaces/1/members/1", reviewer, map[string]any{"expected_version": 2, "active": true}, 200)
	var previous string
	if err = a.pool.QueryRow(context.Background(), `SELECT password_hash FROM sys_users WHERE id=2`).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	// This models a reset-password request paused between HTTP authorization
	// and the owner-pool mutation. Rejoining must not revive that old request.
	if err = a.deps.UserRepo.UpdatePassword(old, 2, "must-not-be-written"); err == nil {
		t.Error("an old administrator request regained identity mutation rights")
	}
	var after string
	if err = a.pool.QueryRow(context.Background(), `SELECT password_hash FROM sys_users WHERE id=2`).Scan(&after); err != nil || after != previous {
		t.Fatal("a stale request changed another identity", err)
	}
	// A newly authenticated request from the same authorized administrator works.
	a.call("POST", "/users/2/reset-password", a.admin, map[string]any{"new_password": "NewTestPassword123!"}, 200)
}

func TestSecurityAuditConcurrentIndependentDecisions(t *testing.T) {
	a := setupAPI(t)
	reviewer := securityReviewer(a)
	for _, kind := range []string{"ticket", "export"} {
		t.Run(kind, func(t *testing.T) {
			a.t = t
			var path string
			if kind == "ticket" {
				ticket := a.call("POST", "/tickets", a.developer, map[string]any{"title": "concurrent decisions", "table": a.table, "connection_id": a.connectionFor(a.developer), "sql": "UPDATE " + a.table + " SET secret='one' WHERE id=1"}, 200)
				path = fmt.Sprintf("/tickets/%.0f/status", ticket["id"])
			} else {
				export := a.call("POST", "/query/exports", a.developer, map[string]any{"connection_id": a.connectionFor(a.developer), "sql": "SELECT id FROM " + a.table, "format": "json", "reason": "concurrent decisions"}, 200)
				path = fmt.Sprintf("/query/exports/%.0f/review", export["id"])
			}
			var workers sync.WaitGroup
			start := make(chan struct{})
			results := make(chan executionResponse, 6)
			for i := 0; i < 6; i++ {
				workers.Add(1)
				go func(i int) {
					defer workers.Done()
					<-start
					token, status := a.admin, "approved"
					if i%2 == 1 {
						token, status = reviewer, "rejected"
					}
					results <- executionRequest(a.server.Client(), a.server.URL, "POST", path, token, map[string]any{"status": status})
				}(i)
			}
			close(start)
			workers.Wait()
			close(results)
			winners := 0
			for result := range results {
				if result.status == 200 && result.err == nil {
					winners++
				} else {
					requireExecutionStatus(t, result, 409)
				}
			}
			if winners != 1 {
				t.Fatal("concurrent approvals did not have exactly one winner", winners)
			}
		})
	}
}

func TestSecurityAuditReportBenefitRecheckedAtEveryGate(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	otherAdmin := securityReviewer(a)
	for _, gate := range []string{"validate", "register", "decision", "release"} {
		t.Run(gate, func(t *testing.T) {
			a.t = t
			group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "old audience " + gate, "member_ids": []int64{1}}, 200)
			gid := int64(group["id"].(float64))
			id, body := f.draft("SELECT id FROM public." + f.view)
			first := f.submit(id, 1)
			f.approve(first, f.validate(first, []int64{3}, []int64{gid}))
			f.release(id, 0, first, "publish")
			body["request_id"], body["version"], body["name"] = testRequestID(), 1, "group drift "+gate
			a.call("PUT", fmt.Sprintf("/managed-reports/%d/draft", id), a.developer, body, 200)
			review := f.submit(id, 2)
			var validation string
			var candidate *db.ManagedExecution
			var err error
			ctx, err := a.deps.UserRepo.BindWorkspace(a.actor(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if gate == "register" {
				candidate, err = a.deps.Conns.ManagedReports().ValidationCandidate(ctx, review, f.account, []int64{1, 3}, nil)
				if err != nil {
					t.Fatal(err)
				}
			} else if gate != "validate" {
				validation = f.validate(review, []int64{1, 3}, nil)
				if gate == "release" {
					f.approve(review, validation)
				}
			}
			a.call("PUT", fmt.Sprintf("/user-groups/%d", gid), a.admin, map[string]any{"name": "old audience " + gate, "member_ids": []int64{}, "expected_version": 1}, 200)
			switch gate {
			case "validate":
				a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), a.admin, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{1, 3}}, 400)
			case "register":
				if err = a.deps.Conns.ManagedReports().RegisterValidation(ctx, testRequestID(), candidate); err == nil {
					t.Fatal("stale validation became approval evidence")
				}
			case "decision":
				a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation}, 400)
			case "release":
				a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", id), a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": 1, "review_id": review, "kind": "publish"}, 400)
			}
			a.call("GET", fmt.Sprintf("/published-reports/%d", id), a.admin, nil, 404)
			a.call("GET", fmt.Sprintf("/published-reports/%d", id), a.readonly, nil, 200)
			// Restoring the existing group entitlement removes the new benefit;
			// normal validation/publication remains usable.
			a.call("PUT", fmt.Sprintf("/user-groups/%d", gid), otherAdmin, map[string]any{"name": "old audience " + gate, "member_ids": []int64{1}, "expected_version": 2}, 200)
			if gate != "release" {
				f.approve(review, f.validate(review, []int64{1, 3}, nil))
			}
			f.release(id, 1, review, "publish")
		})
	}
}

func TestSecurityAuditOfflineGroupCannotSelfGrant(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	newBeneficiary := securityReviewer(a)
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "historical report audience", "member_ids": []int64{2}}, 200)
	gid := int64(group["id"].(float64))
	id, _ := f.draft("SELECT id FROM public." + f.view)
	review := f.submit(id, 1)
	f.approve(review, f.validate(review, []int64{3}, []int64{gid}))
	f.release(id, 0, review, "publish")
	f.release(id, 1, 0, "unpublish")
	path := fmt.Sprintf("/user-groups/%d", gid)
	input := map[string]any{"name": "historical report audience", "member_ids": []int64{2, 4}, "expected_version": 1}
	a.call("PUT", path, newBeneficiary, input, 400)
	f.release(id, 2, review, "publish")
	a.call("GET", fmt.Sprintf("/published-reports/%d", id), newBeneficiary, nil, 404)
	// Another administrator can explicitly authorize the membership.
	a.call("PUT", path, a.admin, input, 200)
	a.call("GET", fmt.Sprintf("/published-reports/%d", id), newBeneficiary, nil, 200)
}
