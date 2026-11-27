package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func TestIAMUnknownTaskVersionRetainsReceiptWithoutClaimOrDelivery(t *testing.T) {
	f := setupManaged(t)
	a, ctx := f.a, context.Background()
	report := subscriptionMetricReport(t, f)
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	a.call("POST", "/report-subscriptions", a.readonly, map[string]any{"request_id": testRequestID(), "definition": map[string]any{"name": "任务版本边界", "report_id": report, "publication_no": 1, "interval_minutes": 5, "params": map[string]any{}, "enabled": true, "webhook": false}}, 200)
	if n, e := service.Subscriptions.GenerateDue(ctx); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	known, e := service.RunSubscription(ctx)
	if e != nil {
		t.Fatal("registered legacy task failed", e)
	}
	future := testRequestID()
	var delivery int64
	e = a.pool.QueryRow(ctx, `INSERT INTO sys_report_subscription_runs(id,workspace_id,user_id,subscription_id,subscription_version,member_version,report_id,publication_no,name,encrypted_definition,request_id,permission_bridge)
 SELECT $1::uuid,workspace_id,user_id,subscription_id,subscription_version,member_version,report_id,publication_no,name,'opaque future payload',$2::uuid,'future-test-bridge'
 FROM sys_report_subscription_runs WHERE id=$3::uuid RETURNING delivery_id`, future, testRequestID(), known.ID).Scan(&delivery)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = service.Subscriptions.Claim(ctx); !errors.Is(e, db.ErrNotFound) {
		t.Fatal("unknown subscription claimed", e)
	}
	var state string
	var untouched bool
	if e = a.pool.QueryRow(ctx, `SELECT status,worker_token IS NULL AND encrypted_snapshot='' FROM sys_report_subscription_runs WHERE id=$1::uuid`, future).Scan(&state, &untouched); e != nil || state != "queued" || !untouched {
		t.Fatal("future task mutated", state, untouched, e)
	}
	for _, statement := range []string{`UPDATE sys_report_subscription_runs SET status='running' WHERE id=$1::uuid`, `UPDATE sys_report_subscription_runs SET permission_bridge='legacy-frozen-v1' WHERE id=$1::uuid`} {
		_, e = a.pool.Exec(ctx, statement, future)
		var pg *pgconn.PgError
		if !errors.As(e, &pg) || pg.Code != "0A000" {
			t.Fatal("database accepted unsupported execution or relabel", e)
		}
	}
	rows := a.call("GET", "/report-deliveries", a.readonly, nil, 200)["items"].([]any)
	seen := false
	for _, value := range rows {
		row := value.(map[string]any)
		if row["id"] == future {
			seen = true
			if row["permission_supported"] != false || !strings.Contains(row["message"].(string), "已暂停") {
				t.Fatal("future receipt not explained", row)
			}
		}
	}
	if !seen {
		t.Fatal("future receipt was lost")
	}
	a.call("GET", fmt.Sprintf("/report-deliveries/%d", delivery), a.readonly, nil, 404)
	webhook := testRequestID()
	_, e = a.pool.Exec(ctx, `INSERT INTO sys_webhook_deliveries(id,workspace_id,user_id,run_id,event_type,resource_id,permission_bridge) SELECT $1::uuid,workspace_id,user_id,id,'report_ready',delivery_id::text,'future-test-bridge' FROM sys_report_subscription_runs WHERE id=$2::uuid`, webhook, known.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = service.Subscriptions.ClaimWebhook(ctx); !errors.Is(e, db.ErrNotFound) {
		t.Fatal("unknown webhook claimed", e)
	}
	if _, e = a.pool.Exec(ctx, `UPDATE sys_webhook_deliveries SET status='sending' WHERE id=$1::uuid`, webhook); e == nil {
		t.Fatal("old worker can send future webhook")
	}
	// A fresh child ID is not a way to reinterpret an unknown source task.
	source := testRequestID()
	_, e = a.pool.Exec(ctx, `INSERT INTO sys_source_tasks(id,workspace_id,actor_id,connection_id,connection_name,kind,status,request_id,request_hash,member_version,config_updated_at,permission_bridge)
 SELECT $1::uuid,1,2,c.id,'future source fixture','check','failed',$2::uuid,repeat('0',64),m.version,c.updated_at,'future-test-bridge'
 FROM sys_connections c JOIN sys_workspace_members m ON m.workspace_id=1 AND m.user_id=2 WHERE c.id=$3`, source, testRequestID(), a.connectionFor(a.developer))
	if e != nil {
		t.Fatal(e)
	}
	a.call("POST", fmt.Sprintf("/source-tasks/%s/retry", source), a.developer, map[string]any{"request_id": testRequestID()}, 409)
	_, e = a.pool.Exec(ctx, `INSERT INTO sys_source_tasks(id,workspace_id,actor_id,connection_id,connection_name,kind,request_id,request_hash,parent_id,member_version,config_updated_at)
 SELECT $1::uuid,workspace_id,actor_id,connection_id,connection_name,kind,$2::uuid,request_hash,id,member_version,config_updated_at FROM sys_source_tasks WHERE id=$3::uuid`, testRequestID(), testRequestID(), source)
	var pg *pgconn.PgError
	if !errors.As(e, &pg) || pg.Code != "0A000" {
		t.Fatal("old process retried an opaque task as a fresh legacy child", e)
	}
}
