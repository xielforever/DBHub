package server

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestBusinessOrganizationScopes(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	const base = "/workspaces/1/business-identities/2"
	const orders = base + "/datasets/orders/organization-scope"
	const stock = base + "/datasets/stock/organization-scope"
	now := time.Now().UTC().Truncate(time.Second)
	from, until := now.Add(-time.Hour), now.Add(time.Hour)
	dataset := map[string]any{
		"expected_version": 0, "authority": "验收数据目录", "source_version": "catalog-v1",
		"valid_from": from, "valid_until": until, "account_id": f.account, "account_version": 1,
	}
	for _, key := range []string{"orders", "stock"} {
		a.call("PUT", "/workspaces/1/business-datasets/"+key, a.admin, dataset, 200)
	}
	entries := []any{
		map[string]any{"department_key": "dept-a", "warehouse_key": "warehouse-1", "transport_org_key": "carrier-x"},
		map[string]any{"department_key": "dept-b", "warehouse_key": "warehouse-2", "transport_org_key": "carrier-y"},
	}
	scope := map[string]any{
		"expected_version": 0, "identity_version": 1, "dataset_version": 1,
		"authority": "验收组织目录", "source_version": "org-v1", "valid_from": from, "valid_until": until,
		"scopes": entries,
	}
	a.call("GET", orders, "", nil, 401)
	a.call("PUT", orders, a.readonly, scope, 403)
	a.call("GET", orders, a.admin, nil, 404)
	a.call("PUT", orders, a.admin, scope, 409)
	identity := map[string]any{
		"expected_version": 0, "authority": "验收员工目录", "source_version": "hr-v1",
		"business_key": "employee-42", "valid_from": from, "valid_until": until,
	}
	saveIdentity := func(expected, ordersVersion int) {
		t.Helper()
		identity["expected_version"] = expected
		identity["datasets"] = []any{map[string]any{"key": "orders", "version": ordersVersion}, map[string]any{"key": "stock", "version": 1}}
		a.call("PUT", base, a.admin, identity, 200)
	}
	saveIdentity(0, 1)
	a.call("PUT", orders, a.admin, scope, 200)
	read := func(path string, version int, stored, effective []any) {
		t.Helper()
		result := a.call("GET", path, a.admin, nil, 200)
		if result["version"] != float64(version) || result["authority"] != "验收组织目录" ||
			result["source_version"] != "org-v1" || !reflect.DeepEqual(result["scopes"], stored) ||
			!reflect.DeepEqual(result["effective_scopes"], effective) {
			t.Fatalf("unexpected organization scope at %s: %#v", path, result)
		}
	}
	empty := []any{}
	read(orders, 1, entries, entries)
	a.call("GET", stock, a.admin, nil, 404)
	a.call("GET", "/workspaces/1/business-identities/3/datasets/orders/organization-scope", a.admin, nil, 404)
	a.call("PUT", orders, a.admin, scope, 409)

	// A dimension omitted in a tuple stays omitted in both readbacks.
	departmentOnly := []any{map[string]any{"department_key": "dept-a"}}
	scope["scopes"] = departmentOnly
	a.call("PUT", stock, a.admin, scope, 200)
	read(stock, 1, departmentOnly, departmentOnly)
	read(orders, 1, entries, entries)

	scope["expected_version"] = 1
	scope["scopes"] = []any{entries[0], entries[0]}
	a.call("PUT", orders, a.admin, scope, 400)
	scope["scopes"] = []any{map[string]any{}}
	a.call("PUT", orders, a.admin, scope, 400)
	scope["scopes"] = entries
	read(orders, 1, entries, entries)
	a.exec(a.developer, "SELECT 1", 200)

	// An identity update and a dataset update each invalidate only the
	// effective ranges they versioned. Saved tuples remain inspectable.
	saveIdentity(1, 1)
	read(orders, 1, entries, empty)
	read(stock, 1, departmentOnly, empty)
	a.call("PUT", orders, a.admin, scope, 409)
	scope["identity_version"] = 2
	a.call("PUT", orders, a.admin, scope, 200)
	read(orders, 2, entries, entries)

	dataset["expected_version"] = 1
	a.call("PUT", "/workspaces/1/business-datasets/orders", a.admin, dataset, 200)
	read(orders, 2, entries, empty)
	scope["expected_version"] = 2
	a.call("PUT", orders, a.admin, scope, 409)
	saveIdentity(2, 2)
	scope["identity_version"] = 3
	scope["dataset_version"] = 2
	scope["valid_from"] = from.Add(-time.Hour)
	scope["valid_until"] = from
	a.call("PUT", orders, a.admin, scope, 200)
	read(orders, 3, entries, empty)
	scope["expected_version"] = 3
	scope["valid_from"], scope["valid_until"] = from, until
	a.call("PUT", orders, a.admin, scope, 200)
	read(orders, 4, entries, entries)

	// Disable the original execution account, then verify explicit withdrawal.
	a.call("PATCH", fmt.Sprintf("/workspaces/1/report-accounts/%d", f.account), a.admin,
		map[string]any{"request_id": testRequestID(), "version": 1, "enabled": false, "expected_reports": 0}, 200)
	read(orders, 4, entries, empty)
	scope["expected_version"], scope["scopes"] = 4, empty
	a.call("PUT", orders, a.admin, scope, 200)
	read(orders, 5, empty, empty)

	ctx := context.Background()
	var workspace int64
	if err := a.pool.QueryRow(ctx, "INSERT INTO sys_workspaces(name) VALUES('组织范围隔离验收') RETURNING id").Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, "INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES($1,1,'admin')", workspace); err != nil {
		t.Fatal(err)
	}
	other := fmt.Sprintf("/workspaces/%d/business-identities/2/datasets/orders/organization-scope", workspace)
	a.call("GET", other, a.admin, nil, 404)
	scope["expected_version"] = 0
	a.call("PUT", other, a.admin, scope, 409)

	if err := a.deps.AuditJournal.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var actions int
	err := a.pool.QueryRow(ctx, `SELECT COUNT(DISTINCT action) FROM sys_audit_logs
	    WHERE workspace_id=1 AND status=1 AND resource_name='2/orders'
	    AND action IN ('BUSINESS_ORGANIZATION_SCOPE_GET','BUSINESS_ORGANIZATION_SCOPE_PUT')`).Scan(&actions)
	if err != nil || actions != 2 {
		t.Fatalf("expected two scope audit actions, got %d: %v", actions, err)
	}
}
