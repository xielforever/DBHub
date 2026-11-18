package server

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestBusinessIdentityDatasetMappings(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	const dataset = "/workspaces/1/business-datasets/orders"
	const identity = "/workspaces/1/business-identities/2"
	now := time.Now().UTC().Truncate(time.Second)
	authority := "验收数据目录"
	datasetInput := map[string]any{
		"expected_version": 0, "authority": authority, "source_version": "catalog-v1",
		"valid_from": now.Add(-time.Hour), "valid_until": now.Add(time.Hour),
		"account_id": f.account, "account_version": 1,
	}
	datasets := []map[string]any{{"key": "orders", "version": 1}}
	identityInput := map[string]any{
		"expected_version": 0, "authority": "验收员工目录", "source_version": "hr-v1",
		"business_key": "employee-42", "valid_from": now.Add(-time.Hour), "valid_until": now.Add(time.Hour),
		"datasets": datasets,
	}
	a.call("GET", dataset, "", nil, 401)
	a.call("PUT", dataset, a.developer, datasetInput, 403)
	a.call("GET", identity, a.readonly, nil, 403)
	a.call("GET", identity, a.admin, nil, 404)

	created := a.call("PUT", dataset, a.admin, datasetInput, 200)
	if created["version"] != float64(1) || created["definition_revision_id"] != float64(f.definition) ||
		created["available"] != true || created["authority"] != authority || created["password"] != nil {
		t.Fatalf("unexpected dataset metadata: %#v", created)
	}
	a.call("PUT", dataset, a.admin, datasetInput, 409)
	a.call("PUT", identity, a.admin, identityInput, 200)
	read := func(version int, identityAvailable, datasetAvailable bool) {
		t.Helper()
		result := a.call("GET", identity, a.admin, nil, 200)
		bindings := result["datasets"].([]any)
		if result["version"] != float64(version) || result["business_key"] != "employee-42" ||
			result["source_version"] != "hr-v1" || result["available"] != identityAvailable || len(bindings) != 1 {
			t.Fatalf("unexpected identity metadata: %#v", result)
		}
		binding := bindings[0].(map[string]any)
		if binding["key"] != "orders" || binding["available"] != datasetAvailable {
			t.Fatalf("unexpected dataset binding: %#v", binding)
		}
	}
	read(1, true, true)
	a.call("PUT", "/workspaces/1/business-identities/3", a.admin, identityInput, 409)
	a.call("PUT", "/workspaces/1/business-identities/90000", a.admin, identityInput, 404)
	a.call("PUT", identity, a.admin, identityInput, 409)
	identityInput["expected_version"] = 1
	identityInput["datasets"] = []map[string]any{{"key": "orders", "version": 1}, {"key": "missing", "version": 1}}
	a.call("PUT", identity, a.admin, identityInput, 409)
	read(1, true, true)
	identityInput["datasets"] = []map[string]any{datasets[0], datasets[0]}
	a.call("PUT", identity, a.admin, identityInput, 400)

	// Dataset and account revisions require explicit rebinding.
	datasetInput["expected_version"] = 1
	datasetInput["source_version"] = "catalog-v2"
	a.call("PUT", dataset, a.admin, datasetInput, 200)
	read(1, true, false)
	identityInput["datasets"] = datasets
	a.call("PUT", identity, a.admin, identityInput, 409)
	identityInput["datasets"] = []map[string]any{{"key": "orders", "version": 2}}
	a.call("PUT", identity, a.admin, identityInput, 200)
	read(2, true, true)
	a.exec(a.developer, "SELECT 1", 200)
	a.call("GET", "/workspaces/1/report-accounts", a.developer, nil, 403)

	a.call("PATCH", fmt.Sprintf("/workspaces/1/report-accounts/%d", f.account), a.admin,
		map[string]any{"request_id": testRequestID(), "version": 1, "enabled": false, "expected_reports": 0}, 200)
	read(2, true, false)
	if result := a.call("GET", dataset, a.admin, nil, 200); result["available"] != false {
		t.Fatalf("stopped execution account still resolves: %#v", result)
	}
	a.call("PATCH", fmt.Sprintf("/workspaces/1/report-accounts/%d", f.account), a.admin,
		map[string]any{"request_id": testRequestID(), "version": 2, "enabled": true}, 200)
	if result := a.call("GET", dataset, a.admin, nil, 200); result["available"] != false {
		t.Fatalf("account reactivation revived the previous version: %#v", result)
	}

	// Inactive membership and expiry are visible even when a mapping still exists.
	ctx := context.Background()
	for _, active := range []bool{false, true} {
		if _, err := a.pool.Exec(ctx, "UPDATE sys_workspace_members SET active=$1 WHERE workspace_id=1 AND user_id=2", active); err != nil {
			t.Fatal(err)
		}
		read(2, false, false)
	}
	identityInput["expected_version"] = 2
	identityInput["datasets"] = []map[string]any{}
	identityInput["valid_from"] = now.Add(-2 * time.Hour)
	identityInput["valid_until"] = now.Add(-time.Hour)
	if result := a.call("PUT", identity, a.admin, identityInput, 200); result["available"] != false ||
		result["version"] != float64(3) || len(result["datasets"].([]any)) != 0 {
		t.Fatalf("expected expired identity with cleared bindings: %#v", result)
	}

	// Even an administrator admitted to a second space cannot reuse its neighbor's
	// metadata, members, or managed account IDs.
	var workspace int64
	if err := a.pool.QueryRow(ctx, "INSERT INTO sys_workspaces(name) VALUES('P2 隔离验收') RETURNING id").Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, "INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES($1,1,'admin')", workspace); err != nil {
		t.Fatal(err)
	}
	other := fmt.Sprintf("/workspaces/%d", workspace)
	a.call("GET", other+"/business-identities/2", a.admin, nil, 404)
	a.call("PUT", other+"/business-identities/2", a.admin, identityInput, 404)
	a.call("GET", other+"/business-datasets/orders", a.admin, nil, 404)
	datasetInput["expected_version"] = 0
	datasetInput["account_version"] = 3
	a.call("PUT", other+"/business-datasets/orders", a.admin, datasetInput, 409)

	if err := a.deps.AuditJournal.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var actions int
	err := a.pool.QueryRow(ctx, `SELECT COUNT(DISTINCT action) FROM sys_audit_logs
	    WHERE workspace_id=1 AND status=1 AND action IN
	    ('BUSINESS_IDENTITY_GET','BUSINESS_IDENTITY_PUT','BUSINESS_DATASET_GET','BUSINESS_DATASET_PUT')`).Scan(&actions)
	if err != nil || actions != 4 {
		t.Fatalf("expected four business mapping audit actions, got %d: %v", actions, err)
	}
}
