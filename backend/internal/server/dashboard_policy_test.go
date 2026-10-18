package server

import (
	"context"
	"fmt"
	"testing"
)

func TestDashboardSubmissionPolicies(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	report, body := dashboardReport(f)
	a.call("POST", "/users", a.admin, map[string]any{"username": "ungranted_admin", "password": "TestPassword123!", "role": "admin"}, 201)
	definition := func(tiles []any, users, groups []int64, allow bool) map[string]any {
		return map[string]any{"name": "策略验收", "description": "", "tiles": tiles, "user_ids": users, "group_ids": groups, "quality": map[string]any{"allow_unknown_freshness": allow, "reason": "业务接受缺少处理水位，明确标记未知", "max_watermark_skew_seconds": 300}}
	}
	card := func(key string, required bool) map[string]any {
		return map[string]any{"key": key, "report_id": report, "required": required, "width": "half"}
	}
	save := func(d map[string]any) int64 {
		out := a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": d}, 200)
		return int64(out["id"].(float64))
	}
	submit := func(id, reviewer int64, want int) {
		a.call("POST", fmt.Sprintf("/managed-dashboards/%d/submit", id), a.developer, map[string]any{"request_id": testRequestID(), "version": 1, "reviewer_id": reviewer}, want)
	}
	t.Run("L01-empty-draft-and-required-card", func(t *testing.T) {
		a.t = t
		id := save(definition([]any{}, []int64{3}, nil, false))
		submit(id, 1, 400)
		id = save(definition([]any{card("optional", false)}, []int64{3}, nil, false))
		submit(id, 1, 400)
	})
	t.Run("L02-card-limit-and-duplicates", func(t *testing.T) {
		a.t = t
		d := definition([]any{card("one", true), card("two", true)}, []int64{3}, nil, false)
		a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": d}, 400)
		tiles := []any{}
		for i := 0; i < 9; i++ {
			tiles = append(tiles, card(fmt.Sprint(i), true))
		}
		d["tiles"] = tiles
		a.call("POST", "/managed-dashboards", a.developer, map[string]any{"request_id": testRequestID(), "definition": d}, 400)
	})
	t.Run("A01-reviewer-needs-ordinary-report-read", func(t *testing.T) {
		a.t = t
		id := save(definition([]any{card("stock", true)}, []int64{3}, nil, false))
		submit(id, 4, 400)
	})
	t.Run("A02-direct-audience-must-be-covered", func(t *testing.T) {
		a.t = t
		id := save(definition([]any{card("stock", true)}, []int64{4}, nil, false))
		submit(id, 1, 400)
	})
	t.Run("A03-explicit-group-grants-cover-future-members", func(t *testing.T) {
		a.t = t
		g := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "业务组", "description": "", "member_ids": []int64{3}}, 200)
		id := save(definition([]any{card("stock", true)}, nil, []int64{int64(g["id"].(float64))}, false))
		submit(id, 1, 400)
	})
	t.Run("A04-empty-group-does-not-count-as-effective-audience", func(t *testing.T) {
		a.t = t
		g := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "空组", "description": "", "member_ids": []int64{}}, 200)
		id := save(definition([]any{card("stock", true)}, nil, []int64{int64(g["id"].(float64))}, false))
		submit(id, 1, 400)
	})
	t.Run("Q03-unknown-freshness-needs-explicit-approval-and-policy", func(t *testing.T) {
		a.t = t
		body["request_id"] = testRequestID()
		body["version"] = 1
		body["data_contract"].(map[string]any)["freshness"].(map[string]any)["mode"] = "unverified"
		a.call("PUT", fmt.Sprintf("/managed-reports/%d/draft", report), a.developer, body, 200)
		review := f.submit(report, 2)
		validation := f.validate(review, []int64{1, 2, 3}, nil)
		a.call("POST", fmt.Sprintf("/report-reviews/%d/decision", review), a.admin, map[string]any{"request_id": testRequestID(), "decision": "approved", "validation_id": validation, "contract_confirmed": true}, 200)
		f.release(report, 1, review, "publish")
		id := save(definition([]any{card("unknown", true)}, []int64{3}, nil, false))
		submit(id, 1, 400)
		id = save(definition([]any{card("unknown", true)}, []int64{3}, nil, true))
		submit(id, 1, 400)
		a.call("PATCH", "/workspaces/1/policy", a.admin, map[string]any{"allow_unknown_freshness": true}, 200)
		submit(id, 1, 200)
		preview := a.call("POST", fmt.Sprintf("/managed-dashboards/%d/refreshes", id), a.developer, map[string]any{"mode": "draft"}, 200)
		result := a.call("POST", "/dashboard-refreshes/"+preview["refresh_id"].(string)+"/tiles/unknown/execute", a.developer, map[string]any{}, 200)
		if result["quality"].(map[string]any)["freshness"] != "unknown" {
			t.Fatal("unknown watermark presented as fresh")
		}
		a.call("PATCH", "/workspaces/1/policy", a.admin, map[string]any{"allow_unknown_freshness": false}, 200)
		a.call("POST", "/dashboard-refreshes/"+preview["refresh_id"].(string)+"/tiles/unknown/execute", a.developer, map[string]any{}, 409)
	})
	t.Run("E04-enforced-native-result-byte-budget", func(t *testing.T) {
		a.t = t
		id, _ := f.draft("SELECT repeat('x',2097152) AS oversized")
		rv := f.submit(id, 1)
		key := testRequestID()
		a.call("POST", fmt.Sprintf("/report-reviews/%d/validate", rv), a.admin, map[string]any{"request_id": key, "account_id": f.account, "user_ids": []int64{3}}, 400)
		var status string
		if e := a.pool.QueryRow(context.Background(), `SELECT status FROM sys_query_executions WHERE request_id=$1::uuid`, key).Scan(&status); e != nil || status != "failed" {
			t.Fatal("oversized execution receipt missing", status, e)
		}
	})
}
