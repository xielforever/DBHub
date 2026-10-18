package server

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"testing"
)

func TestDashboardContractFollowingAndAdoption(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	report, body := dashboardReport(f)
	did := publishTestDashboard(f, report, 0)
	path := fmt.Sprintf("/managed-dashboards/%d", did)
	rpath := fmt.Sprintf("/managed-reports/%d", report)
	refresh := a.call("POST", path+"/refreshes", a.readonly, map[string]any{"mode": "published"}, 200)
	publish := func(draftVersion, no int64, breaking bool) int64 {
		body["request_id"] = testRequestID()
		body["version"] = draftVersion
		body["sql"] = body["sql"].(string) + " /* reviewed fix */"
		if breaking {
			body["data_contract"].(map[string]any)["unit"] = "other_count"
		}
		a.call("PUT", rpath+"/draft", a.developer, body, 200)
		rv := f.submit(report, draftVersion+1)
		validation := f.validate(rv, []int64{1, 2, 3}, nil)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", rv), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
		impact := a.call("GET", rpath+fmt.Sprintf("/dashboard-impact?review_id=%d", rv), a.admin, nil, 200)
		expected := float64(0)
		if breaking {
			expected = 1
		}
		if impact["blocked_dashboards"] != expected {
			t.Fatal("incorrect live dependency impact", impact)
		}
		release := map[string]any{"request_id": testRequestID(), "expected_publication_no": no, "review_id": rv, "kind": "publish"}
		if breaking {
			a.call("POST", rpath+"/releases", a.admin, release, 409)
			release["request_id"] = testRequestID()
			release["expected_affected_dashboards"] = 1
		}
		a.call("POST", rpath+"/releases", a.admin, release, 200)
		return rv
	}
	t.Run("V01-compatible-release-follows-without-dashboard-republish", func(t *testing.T) {
		a.t = t
		publish(1, 1, false)
		a.call("POST", "/dashboard-refreshes/"+refresh["refresh_id"].(string)+"/tiles/required/execute", a.readonly, map[string]any{}, 409)
		next := a.call("POST", path+"/refreshes", a.readonly, map[string]any{"mode": "published"}, 200)
		if next["publication_no"] != float64(1) {
			t.Fatal("child SQL fix republished dashboard")
		}
		result := a.call("POST", "/dashboard-refreshes/"+next["refresh_id"].(string)+"/tiles/required/execute", a.readonly, map[string]any{}, 200)
		if result["report_publication_no"] != float64(2) {
			t.Fatal("did not follow actual publication")
		}
	})
	t.Run("V02-breaking-contract-requires-explicit-adoption", func(t *testing.T) {
		a.t = t
		publish(2, 2, true)
		next := a.call("POST", path+"/refreshes", a.readonly, map[string]any{"mode": "published"}, 200)
		if next["tiles"].([]any)[0].(map[string]any)["status"] != "contract_changed" {
			t.Fatal("breaking unit rendered silently")
		}
		draft := a.call("GET", path, a.developer, nil, 200)["draft"].(map[string]any)
		tiles := draft["tiles"].([]any)
		oldHash := tiles[0].(map[string]any)["contract_hash"]
		catalog := a.call("GET", "/dashboard-report-catalog", a.developer, nil, 200)["items"].([]any)[0].(map[string]any)
		tiles[0].(map[string]any)["contract_hash"] = catalog["data_contract"].(map[string]any)["hash"]
		a.call("PUT", path+"/draft", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "definition": draft}, 400)
		tiles[0].(map[string]any)["contract_hash"] = oldHash
		a.call("PUT", path+"/draft", a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "definition": draft}, 200)
		a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 2, "reviewer_id": 1}, 409)
		a.call("POST", path+"/adopt-contract", a.developer, map[string]any{"request_id": testRequestID(), "version": 2, "report_id": report, "publication_no": 2}, 409)
		a.call("POST", path+"/adopt-contract", a.developer, map[string]any{"request_id": testRequestID(), "version": 2, "report_id": report, "publication_no": 3}, 200)
		a.call("POST", path+"/submit", a.developer, map[string]any{"request_id": testRequestID(), "version": 3, "reviewer_id": 1}, 200)
	})
}

