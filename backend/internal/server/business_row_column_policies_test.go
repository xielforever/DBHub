package server

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/db"
)

func TestBusinessRowColumnPolicies(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := context.Background()
	view := a.table + "_p2_rules"
	viewName := pgx.Identifier{"public", view}.Sanitize()
	if _, err := a.targetPool.Exec(ctx, "CREATE VIEW "+viewName+` AS
		SELECT row_id, department, warehouse, carrier, label, private_note
		FROM (VALUES
		 (1,'dept-a','wh-1','carrier-x','retained','internal',true),
		 (2,'dept-b','wh-2','carrier-y','denied','internal',true),
		 (3,'dept-a','wh-2','carrier-y','cross combination','internal',true),
		 (4,'dept-c','wh-3','carrier-z','outside ceiling','internal',true),
		 (5,'dept-a',NULL,NULL,'department only','internal',true),
		 (6,'dept-a','wh-1',NULL,'non-null warehouse','internal',true),
		 (7,'dept-d','wh-4','carrier-d','not bound to identity','internal',true),
		 (8,'dept-a','wh-1','carrier-x','outside view ceiling','internal',false),
		 (9,'dept-A','wh-1','carrier-x','different business key','internal',true)
		) AS fixture(row_id,department,warehouse,carrier,label,private_note,approved)
		WHERE approved`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := a.targetPool.Exec(ctx, "DROP VIEW "+viewName); err != nil {
			t.Error(err)
		}
	})
	if _, err := a.targetPool.Exec(ctx, "GRANT SELECT ON "+viewName+" TO "+pgx.Identifier{f.username}.Sanitize()); err != nil {
		t.Fatal(err)
	}

	const identity = "/workspaces/1/business-identities/2"
	const scope = identity + "/datasets/orders/organization-scope"
	const planPath = identity + "/datasets/orders/row-column-plan"
	const policy = "/workspaces/1/business-datasets/orders/row-column-policy"
	a.call("POST", "/workspaces/1/published-reports/1/policy-verification", a.admin, map[string]any{}, 404)
	now := time.Now().UTC().Truncate(time.Second)
	from, until := now.Add(-time.Hour), now.Add(time.Hour)
	src := db.BusinessMappingSource{Authority: "验收业务目录", SourceVersion: "v1", ValidFrom: from, ValidUntil: until}
	initial := int64(0)
	dataset := db.BusinessDatasetInput{BusinessMappingSource: src, ExpectedVersion: &initial, AccountID: f.account, AccountVersion: 1}
	a.call("PUT", "/workspaces/1/business-datasets/orders", a.admin, dataset, 200)
	a.call("PUT", identity, a.admin, db.BusinessIdentityInput{
		BusinessMappingSource: src, ExpectedVersion: &initial, BusinessKey: "employee-42",
		Datasets: []db.BusinessDatasetVersion{{Key: "orders", Version: 1}},
	}, 200)
	deptA := db.OrganizationScopeEntry{DepartmentKey: "dept-a", WarehouseKey: "wh-1", TransportOrgKey: "carrier-x"}
	deptB := db.OrganizationScopeEntry{DepartmentKey: "dept-b", WarehouseKey: "wh-2", TransportOrgKey: "carrier-y"}
	deptC := db.OrganizationScopeEntry{DepartmentKey: "dept-c", WarehouseKey: "wh-3", TransportOrgKey: "carrier-z"}
	deptD := db.OrganizationScopeEntry{DepartmentKey: "dept-d", WarehouseKey: "wh-4", TransportOrgKey: "carrier-d"}
	onlyDepartment := db.OrganizationScopeEntry{DepartmentKey: "dept-a"}
	scopeInput := db.OrganizationScopeInput{
		BusinessMappingSource: src, ExpectedVersion: &initial, IdentityVersion: 1, DatasetVersion: 1,
		Scopes: []db.OrganizationScopeEntry{deptA, deptB, deptC, onlyDepartment},
	}
	a.call("PUT", scope, a.admin, scopeInput, 200)
	definition := db.BusinessRowColumnDefinition{
		Schema: "public", View: view,
		Dimensions:     db.OrganizationDimensionColumns{Department: "department", Warehouse: "warehouse", Transport: "carrier"},
		AllowedScopes:  []db.OrganizationScopeEntry{deptA, deptB, deptD, onlyDepartment},
		DeniedScopes:   []db.OrganizationScopeEntry{deptB},
		AllowedColumns: []string{"row_id", "label", "private_note"},
		DeniedColumns:  []string{"private_note"},
	}
	policyInput := db.BusinessRowColumnPolicyInput{
		BusinessMappingSource: src, ExpectedVersion: &initial, DatasetVersion: 1, Definition: definition,
	}
	planInput := db.BusinessReadPlanInput{ScopeVersion: 1, PolicyVersion: 1, Columns: []string{"row_id", "label"}}
	a.call("GET", policy, "", nil, 401)
	a.call("PUT", policy, a.developer, policyInput, 403)
	a.call("POST", planPath, a.admin, planInput, 403)
	badPolicy := policyInput
	badPolicy.Definition.View = "invalid.view"
	a.call("PUT", policy, a.admin, badPolicy, 400)
	badPolicy = policyInput
	badPolicy.Definition.Dimensions.Transport = ""
	a.call("PUT", policy, a.admin, badPolicy, 400)
	a.call("PUT", policy, a.admin, policyInput, 200)
	a.call("PUT", policy, a.admin, policyInput, 409)
	if saved := a.call("GET", policy, a.admin, nil, 200); saved["version"] != float64(1) || saved["dataset_version"] != float64(1) {
		t.Fatalf("unexpected policy version: %#v", saved)
	}
	a.call("POST", planPath, a.readonly, planInput, 403)
	summary := a.call("POST", planPath, a.admin, planInput, 200)
	if summary["scope_count"] != float64(2) || !reflect.DeepEqual(summary["columns"], []any{"row_id", "label"}) ||
		summary["statement"] != nil || summary["args"] != nil {
		t.Fatalf("unexpected plan summary: %#v", summary)
	}
	for _, column := range []string{"private_note", "unregistered"} {
		rejected := planInput
		rejected.Columns = []string{column}
		a.call("POST", planPath, a.admin, rejected, 403)
	}
	stale := planInput
	stale.PolicyVersion = 2
	a.call("POST", planPath, a.admin, stale, 409)

	// Execute the same generated statement on the real target as the limited
	// report account. The validated path works with an approved view and binds
	// organization keys instead of interpolating them.
	plan, err := a.deps.UserRepo.CompileBusinessReadPlan(a.actor(), 2, "orders", planInput)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.User, cfg.Password = f.username, f.password
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	target, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close(ctx)
	rows, err := target.Query(ctx, plan.Statement, plan.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := rows.FieldDescriptions()
	if len(columns) != 2 || columns[0].Name != "row_id" || columns[1].Name != "label" {
		t.Fatalf("unexpected target projection: %#v", columns)
	}
	var ids []int
	for rows.Next() {
		var id int
		var label string
		if err = rows.Scan(&id, &label); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Ints(ids)
	if !reflect.DeepEqual(ids, []int{1, 5}) {
		t.Fatalf("expected only approved tuples and SQL NULL match, got %v", ids)
	}
	a.exec(a.developer, "SELECT 1", 200)

	versionOne := int64(1)
	scopeInput.ExpectedVersion = &versionOne
	scopeInput.Scopes = []db.OrganizationScopeEntry{}
	a.call("PUT", scope, a.admin, scopeInput, 200)
	a.call("POST", planPath, a.admin, planInput, 409)
	planInput.ScopeVersion = 2
	a.call("POST", planPath, a.admin, planInput, 403)

	var other int64
	if err = a.pool.QueryRow(ctx, "INSERT INTO sys_workspaces(name) VALUES('行列条件隔离验收') RETURNING id").Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pool.Exec(ctx, "INSERT INTO sys_workspace_members(workspace_id,user_id,role_code) VALUES($1,1,'admin')", other); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("/workspaces/%d", other)
	a.call("GET", prefix+"/business-datasets/orders/row-column-policy", a.admin, nil, 404)
	a.call("POST", prefix+"/business-identities/2/datasets/orders/row-column-plan", a.admin, planInput, 403)
	if err = a.deps.AuditJournal.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	err = a.pool.QueryRow(ctx, `SELECT COUNT(DISTINCT action) FROM sys_audit_logs
	    WHERE workspace_id=1 AND status=1 AND action IN
	    ('BUSINESS_ROW_COLUMN_POLICY_GET','BUSINESS_ROW_COLUMN_POLICY_PUT','BUSINESS_ROW_COLUMN_PLAN')`).Scan(&count)
	if err != nil || count != 3 {
		t.Fatalf("expected three policy audit actions, got %d: %v", count, err)
	}
}
