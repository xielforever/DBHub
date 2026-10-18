package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type managedFixture struct {
	a                        *apiTest
	account, definition      int64
	username, password, view string
}

func setupManaged(t *testing.T) *managedFixture {
	a := setupAPI(t)
	ctx := context.Background()
	conn, e := a.deps.Conns.Get(a.actor(), a.connID)
	if e != nil {
		t.Fatal(e)
	}
	f := &managedFixture{a: a, definition: *conn.DefinitionRevisionID, username: fmt.Sprintf("mreport_%d", time.Now().UnixNano()), password: strings.ReplaceAll(testRequestID(), "-", ""), view: a.table + "_report"}
	if _, e = a.pool.Exec(ctx, `CREATE ROLE `+pgx.Identifier{f.username}.Sanitize()+` LOGIN PASSWORD '`+f.password+`'`); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, _ = a.pool.Exec(ctx, `DROP VIEW IF EXISTS public.`+pgx.Identifier{f.view}.Sanitize())
		_, _ = a.pool.Exec(ctx, `DROP ROLE IF EXISTS `+pgx.Identifier{f.username}.Sanitize())
	})
	if _, e = a.pool.Exec(ctx, `CREATE VIEW public.`+pgx.Identifier{f.view}.Sanitize()+` AS SELECT id,email FROM public.`+pgx.Identifier{a.table}.Sanitize()+` WHERE owner_id=1`); e != nil {
		t.Fatal(e)
	}
	if _, e = a.pool.Exec(ctx, `GRANT SELECT ON public.`+pgx.Identifier{f.view}.Sanitize()+` TO `+pgx.Identifier{f.username}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	account := a.call("POST", "/report-accounts", a.admin, map[string]any{"request_id": testRequestID(), "name": "库存只读账号", "definition_revision_id": f.definition, "username": f.username, "password": f.password}, 200)
	f.account = int64(account["id"].(float64))
	return f
}
func (f *managedFixture) draft(sql string) (int64, map[string]any) {
	body := map[string]any{"request_id": testRequestID(), "name": "正式库存", "connection_id": f.a.connectionFor(f.a.developer), "sql": sql, "chart_type": "table", "chart_config": map[string]any{}}
	out := f.a.call("POST", "/managed-reports", f.a.developer, body, 200)
	return int64(out["id"].(float64)), body
}
func (f *managedFixture) submit(id, version int64) int64 {
	out := f.a.call("POST", fmt.Sprintf("/managed-reports/%d/submit", id), f.a.developer, map[string]any{"request_id": testRequestID(), "version": version}, 200)
	return int64(out["id"].(float64))
}
func (f *managedFixture) validate(review int64, users, groups []int64) string {
	key := testRequestID()
	f.a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), f.a.admin, map[string]any{"request_id": key, "account_id": f.account, "user_ids": users, "group_ids": groups}, 200)
	return key
}
func (f *managedFixture) approve(review int64, key string) {
	f.a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), f.a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": key}, 200)
}
func (f *managedFixture) release(id, no, review int64, kind string) map[string]any {
	return f.a.call("POST", fmt.Sprintf("/managed-reports/%d/releases", id), f.a.admin, map[string]any{"request_id": testRequestID(), "expected_publication_no": no, "review_id": review, "kind": kind}, 200)
}
func (f *managedFixture) run(id, no int64, want int) map[string]any {
	return f.a.call("POST", fmt.Sprintf("/published-reports/%d/execute", id), f.a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": no}, want)
}

func TestManagedReportLifecycle(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := context.Background()
	sql := `SELECT current_user AS identity,id,email FROM public.` + f.view + ` ORDER BY id`
	id, body := f.draft(sql)
	path := fmt.Sprintf("/managed-reports/%d", id)
	var review int64
	var validation string
	t.Run("draft-idempotency-and-private-management", func(t *testing.T) {
		a.t = t
		again := a.call("POST", "/managed-reports", a.developer, body, 200)
		if int64(again["id"].(float64)) != id {
			t.Fatal("retry created another report")
		}
		changed := map[string]any{}
		for k, v := range body {
			changed[k] = v
		}
		changed["name"] = "different"
		a.call("POST", "/managed-reports", a.developer, changed, 409)
		a.call("GET", path, a.readonly, nil, 403)
		a.call("GET", "/report-accounts", a.developer, nil, 403)
		a.call("POST", fmt.Sprintf("/published-reports/%d/execute", id), a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 1}, 404)
		list := a.call("GET", "/published-reports", a.readonly, nil, 200)
		if list["total"] != float64(0) {
			t.Fatal("draft is visible")
		}
		var encrypted string
		if e := a.pool.QueryRow(ctx, `SELECT password FROM sys_report_accounts WHERE id=$1`, f.account).Scan(&encrypted); e != nil || encrypted == f.password || strings.Contains(encrypted, f.password) {
			t.Fatal("service credential was not encrypted")
		}
		accounts := a.call("GET", "/report-accounts", a.admin, nil, 200)
		raw, _ := json.Marshal(accounts)
		if strings.Contains(string(raw), f.password) || strings.Contains(string(raw), `"password"`) {
			t.Fatal("account response leaked credentials")
		}
	})
	t.Run("submission-freezes-definition-and-approval-requires-real-validation", func(t *testing.T) {
		a.t = t
		review = f.submit(id, 1)
		if _, e := a.pool.Exec(ctx, `UPDATE sys_report_versions SET definition='{}'::jsonb WHERE report_id=$1`, id); e == nil {
			t.Fatal("submitted definition was mutable")
		}
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.developer, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": testRequestID()}, 403)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": testRequestID()}, 409)
		validation = f.validate(review, []int64{3}, nil)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), a.readonly, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 403)
		f.approve(review, validation)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "rejected", "comment": "too late"}, 409)
		f.release(id, 0, review, "publish")
	})
	t.Run("unbound-viewer-uses-service-identity-and-only-the-approved-view", func(t *testing.T) {
		a.t = t
		a.call("DELETE", fmt.Sprintf("/connections/%d", a.connectionFor(a.readonly)), a.readonly, nil, 200)
		access := a.call("GET", fmt.Sprintf("/datasource-definitions/%d/access", f.definition), a.admin, nil, 200)
		a.call("PUT", fmt.Sprintf("/datasource-definitions/%d/access", f.definition), a.admin, map[string]any{"mode": "restricted", "user_ids": []int64{2}, "group_ids": []int64{}, "expected_version": access["version"]}, 200)
		// A report grant intentionally does not grant direct source access.
		a.call("GET", fmt.Sprintf("/datasource-definitions/%d/revisions", f.definition), a.readonly, nil, 404)
		result := f.run(id, 1, 200)
		rows := result["rows"].([]any)
		if len(rows) != 1 || rows[0].([]any)[0] != f.username {
			t.Fatal("report borrowed another account or bypassed the scoped view", rows)
		}
		list := a.call("GET", "/published-reports", a.readonly, nil, 200)
		encoded, _ := json.Marshal(list)
		if strings.Contains(string(encoded), "sql_text") || strings.Contains(string(encoded), f.username) || strings.Contains(string(encoded), "definition_revision_id") {
			t.Fatal("viewer catalog exposed execution internals")
		}
		a.call("POST", fmt.Sprintf("/published-reports/%d/execute", id), a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 1, "sql": "SELECT secret FROM " + a.table}, 400)
		a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": -f.account, "sql": "SELECT 1"}, 400)
		a.call("POST", fmt.Sprintf("/published-reports/%d/execute", id), a.developer, map[string]any{"request_id": testRequestID(), "publication_no": 1}, 404)
		jobs := a.call("GET", "/query/jobs", a.readonly, nil, 200)
		jobsJSON, _ := json.Marshal(jobs)
		if strings.Contains(string(jobsJSON), sql) {
			t.Fatal("job queue leaked approved SQL")
		}
		history := a.call("GET", "/query/history", a.readonly, nil, 200)
		historyJSON, _ := json.Marshal(history)
		if strings.Contains(string(historyJSON), sql) {
			t.Fatal("viewer history leaked SQL")
		}
	})
	t.Run("new-draft-does-not-change-live-version-and-conflicts-are-detected", func(t *testing.T) {
		a.t = t
		update := map[string]any{"request_id": testRequestID(), "version": 1, "name": "库存 v2", "connection_id": a.connectionFor(a.developer), "sql": "SELECT current_user AS identity,42 AS total", "chart_type": "table", "chart_config": map[string]any{}}
		a.call("PUT", path+"/draft", a.developer, update, 200)
		update["request_id"] = testRequestID()
		a.call("PUT", path+"/draft", a.developer, update, 409)
		if len(f.run(id, 1, 200)["rows"].([]any)[0].([]any)) != 3 {
			t.Fatal("draft replaced the published definition")
		}
		r2 := f.submit(id, 2)
		f.approve(r2, f.validate(r2, []int64{3}, nil))
		f.release(id, 1, r2, "publish")
		f.run(id, 1, 409)
		if f.run(id, 2, 200)["rows"].([]any)[0].([]any)[1] != float64(42) {
			t.Fatal("new version did not run")
		}
		f.release(id, 2, review, "rollback")
		f.run(id, 3, 200)
		f.release(id, 3, 0, "unpublish")
		f.run(id, 3, 404)
		f.release(id, 4, review, "rollback")
		f.run(id, 5, 200)
	})
	t.Run("credential-rotation-and-account-disable-are-live-and-never-fall-back", func(t *testing.T) {
		a.t = t
		accounts := a.call("GET", "/report-accounts", a.admin, nil, 200)["items"].([]any)
		account := accounts[0].(map[string]any)
		a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": account["version"], "password": "WrongPassword123!"}, 200)
		f.run(id, 5, 400)
		account = a.call("GET", "/report-accounts", a.admin, nil, 200)["items"].([]any)[0].(map[string]any)
		a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": account["version"], "password": f.password}, 200)
		f.run(id, 5, 200)
		account = a.call("GET", "/report-accounts", a.admin, nil, 200)["items"].([]any)[0].(map[string]any)
		a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": account["version"], "enabled": false, "expected_reports": 0}, 409)
		a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": account["version"], "enabled": false, "expected_reports": 1}, 200)
		f.run(id, 5, 403)
		account = a.call("GET", "/report-accounts", a.admin, nil, 200)["items"].([]any)[0].(map[string]any)
		a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": account["version"], "enabled": true}, 200)
		f.run(id, 5, 200)
	})
	t.Run("source-stop-confirms-published-impact-and-blocks-service-execution", func(t *testing.T) {
		a.t = t
		revisions := a.call("GET", fmt.Sprintf("/datasource-definitions/%d/revisions", f.definition), a.admin, nil, 200)
		var current map[string]any
		for _, raw := range revisions["items"].([]any) {
			item := raw.(map[string]any)
			if item["id"] == float64(f.definition) {
				current = item
				break
			}
		}
		if current == nil || current["report_count"] != float64(1) {
			t.Fatal("source impact did not include published report")
		}
		path := fmt.Sprintf("/datasource-definitions/%d/status", f.definition)
		a.call("PATCH", path, a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": current["binding_count"], "expected_report_count": 0}, 409)
		a.call("PATCH", path, a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": current["binding_count"], "expected_report_count": 1}, 200)
		f.run(id, 5, 403)
		a.call("PATCH", path, a.admin, map[string]any{"enabled": true}, 200)
		f.run(id, 5, 200)
	})
	t.Run("author-connection-removal-disable-and-transfer-preserve-the-release", func(t *testing.T) {
		a.t = t
		a.call("DELETE", fmt.Sprintf("/connections/%d", a.connectionFor(a.developer)), a.developer, nil, 200)
		f.run(id, 5, 200)
		a.call("DELETE", "/users/2", a.admin, nil, 409)
		a.call("PATCH", "/users/2/status", a.admin, map[string]any{"is_active": false}, 200)
		f.run(id, 5, 200)
		current := a.call("GET", path, a.admin, nil, 200)
		a.call("POST", path+"/transfer", a.admin, map[string]any{"request_id": testRequestID(), "version": current["draft_version"], "owner_id": 1}, 200)
		a.call("DELETE", "/users/2", a.admin, nil, 200)
		f.run(id, 5, 200)
		var submitter *int64
		if e := a.pool.QueryRow(ctx, `SELECT submitted_by FROM sys_report_versions WHERE report_id=$1 AND number=1`, id).Scan(&submitter); e != nil || submitter != nil {
			t.Fatal("history did not survive author deletion", e)
		}
	})
	t.Run("publication-audit-records-caller-service-account-and-manifest", func(t *testing.T) {
		a.t = t
		logs := a.call("GET", "/audit-logs?action=REPORT_PUBLISHED_EXECUTE", a.admin, nil, 200)
		found := false
		for _, raw := range logs["items"].([]any) {
			item := raw.(map[string]any)
			c := item["context"].(map[string]any)
			if c["report_id"] == float64(id) && c["outcome"] == "succeeded" && c["report_publication_no"] == float64(5) {
				found = true
				if c["actor_id"] != float64(3) || c["database_account"] != f.username || c["report_manifest_hash"] == nil || c["report_review_id"] != float64(review) || c["report_publication_no"] != float64(5) {
					t.Fatal("audit identity missing", c)
				}
			}
		}
		if !found {
			t.Fatal("formal execution not audited")
		}
	})
}

func TestManagedReportApprovalBoundaries(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := context.Background()
	t.Run("chart-config-survives-jsonb-normalization-and-freezes-audience", func(t *testing.T) {
		a.t = t
		out := a.call("POST", "/managed-reports", a.developer, map[string]any{"request_id": testRequestID(), "name": "分类库存", "connection_id": a.connectionFor(a.developer), "sql": "SELECT '库存' AS category,12.34::numeric AS amount", "chart_type": "bar", "chart_config": map[string]any{"dimension": "category", "metrics": []string{"amount"}}}, 200)
		id := int64(out["id"].(float64))
		review := f.submit(id, 1)
		key := f.validate(review, []int64{3}, nil)
		f.approve(review, key)
		f.release(id, 0, review, "publish")
		row := f.run(id, 1, 200)["rows"].([]any)[0].([]any)
		if row[1] != "12.34" {
			t.Fatal("managed chart lost decimal precision")
		}
		if _, e := a.pool.Exec(ctx, `UPDATE sys_report_reviews SET account_id=NULL WHERE id=$1`, review); e == nil {
			t.Fatal("approved identity was mutable")
		}
		versions := a.call("GET", fmt.Sprintf("/managed-reports/%d/versions", id), a.admin, nil, 200)
		manifest := versions["items"].([]any)[0].(map[string]any)["manifest"].(map[string]any)
		if len(manifest["user_ids"].([]any)) != 1 {
			t.Fatal("approved audience changed")
		}
		a.call("POST", fmt.Sprintf("/report-accounts/%d/test", f.account), a.admin, map[string]any{}, 200)
		a.call("POST", fmt.Sprintf("/report-accounts/%d/test", f.account), a.developer, map[string]any{}, 403)
	})
	t.Run("self-approval-rejected-even-for-an-administrator", func(t *testing.T) {
		a.t = t
		out := a.call("POST", "/managed-reports", a.admin, map[string]any{"request_id": testRequestID(), "name": "self", "connection_id": a.connID, "sql": "SELECT 1"}, 200)
		id := int64(out["id"].(float64))
		out = a.call("POST", fmt.Sprintf("/managed-reports/%d/submit", id), a.admin, map[string]any{"request_id": testRequestID(), "version": 1}, 200)
		review := int64(out["id"].(float64))
		a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), a.admin, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 400)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "rejected", "comment": "self"}, 400)
	})
	t.Run("service-scope-and-hidden-writes-are-validated-on-the-target", func(t *testing.T) {
		a.t = t
		id, _ := f.draft("SELECT secret FROM public." + a.table)
		review := f.submit(id, 1)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), a.admin, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 400)
		// A bad target query produces no usable approval validation.
		var count int
		if e := a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_report_validations WHERE review_id=$1`, review).Scan(&count); e != nil || count != 0 {
			t.Fatal("failed validation became approvable", e)
		}
		fn := a.table + "_managed_write"
		_, e := a.pool.Exec(ctx, `CREATE FUNCTION public.`+fn+`() RETURNS int LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN UPDATE public.`+a.table+` SET owner_id=99 WHERE id=1; RETURN 1; END $$`)
		if e != nil {
			t.Fatal(e)
		}
		defer a.pool.Exec(ctx, `DROP FUNCTION IF EXISTS public.`+fn+`()`)
		id, _ = f.draft("SELECT public." + fn + "()")
		review = f.submit(id, 1)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", review), a.admin, map[string]any{"request_id": testRequestID(), "account_id": f.account, "user_ids": []int64{3}}, 400)
		var value int
		if e = a.pool.QueryRow(ctx, `SELECT owner_id FROM public.`+a.table+` WHERE id=1`).Scan(&value); e != nil || value != 1 {
			t.Fatal("service preview wrote through read-only transaction")
		}
	})
	t.Run("expired-and-credential-stale-validation-cannot-be-approved", func(t *testing.T) {
		a.t = t
		id, _ := f.draft("SELECT 1 AS total")
		review := f.submit(id, 1)
		key := f.validate(review, []int64{3}, nil)
		expired := testRequestID()
		_, e := a.pool.Exec(ctx, `INSERT INTO sys_report_validations(id,review_id,account_id,account_generation,account_version,manifest,manifest_hash,performed_by,created_at) SELECT $2::uuid,review_id,account_id,account_generation,account_version,manifest,manifest_hash,performed_by,now()-interval '16 minutes' FROM sys_report_validations WHERE id=$1::uuid`, key, expired)
		if e != nil {
			t.Fatal(e)
		}
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": expired}, 409)
		account := a.call("GET", "/report-accounts", a.admin, nil, 200)["items"].([]any)[0].(map[string]any)
		a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": account["version"], "password": f.password}, 200)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": key}, 409)
		f.approve(review, f.validate(review, []int64{3}, nil))
	})
	t.Run("concurrent-review-and-publish-have-one-winner", func(t *testing.T) {
		a.t = t
		id, _ := f.draft("SELECT 1 AS total")
		review := f.submit(id, 1)
		key := f.validate(review, []int64{3}, nil)
		concurrent := func(path string, body func() any) {
			var wg sync.WaitGroup
			results := make(chan executionResponse, 6)
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results <- executionRequest(a.server.Client(), a.server.URL, "POST", path, a.admin, body())
				}()
			}
			wg.Wait()
			close(results)
			won := 0
			for result := range results {
				if result.status == 200 {
					won++
				} else {
					requireExecutionStatus(t, result, 409)
				}
			}
			if won != 1 {
				t.Fatal("concurrent winners", won)
			}
		}
		concurrent(fmt.Sprintf("/report-reviews/%d/decision", review), func() any {
			return map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": key}
		})
		concurrent(fmt.Sprintf("/managed-reports/%d/releases", id), func() any {
			return map[string]any{"request_id": testRequestID(), "expected_publication_no": 0, "review_id": review, "kind": "publish"}
		})
	})
}

func TestManagedReportQueuedRevocation(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := context.Background()
	id, _ := f.draft("SELECT current_user AS identity,pg_sleep(1.5)")
	review := f.submit(id, 1)
	f.approve(review, f.validate(review, []int64{3}, nil))
	f.release(id, 0, review, "publish")
	responses := make(chan executionResponse, 3)
	run := func(key string) {
		go func() {
			responses <- executionRequest(a.server.Client(), a.server.URL, "POST", fmt.Sprintf("/published-reports/%d/execute", id), a.readonly, map[string]any{"request_id": key, "publication_no": 1})
		}()
	}
	run(testRequestID())
	run(testRequestID())
	deadline := time.Now().Add(3 * time.Second)
	running := false
	for time.Now().Before(deadline) {
		var count int
		_ = a.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND state='active' AND query LIKE '%pg_sleep%'`, f.username).Scan(&count)
		if count == 2 {
			running = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !running {
		t.Fatal("viewer slots were not occupied")
	}
	queued := testRequestID()
	run(queued)
	registered := false
	for time.Now().Before(deadline) {
		var count int
		_ = a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_query_executions WHERE user_id=3 AND request_id=$1::uuid AND status='queued'`, queued).Scan(&count)
		if count == 1 {
			registered = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !registered {
		t.Fatal("third request never queued")
	}
	a.call("PATCH", fmt.Sprintf("/report-accounts/%d", f.account), a.admin, map[string]any{"request_id": testRequestID(), "version": 1, "enabled": false, "expected_reports": 1}, 200)
	for i := 0; i < 3; i++ {
		requireExecutionStatus(t, <-responses, 409)
	}
	logs := a.call("GET", "/audit-logs?action=REPORT_PUBLISHED_EXECUTE", a.admin, nil, 200)
	found := false
	for _, raw := range logs["items"].([]any) {
		c := raw.(map[string]any)["context"].(map[string]any)
		if c["execution_request_id"] == queued {
			found = true
			if c["target_opened"] == true {
				t.Fatal("revoked queued request opened a target")
			}
		}
	}
	if !found {
		t.Fatal("queued rejection was not audited")
	}
}

func TestManagedReportRevocationBeforeDelivery(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := context.Background()
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "report readers", "description": "", "member_ids": []int64{3}, "expected_version": 0}, 200)
	groupID := int64(group["id"].(float64))
	id, _ := f.draft("SELECT current_user AS identity,pg_sleep(0.8)")
	review := f.submit(id, 1)
	f.approve(review, f.validate(review, nil, []int64{groupID}))
	f.release(id, 0, review, "publish")
	response := make(chan executionResponse, 1)
	go func() {
		response <- executionRequest(a.server.Client(), a.server.URL, "POST", fmt.Sprintf("/published-reports/%d/execute", id), a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 1})
	}()
	deadline := time.Now().Add(3 * time.Second)
	running := false
	for time.Now().Before(deadline) {
		var count int
		_ = a.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND state='active' AND query LIKE '%pg_sleep%'`, f.username).Scan(&count)
		if count > 0 {
			running = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !running {
		t.Fatal("target query did not start")
	}
	a.call("PUT", fmt.Sprintf("/user-groups/%d", groupID), a.admin, map[string]any{"name": "report readers", "description": "", "member_ids": []int64{}, "expected_version": group["version"]}, 200)
	result := <-response
	requireExecutionStatus(t, result, 409)
	if strings.Contains(string(result.data), f.username) {
		t.Fatal("revoked result delivered")
	}
	f.run(id, 1, 404)
}
