package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"strings"
	"sync"
	"testing"
)

func TestSourceAudienceAndDirectory(t *testing.T) {
	a := setupAPI(t)
	base, password, e := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if e != nil {
		t.Fatal(e)
	}
	cfg := db.ConfigOf(base)
	definition := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "库存团队目录", "visibility": "global", "access_mode": "restricted", "config": cfg}, 201)
	id := int64(definition["id"].(float64))
	path := fmt.Sprintf("/datasource-definitions/%d", id)
	identity := map[string]any{"username": base.Username, "password": password, "confirmed": true}
	access := func(users, groups []int64, version int64, status int) {
		a.call("PUT", path+"/access", a.admin, map[string]any{"mode": "restricted", "user_ids": users, "group_ids": groups, "expected_version": version}, status)
	}
	t.Run("restricted-create-has-no-public-window", func(t *testing.T) {
		a.t = t
		for _, token := range []string{a.developer, a.readonly} {
			a.call("GET", path+"/revisions", token, nil, 404)
			a.call("POST", path+"/connections", token, identity, 404)
		}
		a.call("GET", path+"/access", a.developer, nil, 404)
		a.call("GET", "/user-groups", a.developer, nil, 403)
		a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "invalid audience", "visibility": "global", "access_mode": "invalid", "config": cfg}, 400)
		a.call("POST", "/user-groups", a.admin, map[string]any{"name": "", "member_ids": []int64{}}, 400)
		a.call("POST", "/user-groups", a.admin, map[string]any{"name": "invalid members", "member_ids": []int64{999999}}, 400)
		invalid := a.call("GET", "/user-groups?keyword=invalid", a.admin, nil, 200)
		if invalid["total"] != float64(0) {
			t.Fatal("failed group creation was not rolled back")
		}

	})
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "库存读者", "description": "验收团队", "member_ids": []int64{3}}, 200)
	gid := int64(group["id"].(float64))
	a.call("POST", "/user-groups", a.admin, map[string]any{"name": "库存读者", "member_ids": []int64{}}, 409)

	gp := fmt.Sprintf("/user-groups/%d", gid)
	access([]int64{2}, []int64{gid}, 1, 200)
	dev := a.call("POST", path+"/connections", a.developer, identity, 201)
	devID := int64(dev["id"].(float64))
	devPath := fmt.Sprintf("/connections/%d", devID)
	ro := a.call("POST", path+"/connections", a.readonly, identity, 201)
	roID := int64(ro["id"].(float64))
	t.Run("direct-or-group-grant-and-version-validation", func(t *testing.T) {
		a.t = t
		a.call("POST", devPath+"/test", a.developer, nil, 200)
		a.call("GET", devPath, a.admin, nil, 404)
		access(nil, nil, 1, 409)
		access([]int64{999999}, nil, 2, 400)
		access([]int64{2, 2}, nil, 2, 400)
		result := a.call("GET", path+"/access", a.admin, nil, 200)
		if result["version"] != float64(2) {
			t.Fatal("invalid grant changed policy")
		}
		a.call("PUT", gp, a.readonly, map[string]any{"name": "steal", "member_ids": []int64{3}, "expected_version": 1}, 403)
	})
	t.Run("directory-profile-favorite-recent-and-pagination", func(t *testing.T) {
		a.t = t
		body := map[string]any{"category": "供应链", "tags": []string{" 核心 ", "核心", "库存"}, "maintainer": "演示负责人", "contact": "inventory@example.test", "expected_version": 1}
		a.call("PUT", path+"/profile", a.developer, body, 404)
		a.call("PUT", path+"/profile", a.admin, body, 200)
		a.call("PUT", path+"/profile", a.admin, body, 409)
		a.call("PUT", path+"/favorite", a.developer, map[string]any{"favorite": true}, 200)
		a.call("PUT", path+"/favorite", a.developer, map[string]any{"favorite": true}, 200)
		list := a.call("GET", "/datasource-definitions?view=favorites&page_size=1", a.developer, nil, 200)
		if list["total"] != float64(1) {
			t.Fatal("favorite not idempotent")
		}
		d := list["items"].([]any)[0].(map[string]any)
		if d["category"] != "供应链" || len(d["tags"].([]any)) != 2 || d["maintainer"] != "演示负责人" {
			t.Fatal("profile data mismatch")
		}
		empty := a.call("GET", "/datasource-definitions?view=favorites&page=2&page_size=1", a.developer, nil, 200)
		if empty["total"] != float64(1) || len(empty["items"].([]any)) != 0 {
			t.Fatal("empty page total lost")
		}
		other := a.call("GET", "/datasource-definitions?view=favorites", a.readonly, nil, 200)
		if other["total"] != float64(0) {
			t.Fatal("favorite leaked across users")
		}
		recent := a.call("GET", "/datasource-definitions?view=recent&keyword=库存团队目录", a.developer, nil, 200)
		if recent["total"] != float64(1) || recent["items"].([]any)[0].(map[string]any)["last_used_at"] == nil {
			t.Fatal("physical open did not record usage")
		}
		never := a.call("GET", "/datasource-definitions?view=recent&keyword=库存团队目录", a.readonly, nil, 200)
		if never["total"] != float64(0) {
			t.Fatal("merely saving a binding fabricated recent use")
		}
		for _, q := range []string{"page=0", "page_size=101", "view=invalid"} {
			a.call("GET", "/datasource-definitions?"+q, a.admin, nil, 400)
		}
	})
	requestID := testRequestID()
	a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": devID, "request_id": requestID, "sql": "SELECT 1"}, 200)
	a.call("POST", "/assets/sync", a.developer, map[string]any{"connection_id": devID}, 200)
	export := a.call("POST", "/query/exports", a.developer, map[string]any{"connection_id": devID, "sql": "SELECT 1", "format": "json", "reason": "owned snapshot acceptance"}, 200)
	ep := fmt.Sprintf("/query/exports/%d", int64(export["id"].(float64)))
	a.call("POST", ep+"/review", a.admin, map[string]any{"status": "approved"}, 200)
	a.call("GET", ep+"/download", a.developer, nil, 200)
	report := a.call("POST", "/reports", a.developer, map[string]any{"name": "restricted report", "connection_id": devID, "database": base.Database, "sql": "SELECT 1", "visibility": "shared"}, 200)
	rp := fmt.Sprintf("/reports/%d", int64(report["id"].(float64)))
	viewer := a.call("GET", rp, a.readonly, nil, 200)
	if viewer["connection_id"] != float64(roID) {
		t.Fatal("shared report inherited another identity")
	}
	t.Run("audit-correlates-identity-revision-and-durable-outcome", func(t *testing.T) {
		a.t = t
		result := a.call("GET", "/audit-logs?database_account="+base.Username+"&outcome=succeeded", a.admin, nil, 200)
		found := false
		for _, raw := range result["items"].([]any) {
			row := raw.(map[string]any)
			c := row["context"].(map[string]any)
			if c["execution_request_id"] == requestID {
				found = true
				if c["actor_username"] != "developer" || c["database_account"] != base.Username || c["definition_revision_id"] != definition["id"] || c["source_family_id"] != definition["family_id"] || c["target_opened"] != true {
					t.Fatal("audit correlation missing")
				}
			}
		}
		if !found {
			t.Fatal("query audit record missing")
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), password) || strings.Contains(string(raw), "SELECT 1") {
			t.Fatal("audit contains credential or SQL body")
		}
		a.call("GET", "/audit-logs", a.developer, nil, 403)
		a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": devID, "request_id": requestID, "sql": "SELECT 1"}, 409)
		duplicate := a.call("GET", "/audit-logs?outcome=duplicate&status=failed", a.admin, nil, 200)
		if duplicate["total"] != float64(1) {
			t.Fatal("duplicate recorded as fresh execution")
		}
	})
	t.Run("group-removal-revokes-existing-personal-connection", func(t *testing.T) {
		a.t = t
		a.call("PUT", gp, a.admin, map[string]any{"name": "库存读者", "member_ids": []int64{}, "expected_version": 1}, 200)
		a.call("PUT", gp, a.admin, map[string]any{"name": "stale", "member_ids": []int64{3}, "expected_version": 1}, 409)
		a.call("GET", path+"/revisions", a.readonly, nil, 404)
		a.call("GET", rp, a.readonly, nil, 404)
		a.call("POST", rp+"/execute", a.readonly, map[string]any{"version": 1, "request_id": testRequestID()}, 404)
		a.call("POST", "/query/execute", a.readonly, map[string]any{"connection_id": roID, "sql": "SELECT 1"}, 403)
		a.call("POST", devPath+"/test", a.developer, nil, 200)
	})
	t.Run("family-revocation-blocks-history-metadata-and-export", func(t *testing.T) {
		a.t = t
		next := a.call("POST", path+"/revisions", a.admin, map[string]any{"name": "库存团队目录 v2", "visibility": "global", "config": cfg}, 201)
		access(nil, nil, 2, 200)
		for _, rev := range []int64{id, int64(next["id"].(float64))} {
			a.call("GET", fmt.Sprintf("/datasource-definitions/%d/revisions", rev), a.developer, nil, 404)
		}
		a.call("POST", devPath+"/test", a.developer, nil, 403)
		caps := a.call("GET", devPath+"/capabilities", a.developer, nil, 200)
		if caps["sql"] != false || caps["metadata"] != false {
			t.Fatal("revoked connection advertises execution")
		}
		a.call("GET", ep+"/download", a.developer, nil, 404)
		a.call("GET", rp, a.developer, nil, 404)
		a.call("POST", rp+"/execute", a.developer, map[string]any{"version": 1, "request_id": testRequestID()}, 404)
		shared := a.call("GET", "/reports?scope=shared", a.developer, nil, 200)
		if shared["total"] != float64(0) {
			t.Fatal("revoked report source remains visible")
		}
		history := a.call("GET", fmt.Sprintf("/query/history?connection_id=%d", devID), a.developer, nil, 200)
		if history["total"] != float64(0) {
			t.Fatal("revoked cached history visible")
		}
		assets := a.call("GET", fmt.Sprintf("/assets/tables?connection_id=%d", devID), a.developer, nil, 200)
		if assets["total"] != float64(0) {
			t.Fatal("revoked cached structure visible")
		}
		fav := a.call("GET", "/datasource-definitions?view=favorites", a.developer, nil, 200)
		if fav["total"] != float64(0) {
			t.Fatal("revoked favorite remains discoverable")
		}
		a.call("GET", devPath, a.developer, nil, 200)
		a.call("DELETE", devPath, a.developer, nil, 200)
		a.call("DELETE", gp, a.admin, map[string]any{"expected_version": 2}, 200)
	})
}

