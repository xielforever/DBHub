package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/businessops"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/db"
)

type lostOperationReply struct {
	businessops.Gateway
	t       *testing.T
	lose    atomic.Bool
	applies atomic.Int32
}

func (g *lostOperationReply) Health(ctx context.Context, c *db.Connection, password, installation string) error {
	e := g.Gateway.Health(ctx, c, password, installation)
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) {
			g.t.Log("health PG failure", pe.Code, pe.Message)
		} else {
			var target *businessops.TargetError
			if errors.As(e, &target) {
				g.t.Log("health target failure", target.Cause)
			} else {
				g.t.Log("health failure", e)
			}
		}
	}
	return e
}

func (g *lostOperationReply) Lookup(ctx context.Context, target *db.OperationTarget, x *db.OperationRequest) (*db.OperationReceipt, error) {
	out, e := g.Gateway.Lookup(ctx, target, x)
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) {
			g.t.Log("lookup PG failure", pe.Code, pe.Message)
		} else {
			g.t.Log("lookup failure", e)
		}
	}
	return out, e
}

func (g *lostOperationReply) Apply(ctx context.Context, target *db.OperationTarget, x *db.OperationRequest) (db.OperationReceipt, error) {
	g.applies.Add(1)
	receipt, e := g.Gateway.Apply(ctx, target, x)
	if e == nil && g.lose.Swap(false) {
		return receipt, &businessops.TargetError{Dispatched: true, Cause: fmt.Errorf("simulated reply loss after real target commit")}
	}
	return receipt, e
}
func TestControlledBusinessOperationsRealDatabase(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	ctx := context.Background()
	role := fmt.Sprintf("dbhub_ops_test_%d", time.Now().UnixNano())
	password := "Ops-Test-Password-83!"
	installation := testRequestID()
	secret := []byte("test-main-key-32-bytes-long-123456789")
	var exists bool
	if e := a.targetPool.QueryRow(ctx, `SELECT to_regnamespace('dbhub_operation_sandbox') IS NOT NULL OR to_regnamespace('dbhub_ops_api_v1') IS NOT NULL`).Scan(&exists); e != nil || exists {
		t.Fatal("operation test requires unused protocol namespaces", e)
	}
	if _, e := a.targetPool.Exec(ctx, businessops.ProtocolSQL); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, _ = a.targetPool.Exec(context.Background(), `DROP SCHEMA dbhub_ops_api_v1 CASCADE;DROP SCHEMA dbhub_operation_sandbox CASCADE;DROP OWNED BY `+pgx.Identifier{role}.Sanitize()+`;DROP ROLE `+pgx.Identifier{role}.Sanitize())
	})
	if _, e := a.targetPool.Exec(ctx, `INSERT INTO dbhub_operation_sandbox.installation VALUES(true,$1::uuid,$2,'test-protocol-v1')`, installation, businessops.SigningKey(secret)); e != nil {
		t.Fatal(e)
	}
	if _, e := a.targetPool.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '`+password+`';GRANT USAGE ON SCHEMA dbhub_ops_api_v1 TO `+pgx.Identifier{role}.Sanitize()+`;GRANT EXECUTE ON FUNCTION dbhub_ops_api_v1.dispatch_v1(text,text) TO `+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	if _, e := a.targetPool.Exec(ctx, `INSERT INTO dbhub_operation_sandbox.members VALUES(1,3,'MINE',80,1),(1,2,'PEER',300,1);INSERT INTO dbhub_operation_sandbox.stocks VALUES(1,3,'SKU-MINE',50,20,1);INSERT INTO dbhub_operation_sandbox.shipments VALUES(1,3,'SHP-MINE',0,1)`); e != nil {
		t.Fatal(e)
	}
	gateway := &lostOperationReply{Gateway: businessops.NewGateway(connectorruntime.New(), secret), t: t}
	a.deps.OperationsGateway = gateway
	// Rebuild the existing fixture router with the same real stores and signing key.
	replacement := httptest.NewServer(NewRouter(&config.Config{Env: "development", SecretKey: secret, AccessTTL: time.Hour, RefreshTTL: time.Hour}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps.UserRepo, a.deps))
	defer replacement.Close()
	original := a.server
	a.server = replacement
	defer func() { a.server = original }()
	base := "/business-operations"
	accountBody := map[string]any{"request_id": testRequestID(), "account": map[string]any{"name": "业务专用EXECUTE账号", "definition_revision_id": f.definition, "username": role, "installation_id": installation, "enabled": true}, "password": password}
	a.call("POST", base+"/accounts", a.readonly, accountBody, 403)
	created := a.call("POST", base+"/accounts", a.admin, accountBody, 200)
	account := int64(created["id"].(float64))
	a.call("POST", base+"/accounts", a.admin, accountBody, 200)
	accounts := a.call("GET", base+"/accounts", a.admin, nil, 200)
	if strings.Contains(fmt.Sprint(accounts), password) || strings.Contains(fmt.Sprint(accounts), "password") {
		t.Fatal("operation credential exposed")
	}
	a.call("GET", base+"/accounts", a.readonly, nil, 403)
	templates := map[string]int64{}
	for _, kind := range []string{"member_points_adjust", "member_coupon_issue", "warehouse_stock_adjust", "transport_status_advance"} {
		result := a.call("POST", base+"/templates", a.admin, map[string]any{"request_id": testRequestID(), "template": map[string]any{"account_id": account, "kind": kind, "user_ids": []int64{3}, "group_ids": []int64{}, "enabled": true}}, 200)
		templates[kind] = int64(result["id"].(float64))
	}
	if len(a.call("GET", base+"/templates", a.developer, nil, 200)["items"].([]any)) != 0 {
		t.Fatal("peer sees unassigned templates")
	}
	preview := func(kind, key string, amount, want int) map[string]any {
		return a.call("POST", base+"/previews", a.readonly, map[string]any{"template_id": templates[kind], "version": 1, "target_key": key, "amount": amount}, want)
	}
	preview("member_points_adjust", "PEER", 50, 409)
	preview("member_points_adjust", "MINE", 501, 400)
	preview("warehouse_stock_adjust", "SKU-MINE", -40, 409)
	makeRequest := func(kind, key string, amount int) int64 {
		p := preview(kind, key, amount, 200)
		body := map[string]any{"request_id": testRequestID(), "preview_id": p["id"], "reason": "独立核对后的演示业务调整"}
		x := a.call("POST", base+"/requests", a.readonly, body, 200)
		same := a.call("POST", base+"/requests", a.readonly, body, 200)
		if same["id"] != x["id"] {
			t.Fatal("submit retry duplicated operation")
		}
		return int64(x["id"].(float64))
	}
	path := func(id int64) string { return fmt.Sprintf("%s/requests/%d", base, id) }
	approve := func(id int64) {
		p := path(id)
		a.call("POST", p+"/validate", a.readonly, map[string]any{"version": 1}, 403)
		validation := a.call("POST", p+"/validate", a.admin, map[string]any{"version": 1}, 200)
		a.call("POST", p+"/decision", a.admin, map[string]any{"request_id": testRequestID(), "version": 1, "decision": "approved", "validation_id": validation["validation_id"], "confirmed": true, "comment": "已复核范围与变更前后值"}, 200)
	}
	execute := func(id int64, want int) map[string]any {
		return a.call("POST", path(id)+"/execute", a.readonly, map[string]any{"version": 2, "confirmed": true}, want)
	}
	first := makeRequest("member_points_adjust", "MINE", 50)
	a.call("GET", path(first), a.developer, nil, 404)
	execute(first, 409)
	approve(first)
	done := execute(first, 200)
	if done["status"] != "executed" {
		t.Fatal("real operation did not execute", done["status"])
	}
	execute(first, 200)
	var points, receipts int
	if e := a.targetPool.QueryRow(ctx, `SELECT points FROM dbhub_operation_sandbox.members WHERE owner_app_user_id=3 AND member_no='MINE'`).Scan(&points); e != nil || points != 130 {
		t.Fatal("points changed more than once", points, e)
	}
	if e := a.targetPool.QueryRow(ctx, `SELECT count(*) FROM dbhub_operation_sandbox.receipts`).Scan(&receipts); e != nil || receipts != 1 {
		t.Fatal("target receipt duplicate", receipts, e)
	}
	t.Log("real PG execute-only account: typed owner-bound preview, independent approval, exact once receipt, peer denial passed")
	// A delegated reviewer remains unable to approve their own operation.
	if e := a.deps.UserRepo.UpdateMembership(a.actor(), 3, "", nil, 1, []string{"operation_review"}); e != nil {
		t.Fatal(e)
	}
	self := makeRequest("member_points_adjust", "MINE", 1)
	a.call("POST", path(self)+"/validate", a.readonly, map[string]any{"version": 1}, 403)
	a.call("POST", path(self)+"/decision", a.readonly, map[string]any{"request_id": testRequestID(), "version": 1, "decision": "rejected", "comment": "cannot self decide"}, 403)
	a.call("POST", path(self)+"/decision", a.admin, map[string]any{"request_id": testRequestID(), "version": 1, "decision": "rejected", "comment": "independent rejection of self-review boundary fixture"}, 200)
	if e := a.deps.UserRepo.UpdateMembership(a.actor(), 3, "", nil, 2, []string{}); e != nil {
		t.Fatal(e)
	}
	expired := preview("member_points_adjust", "MINE", 1, 200)
	if _, e := a.pool.Exec(ctx, `UPDATE sys_operation_previews SET expires_at=now()-interval '1 second' WHERE id=$1::uuid`, expired["id"]); e != nil {
		t.Fatal(e)
	}
	a.call("POST", base+"/requests", a.readonly, map[string]any{"request_id": testRequestID(), "preview_id": expired["id"], "reason": "expired preview must not submit"}, 404)
	stale := makeRequest("member_points_adjust", "MINE", 10)
	approve(stale)
	if _, e := a.targetPool.Exec(ctx, `UPDATE dbhub_operation_sandbox.members SET points=points+1,version=version+1 WHERE owner_app_user_id=3 AND member_no='MINE'`); e != nil {
		t.Fatal(e)
	}
	execute(stale, 409)
	if a.call("GET", path(stale), a.readonly, nil, 200)["status"] != "stale" {
		t.Fatal("stale target was not rejected")
	}
	for _, item := range []struct {
		kind, key string
		amount    int
	}{{"member_coupon_issue", "MINE", 2}, {"warehouse_stock_adjust", "SKU-MINE", 20}, {"transport_status_advance", "SHP-MINE", 1}} {
		id := makeRequest(item.kind, item.key, item.amount)
		approve(id)
		if execute(id, 200)["status"] != "executed" {
			t.Fatal("typed operation failed", item.kind)
		}
	}
	unknown := makeRequest("member_points_adjust", "MINE", 7)
	approve(unknown)
	gateway.lose.Store(true)
	before := gateway.applies.Load()
	result := execute(unknown, 200)
	if result["status"] != "unknown" {
		t.Fatal("lost response was claimed successful", result["status"])
	}
	execute(unknown, 409)
	recovered := a.call("POST", path(unknown)+"/reconcile", a.readonly, map[string]any{}, 200)
	if recovered["status"] != "executed" || gateway.applies.Load() != before+1 {
		t.Fatal("reconciliation resent write or lost receipt")
	}
	t.Log("real target committed but reply lost: unknown state persisted; read-only lookup recovered receipt with no second apply")
	// A readonly member with only operation_review can decide a request without
	// receiving database credentials or report-review/source-management powers.
	delegated := a.call("POST", "/users", a.admin, map[string]any{"username": "operation_reviewer", "password": "TestPassword123!", "role": "readonly"}, 201)
	delegateID := int64(delegated["id"].(float64))
	if e := a.deps.UserRepo.UpdateMembership(a.actor(), delegateID, "", nil, 1, []string{"operation_review"}); e != nil {
		t.Fatal(e)
	}
	delegateToken := a.call("POST", "/auth/login", "", map[string]any{"username": "operation_reviewer", "password": "TestPassword123!"}, 200)["access_token"].(string)
	a.call("GET", base+"/accounts", delegateToken, nil, 403)
	a.call("GET", "/report-accounts", delegateToken, nil, 403)
	delegatedRequest := makeRequest("member_points_adjust", "MINE", 1)
	validated := a.call("POST", path(delegatedRequest)+"/validate", delegateToken, map[string]any{"version": 1}, 200)
	a.call("POST", path(delegatedRequest)+"/decision", delegateToken, map[string]any{"request_id": testRequestID(), "version": 1, "decision": "approved", "validation_id": validated["validation_id"], "confirmed": true, "comment": "独立业务操作审批职责"}, 200)
	if e := a.deps.UserRepo.UpdateMembership(a.actor(), delegateID, "", nil, 2, []string{}); e != nil {
		t.Fatal(e)
	}
	beforeRevocation := gateway.applies.Load()
	execute(delegatedRequest, 409)
	if gateway.applies.Load() != beforeRevocation {
		t.Fatal("revoked reviewer approval reached target")
	}
	t.Log("delegated operation reviewer: credential isolation, self-review denial, preview expiry and approval revocation passed")
	// Runtime PG credentials alone cannot read keys or mutate owned rows.
	for _, query := range []string{`SELECT * FROM dbhub_operation_sandbox.installation`, `UPDATE dbhub_operation_sandbox.members SET points=0`, `SELECT dbhub_ops_api_v1.dispatch_v1('{}','forged')`} {
		tx, e := a.targetPool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize())
		if e == nil {
			_, e = tx.Exec(ctx, query)
		}
		_ = tx.Rollback(ctx)
		if e == nil {
			t.Fatal("execute account bypassed protocol", query)
		}
	}
	revoked := makeRequest("member_points_adjust", "MINE", 1)
	approve(revoked)
	if _, e := a.pool.Exec(ctx, `UPDATE sys_operation_accounts SET enabled=false,version=version+1 WHERE id=$1`, account); e != nil {
		t.Fatal(e)
	}
	count := gateway.applies.Load()
	execute(revoked, 404)
	if gateway.applies.Load() != count {
		t.Fatal("disabled account reached write gateway")
	}
	var originalRows int
	if e := a.targetPool.QueryRow(ctx, "SELECT count(*) FROM public."+pgx.Identifier{a.table}.Sanitize()).Scan(&originalRows); e != nil || originalRows != 2 {
		t.Fatal("original business fixture changed", e)
	}
}
