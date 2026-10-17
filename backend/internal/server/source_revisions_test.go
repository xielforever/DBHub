package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/user/dbhub/internal/db"
)

func TestSourceRevisionGovernance(t *testing.T) {
	a := setupAPI(t)
	base, password, err := a.deps.Conns.GetSecrets(a.actor(), a.connID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := db.ConfigOf(base)
	create := func(name string) map[string]any {
		return a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": name, "visibility": "global", "config": cfg}, 201)
	}
	first := create("库存修订")
	id := int64(first["id"].(float64))
	path := fmt.Sprintf("/datasource-definitions/%d", id)
	nextConfig := cfg
	nextConfig.Host = "new-inventory.example.test"
	nextConfig.Port = 6432
	second := a.call("POST", path+"/revisions", a.admin, map[string]any{"name": "库存修订", "description": "目标调整", "visibility": "global", "config": nextConfig}, 201)
	identity := map[string]any{"username": base.Username, "password": password, "confirmed": true}
	personal := a.call("POST", path+"/connections", a.developer, identity, 201)
	personalID := int64(personal["id"].(float64))

	t.Run("history-diffs-counts-pagination-and-privacy", func(t *testing.T) {
		a.t = t
		admin := a.call("GET", path+"/revisions?page_size=1", a.admin, nil, 200)
		row := admin["items"].([]any)[0].(map[string]any)
		if admin["total"] != float64(2) || row["id"] != second["id"] || row["previous_revision"] != float64(1) || row["binding_count"] != float64(0) {
			t.Fatal("incorrect history order, comparison or count")
		}
		fields := map[string]bool{}
		for _, raw := range row["changes"].([]any) {
			c := raw.(map[string]any)
			fields[c["field"].(string)] = true
			if c["field"] == "host" && (c["before"] != cfg.Host || c["after"] != nextConfig.Host) {
				t.Fatal("target diff lost values")
			}
		}
		if !fields["host"] || !fields["port"] || !fields["description"] {
			t.Fatal("target differences missing")
		}
		page2 := a.call("GET", path+"/revisions?page_size=1&page=2", a.admin, nil, 200)["items"].([]any)[0].(map[string]any)
		if page2["id"] != first["id"] || page2["binding_count"] != float64(1) {
			t.Fatal("historical binding count incorrect")
		}
		empty := a.call("GET", path+"/revisions?page=3&page_size=1", a.admin, nil, 200)
		if empty["total"] != float64(2) || len(empty["items"].([]any)) != 0 {
			t.Fatal("empty page lost total")
		}
		for _, token := range []string{a.developer, a.readonly} {
			visible := a.call("GET", path+"/revisions", token, nil, 200)
			raw, _ := json.Marshal(visible)
			if strings.Contains(string(raw), "binding_count") || strings.Contains(string(raw), password) || strings.Contains(string(raw), "username") {
				t.Fatal("history leaked personal information")
			}
		}
		a.call("GET", path+"/revisions?page=0", a.admin, nil, 400)
		a.call("GET", path+"/revisions?page_size=101", a.admin, nil, 400)
		a.call("GET", path+"/revisions", "", nil, 401)
	})
	t.Run("private-predecessors-never-leak-through-a-global-diff", func(t *testing.T) {
		a.t = t
		privateCfg := cfg
		privateCfg.Host = "private-predecessor.invalid"
		private := a.call("POST", "/datasource-definitions", a.admin, map[string]any{"name": "private-predecessor-name", "config": privateCfg}, 201)
		privatePath := fmt.Sprintf("/datasource-definitions/%d", int64(private["id"].(float64)))
		public := a.call("POST", privatePath+"/revisions", a.admin, map[string]any{"name": "公开定义", "visibility": "global", "config": cfg}, 201)
		visible := a.call("GET", fmt.Sprintf("/datasource-definitions/%d/revisions", int64(public["id"].(float64))), a.developer, nil, 200)
		raw, _ := json.Marshal(visible)
		if visible["total"] != float64(1) || strings.Contains(string(raw), "private-predecessor") || strings.Contains(string(raw), "previous_revision") {
			t.Fatal("private revision leaked through history or diff")
		}
		a.call("GET", privatePath+"/revisions", a.developer, nil, 404)
		own := a.call("POST", "/datasource-definitions", a.developer, map[string]any{"name": "个人修订", "config": cfg}, 201)
		ownPath := fmt.Sprintf("/datasource-definitions/%d/revisions", int64(own["id"].(float64)))
		a.call("GET", ownPath, a.admin, nil, 404)
		a.call("GET", ownPath, a.readonly, nil, 404)
		a.call("GET", ownPath, a.developer, nil, 200)
	})
	t.Run("stop-requires-current-impact-and-affects-only-that-revision", func(t *testing.T) {
		a.t = t
		a.call("PATCH", path+"/status", a.admin, map[string]any{"enabled": false}, 400)
		a.call("PATCH", path+"/status", a.admin, map[string]any{"enabled": false, "confirmed": true}, 400)
		a.call("PATCH", path+"/status", a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": 0}, 409)
		a.call("POST", path+"/connections", a.readonly, identity, 201)
		a.call("PATCH", path+"/status", a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": 1}, 409)
		a.call("PATCH", path+"/status", a.developer, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": 2}, 404)
		a.call("PATCH", path+"/status", a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": 2}, 200)
		a.call("POST", "/query/execute", a.developer, map[string]any{"connection_id": personalID, "sql": "SELECT 1"}, 400)
		a.call("POST", path+"/connections", a.admin, identity, 400)
		rows := a.call("GET", path+"/revisions", a.admin, nil, 200)["items"].([]any)
		if rows[0].(map[string]any)["enabled"] != true || rows[1].(map[string]any)["enabled"] != false {
			t.Fatal("stopping old revision affected another revision")
		}
		a.call("PATCH", path+"/status", a.admin, map[string]any{"enabled": true}, 200)
		a.call("POST", fmt.Sprintf("/connections/%d/test", personalID), a.developer, nil, 200)
	})
	t.Run("concurrent-bind-and-stop-do-not-miss-an-unconfirmed-user", func(t *testing.T) {
		a.t = t
		for i := 0; i < 6; i++ {
			source := create(fmt.Sprint("并发停用", i))
			p := fmt.Sprintf("/datasource-definitions/%d", int64(source["id"].(float64)))
			var wg sync.WaitGroup
			start := make(chan struct{})
			statuses := make([]int, 2)
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				r := executionRequest(a.server.Client(), a.server.URL, "POST", p+"/connections", a.developer, identity)
				statuses[0] = r.status
			}()
			go func() {
				defer wg.Done()
				<-start
				r := executionRequest(a.server.Client(), a.server.URL, "PATCH", p+"/status", a.admin, map[string]any{"enabled": false, "confirmed": true, "expected_binding_count": 0})
				statuses[1] = r.status
			}()
			close(start)
			wg.Wait()
			if !(statuses[0] == 201 && statuses[1] == 409 || statuses[0] == 400 && statuses[1] == 200) {
				t.Fatalf("unsafe bind/stop outcome: %v", statuses)
			}
		}
	})
}