func TestSourcePolicyConcurrentUpdates(t *testing.T) {
	a := setupAPI(t)
	base, _, e := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if e != nil {
		t.Fatal(e)
	}
	d := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "concurrent policy", "visibility": "global", "config": db.ConfigOf(base)}, 201)
	p := fmt.Sprintf("/datasource-definitions/%d/access", int64(d["id"].(float64)))
	start := make(chan struct{})
	statuses := make(chan int, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res := executionRequest(a.server.Client(), a.server.URL, "PUT", p, a.admin, map[string]any{"mode": "restricted", "user_ids": []int64{2}, "group_ids": []int64{}, "expected_version": 1})
			statuses <- res.status
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)
	success, conflict := 0, 0
	for s := range statuses {
		if s == 200 {
			success++
		} else if s == 409 {
			conflict++
		} else {
			t.Fatalf("unexpected concurrent status %d", s)
		}
	}
	if success != 1 || conflict != 7 {
		t.Fatal("concurrent edit overwrote accepted policy")
	}
}

func TestSourceBindConcurrentRevocation(t *testing.T) {
	a := setupAPI(t)
	base, password, e := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 6; i++ {
		d := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": fmt.Sprint("bind-revoke-", i), "visibility": "global", "config": db.ConfigOf(base)}, 201)
		p := fmt.Sprintf("/datasource-definitions/%d", int64(d["id"].(float64)))
		var statuses [2]int
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			statuses[0] = executionRequest(a.server.Client(), a.server.URL, "POST", p+"/connections", a.developer, map[string]any{"username": base.Username, "password": password, "confirmed": true}).status
		}()
		go func() {
			defer wg.Done()
			<-start
			statuses[1] = executionRequest(a.server.Client(), a.server.URL, "PUT", p+"/access", a.admin, map[string]any{"mode": "restricted", "user_ids": []int64{}, "group_ids": []int64{}, "expected_version": 1}).status
		}()
		close(start)
		wg.Wait()
		if statuses[1] != 200 || (statuses[0] != 201 && statuses[0] != 403 && statuses[0] != 404) {
			t.Fatalf("unexpected bind/revoke statuses %v", statuses)
		}
		var bound int64
		if e = a.pool.QueryRow(a.actor(), `SELECT COALESCE(max(id),0) FROM sys_connections WHERE user_id=2 AND definition_family_id=$1::uuid`, d["family_id"]).Scan(&bound); e != nil {
			t.Fatal(e)
		}
		if bound > 0 {
			a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": bound, "sql": "SELECT 1"}, 403)
		}
		a.call("GET", p+"/revisions", a.developer, nil, 404)
	}
}

func TestDemotedPublisherCannotManageThroughPrivateHistory(t *testing.T) {
	a := setupAPI(t)
	base, _, e := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if e != nil {
		t.Fatal(e)
	}
	private := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "before publication", "config": db.ConfigOf(base)}, 201)
	id := int64(private["id"].(float64))
	path := fmt.Sprintf("/datasource-definitions/%d", id)
	a.call("POST", path+"/revisions", a.admin, map[string]any{"name": "published", "visibility": "global", "config": db.ConfigOf(base)}, 201)
	if _, e = a.pool.Exec(a.actor(), `UPDATE sys_users SET role_id=(SELECT id FROM sys_roles WHERE code='developer') WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{Subject: 1, Role: "developer"})
	if e = a.deps.Conns.SetDefinitionProfile(ctx, id, db.SourceProfile{Category: "bypass"}, 1); e != db.ErrNotFound {
		t.Fatal("private predecessor bypassed published family management")
	}
	next := db.SourceDefinition{Name: "private bypass", Visibility: "private", Config: db.ConfigOf(base)}
	if e = a.deps.Conns.SaveDefinition(ctx, &next, id); e != db.ErrNotFound {
		t.Fatal("demoted publisher appended a private family branch")
	}
}