func TestDashboardClusterQuotasAcrossPools(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := a.actor()
	a.call("POST", "/users", a.admin, map[string]any{"username": "quota_admin", "password": "TestPassword123!", "role": "admin"}, 201)
	second, e := pgxpool.NewWithConfig(ctx, a.pool.Config())
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	defer db.CloseScopedRuntime(second)
	if _, e = db.Migrate(ctx, second); e != nil {
		t.Fatal(e)
	}
	repos := []*db.ManagedReportRepository{a.deps.Conns.ManagedReports(), db.NewConnectionRepository(second, nil).ManagedReports()}
	cleanups := []func(){}
	defer func() {
		for _, fn := range cleanups {
			fn()
		}
	}()
	memberCtx := func(wid, user int64) context.Context {
		role := map[int64]string{1: "admin", 2: "developer", 3: "readonly", 4: "admin"}[user]
		return context.WithValue(db.WithWorkspace(ctx, wid), auth.ClaimsContextKey, &auth.Claims{Subject: user, Role: role})
	}
	newSpace := func() int64 {
		w, e := a.deps.UserRepo.CreateWorkspace(ctx, "quota-space")
		if e != nil {
			t.Fatal(e)
		}
		for _, m := range []struct{ name, role string }{{"developer", "developer"}, {"readonly", "readonly"}, {"quota_admin", "admin"}} {
			if e = a.deps.UserRepo.AddMember(memberCtx(w.ID, 1), m.name, m.role); e != nil {
				t.Fatal(e)
			}
		}
		return w.ID
	}
	copyAccount := func(wid int64) int64 {
		var family string
		var source, account int64
		e := a.pool.QueryRow(ctx, `INSERT INTO sys_source_families(workspace_id,family_id) VALUES($1,gen_random_uuid()) RETURNING family_id::text`, wid).Scan(&family)
		if e != nil {
			t.Fatal(e)
		}
		e = a.pool.QueryRow(ctx, `INSERT INTO sys_source_definitions(workspace_id,family_id,revision,name,owner_id,visibility,config) SELECT $1,$2::uuid,1,'quota source',1,'global',config FROM sys_source_definitions WHERE id=$3 RETURNING id`, wid, family, f.definition).Scan(&source)
		if e != nil {
			t.Fatal(e)
		}
		e = a.pool.QueryRow(ctx, `INSERT INTO sys_report_accounts(workspace_id,name,definition_revision_id,username,password,created_by) SELECT $1,'quota account',$2,username,password,1 FROM sys_report_accounts WHERE id=$3 RETURNING id`, wid, source, f.account).Scan(&account)
		if e != nil {
			t.Fatal(e)
		}
		return account
	}
	acquire := func(repo int, wid, user, account int64) (func(), error) {
		return repos[repo%2].AcquireExecutionLease(memberCtx(wid, user), &db.ManagedExecution{AccountID: account})
	}
	t.Run("E05-per-account-and-per-user", func(t *testing.T) {
		other := copyAccount(1)
		local := []func(){}
		for i := 0; i < 4; i++ {
			account := f.account
			if i%2 == 1 {
				account = other
			}
			release, e := acquire(i, 1, 1, account)
			if e != nil {
				t.Fatal(e)
			}
			local = append(local, release)
		}
		if release, e := acquire(1, 1, 1, other); e == nil {
			release()
			t.Fatal("cross-pool user limit bypassed")
		}
		for _, fn := range local {
			fn()
		}
		local = nil
		for i := 0; i < 4; i++ {
			user := int64(1)
			if i%2 == 1 {
				user = 3
			}
			release, e := acquire(i, 1, user, f.account)
			if e != nil {
				t.Fatal(e)
			}
			local = append(local, release)
		}
		if release, e := acquire(1, 1, 2, f.account); e == nil {
			release()
			t.Fatal("cross-pool account limit bypassed")
		}
		for _, fn := range local {
			fn()
		}
	})
	for i := 0; i < 4; i++ {
		wid := int64(1)
		if i > 0 {
			wid = newSpace()
		}
		for user := int64(1); user <= 4; user++ {
			account := copyAccount(wid)
			for j := 0; j < 4; j++ {
				release, e := acquire(j, wid, user, account)
				if e != nil {
					t.Fatal(e)
				}
				cleanups = append(cleanups, release)
			}
		}
		if i == 0 {
			extra := copyAccount(wid)
			if release, e := acquire(0, wid, 1, extra); e == nil {
				release()
				t.Fatal("workspace cap bypassed")
			}
		}
	}
	wid := newSpace()
	account := copyAccount(wid)
	if release, e := acquire(1, wid, 1, account); e == nil {
		release()
		t.Fatal("global cluster quota bypassed")
	}
	var leases int
	if e = a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_dashboard_query_leases`).Scan(&leases); e != nil || leases != 64 {
		t.Fatal("unexpected shared lease count", leases, e)
	}

}
