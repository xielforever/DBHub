package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func TestAutomationInbox(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	repo := db.NewAutomationRepository(a.deps.UserRepo)
	id, _ := f.draft("SELECT id,email FROM public." + f.view)
	request := map[string]any{"request_id": testRequestID(), "version": 1}
	review := a.call("POST", fmt.Sprintf("/managed-reports/%d/submit", id), a.developer, request, 200)
	a.call("POST", fmt.Sprintf("/managed-reports/%d/submit", id), a.developer, request, 200)
	inbox := a.call("GET", "/inbox", a.admin, nil, 200)
	if inbox["unread"] != float64(1) || inbox["todo_count"] != float64(1) {
		t.Fatal("submission was not notified exactly once", inbox)
	}
	item := inbox["items"].([]any)[0].(map[string]any)
	if a.call("POST", "/inbox/read", a.readonly, map[string]any{"ids": []any{item["id"]}}, 200)["changed"] != float64(0) {
		t.Fatal("another recipient marked a notification read")
	}
	a.call("POST", "/inbox/read", a.admin, map[string]any{"ids": []any{item["id"]}}, 200)
	if a.call("GET", "/inbox/todos", a.admin, nil, 200)["total"] != float64(1) {
		t.Fatal("reading notice completed a review")
	}
	tx, e := a.pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(context.Background(), `SELECT dbhub_notify(1,2,'rolled-back','decision','回滚通知','不可见','report',$1)`, id); e != nil {
		t.Fatal(e)
	}
	_ = tx.Rollback(context.Background())
	if a.call("GET", "/inbox", a.developer, nil, 200)["total"] != float64(0) {
		t.Fatal("rollback leaked notification")
	}
	rid := int64(review["id"].(float64))
	f.approve(rid, f.validate(rid, []int64{3}, nil))
	f.release(id, 0, rid, "publish")
	if a.call("GET", "/inbox/todos", a.admin, nil, 200)["total"] != float64(0) {
		t.Fatal("decided review remained pending")
	}
	developerInbox := a.call("GET", "/inbox", a.developer, nil, 200)
	if developerInbox["total"] != float64(2) {
		t.Fatal("decision/release notices missing", developerInbox)
	}
	raw, _ := json.Marshal(developerInbox)
	for _, secret := range []string{f.password, f.username, "sql_text", "password", "host"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("notification leaked execution details", secret)
		}
	}
	cutoff := inbox["through_id"]
	if _, e = a.pool.Exec(context.Background(), `SELECT dbhub_notify(1,1,'later-notice','decision','稍后到达的通知','','report',$1)`, id); e != nil {
		t.Fatal(e)
	}
	a.call("POST", "/inbox/read", a.admin, map[string]any{"through_id": cutoff}, 200)
	if a.call("GET", "/inbox?state=unread", a.admin, nil, 200)["unread"] != float64(1) {
		t.Fatal("mark-all consumed a later event")
	}
	a.call("GET", "/inbox?page_size=101", a.admin, nil, 400)
	a.call("POST", "/inbox/read", a.admin, map[string]any{"through_id": -1}, 400)
	created := a.call("POST", "/workspaces", a.admin, map[string]any{"request_id": testRequestID(), "name": "通知隔离空间"}, 200)
	path := fmt.Sprintf("/workspaces/%d/inbox", int64(created["id"].(float64)))
	if a.call("GET", path, a.admin, nil, 200)["total"] != float64(0) {
		t.Fatal("inbox leaked across workspaces")
	}
	a.call("GET", path, a.readonly, nil, 404)
	if _, e = repo.Inbox(context.Background(), false, 20, 0); e == nil {
		t.Fatal("missing scope did not fail closed")
	}
}

func TestAutomationSourceTasks(t *testing.T) {
	a := setupAPI(t)
	repo := db.NewAutomationRepository(a.deps.UserRepo)
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	ctx, e := a.deps.UserRepo.BindWorkspace(a.actor(), 1)
	if e != nil {
		t.Fatal(e)
	}
	conn, e := a.deps.Conns.Get(ctx, a.connID)
	if e != nil {
		t.Fatal(e)
	}
	t.Run("validation-ownership-and-atomic-idempotency", func(t *testing.T) {
		a.t = t
		a.call("POST", "/source-tasks", a.readonly, map[string]any{"connection_id": a.connID, "kind": "check", "request_id": testRequestID()}, 404)
		a.call("POST", "/source-tasks", a.readonly, map[string]any{"connection_id": a.connectionFor(a.readonly), "kind": "collect", "request_id": testRequestID()}, 403)
		a.call("POST", "/source-tasks", a.admin, map[string]any{"connection_id": 0, "kind": "check", "request_id": testRequestID()}, 400)
		key := testRequestID()
		results := make(chan *db.SourceTask, 12)
		failures := make(chan error, 12)
		var wg sync.WaitGroup
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				task, err := repo.EnqueueSourceTask(ctx, conn, "check", key, "")
				results <- task
				failures <- err
			}()
		}
		wg.Wait()
		close(results)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		var jobID string
		for task := range results {
			if jobID != "" && jobID != task.ID {
				t.Fatal("duplicate enqueue")
			}
			jobID = task.ID
		}
		a.call("POST", "/source-tasks", a.admin, map[string]any{"connection_id": a.connID, "kind": "collect", "request_id": key}, 409)
		a.call("GET", "/source-tasks/"+jobID, a.readonly, nil, 404)
		a.call("POST", "/source-tasks/"+jobID+"/cancel", a.readonly, map[string]any{}, 404)
		cancelled := a.call("POST", "/source-tasks/"+jobID+"/cancel", a.admin, map[string]any{}, 200)
		if cancelled["status"] != "cancelled" {
			t.Fatal(cancelled)
		}
		a.call("POST", "/source-tasks/"+jobID+"/cancel", a.admin, map[string]any{}, 200)
		if _, err := service.RunOne(context.Background()); !errors.Is(err, db.ErrNotFound) {
			t.Fatal("cancelled job was admitted", err)
		}
		request := map[string]any{"request_id": testRequestID()}
		retry := a.call("POST", "/source-tasks/"+jobID+"/retry", a.admin, request, 202)
		again := a.call("POST", "/source-tasks/"+jobID+"/retry", a.admin, request, 202)
		if again["id"] != retry["id"] || retry["id"] == jobID {
			t.Fatal("retry lost idempotency or replaced history")
		}
		finished, err := service.RunOne(context.Background())
		if err != nil || finished.Status != "succeeded" {
			t.Fatal("real connection check failed", finished, err)
		}
		history := a.call("GET", "/source-check-history", a.admin, nil, 200)
		if history["total"] != float64(1) || !history["items"].([]any)[0].(map[string]any)["current_config"].(bool) {
			t.Fatal("check history/current status missing", history)
		}
		if a.call("GET", "/source-check-history", a.readonly, nil, 200)["total"] != float64(0) {
			t.Fatal("history leaked another user's connection")
		}
	})
	t.Run("real-collection-and-cancel-publication-fence", func(t *testing.T) {
		a.t = t
		a.call("POST", "/source-tasks", a.admin, map[string]any{"connection_id": a.connID, "kind": "collect", "request_id": testRequestID()}, 202)
		finished, err := service.RunOne(context.Background())
		if err != nil || finished.Status != "succeeded" {
			t.Fatal("real collection failed", finished, err)
		}
		var tables int64
		if err = a.pool.QueryRow(context.Background(), `SELECT count(*) FROM sys_meta_snapshots WHERE connection_id=$1`, a.connID).Scan(&tables); err != nil || tables < 1 {
			t.Fatal("collection did not publish", tables, err)
		}
		a.call("POST", "/source-tasks", a.admin, map[string]any{"connection_id": a.connID, "kind": "collect", "request_id": testRequestID()}, 202)
		claimed, err := repo.ClaimSourceTask(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		batch, err := a.deps.Meta.BeginSync(db.WithSourceTask(ctx, claimed), a.connID, conn.UpdatedAt)
		if err != nil {
			t.Fatal(err)
		}
		defer batch.Close()
		a.call("POST", "/source-tasks/"+claimed.ID+"/cancel", a.admin, map[string]any{}, 200)
		if err = batch.Publish(db.WithSourceTask(ctx, claimed), nil); !errors.Is(err, db.ErrAutomationConflict) {
			t.Fatal("cancelled worker could publish", err)
		}
		batch.Close()
		out, err := repo.FinishSourceTask(context.Background(), claimed, "failed", "测试取消", map[string]any{}, nil)
		if err != nil || out.Status != "cancelled" {
			t.Fatal(out, err)
		}
		var remaining int64
		_ = a.pool.QueryRow(context.Background(), `SELECT count(*) FROM sys_meta_snapshots WHERE connection_id=$1`, a.connID).Scan(&remaining)
		if remaining != tables {
			t.Fatal("cancelled publish removed old metadata")
		}
	})
	t.Run("expired-lease-config-and-membership-fences", func(t *testing.T) {
		a.t = t
		a.call("POST", "/source-tasks", a.admin, map[string]any{"connection_id": a.connID, "kind": "check", "request_id": testRequestID()}, 202)
		old, err := repo.ClaimSourceTask(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.pool.Exec(context.Background(), `UPDATE sys_source_tasks SET lease_until=now()-interval '1 second' WHERE id=$1::uuid`, old.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.ClaimSourceTask(context.Background()); !errors.Is(err, db.ErrNotFound) {
			t.Fatal(err)
		}
		if task := a.call("GET", "/source-tasks/"+old.ID, a.admin, nil, 200); task["status"] != "interrupted" {
			t.Fatal("expired lease was replayed", task)
		}
		if _, err = repo.FinishSourceTask(context.Background(), old, "succeeded", "迟到结果", map[string]any{}, nil); err != nil {
			t.Fatal(err)
		}
		if task := a.call("GET", "/source-tasks/"+old.ID, a.admin, nil, 200); task["status"] != "interrupted" {
			t.Fatal("late worker overwrote recovery")
		}
		for _, change := range []string{`UPDATE sys_connections SET updated_at=clock_timestamp() WHERE id=$1`, `UPDATE sys_workspace_members SET version=version+1 WHERE workspace_id=1 AND user_id=$1`} {
			a.call("POST", "/source-tasks", a.admin, map[string]any{"connection_id": a.connID, "kind": "check", "request_id": testRequestID()}, 202)
			arg := a.connID
			if strings.Contains(change, "sys_workspace_members") {
				arg = 1
			}
			if _, err = a.pool.Exec(context.Background(), change, arg); err != nil {
				t.Fatal(err)
			}
			out, err := service.RunOne(context.Background())
			if err != nil || out.Status != "stale" {
				t.Fatal("stale task was executed", out, err)
			}
		}
	})
	t.Run("scheduler-deduplication-and-stale-plan-pause", func(t *testing.T) {
		a.t = t
		body := map[string]any{"connection_id": a.connID, "kind": "check", "interval_minutes": 5, "enabled": true, "version": 0}
		plan := a.call("PUT", "/source-schedules", a.admin, body, 200)
		a.call("PUT", "/source-schedules", a.admin, body, 409)
		if _, err := a.pool.Exec(context.Background(), `UPDATE sys_source_schedules SET next_run_at=now()-interval '1 minute' WHERE id=$1`, plan["id"]); err != nil {
			t.Fatal(err)
		}
		if err := service.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := service.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM sys_source_tasks WHERE schedule_id=$1`, plan["id"]).Scan(&n); err != nil || n != 1 {
			t.Fatal("scheduler duplicated a tick", n, err)
		}
		if _, err := service.RunOne(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := a.pool.Exec(context.Background(), `UPDATE sys_connections SET updated_at=clock_timestamp() WHERE id=$1; UPDATE sys_source_schedules SET next_run_at=now()-interval '1 minute' WHERE id=$2`, a.connID, plan["id"]); err != nil {
			t.Fatal(err)
		}
		if err := service.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		plans := a.call("GET", "/source-schedules", a.admin, nil, 200)["items"].([]any)
		if len(plans) != 1 || plans[0].(map[string]any)["enabled"] != false {
			t.Fatal("stale schedule remained enabled", plans)
		}
	})
	t.Run("cross-process-two-slot-limit", func(t *testing.T) {
		a.t = t
		for _, body := range []map[string]any{{"connection_id": a.connID, "kind": "check", "request_id": testRequestID()}, {"connection_id": a.connID, "kind": "collect", "request_id": testRequestID()}, {"connection_id": a.redisID, "kind": "check", "request_id": testRequestID()}} {
			a.call("POST", "/source-tasks", a.admin, body, 202)
		}
		other := db.NewAutomationRepository(a.deps.UserRepo)
		first, err := repo.ClaimSourceTask(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		second, err := other.ClaimSourceTask(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = other.ClaimSourceTask(context.Background()); !errors.Is(err, db.ErrNotFound) {
			t.Fatal("third shared execution slot admitted", err)
		}
		for _, job := range []*db.SourceTask{first, second} {
			a.call("POST", "/source-tasks/"+job.ID+"/cancel", a.admin, map[string]any{}, 200)
			if _, err = repo.FinishSourceTask(context.Background(), job, "failed", "未启动目标操作", map[string]any{}, nil); err != nil {
				t.Fatal(err)
			}
		}
		third, err := service.RunOne(context.Background())
		if err != nil || third.Status != "succeeded" {
			t.Fatal("queued task did not recover a slot", third, err)
		}
	})
	t.Run("manual-check-history-and-scheduled-state-notifications", func(t *testing.T) {
		a.t = t
		before := a.call("GET", "/source-check-history", a.admin, nil, 200)["total"].(float64)
		a.call("POST", fmt.Sprintf("/connections/%d/test", a.connID), a.admin, nil, 200)
		if a.call("GET", "/source-check-history", a.admin, nil, 200)["total"] != before+1 {
			t.Fatal("manual check missing from history")
		}
		plan := a.call("GET", "/source-schedules", a.admin, nil, 200)["items"].([]any)[0].(map[string]any)
		a.call("PUT", "/source-schedules", a.admin, map[string]any{"connection_id": a.connID, "kind": "check", "interval_minutes": 5, "enabled": true, "version": plan["version"]}, 200)
		for _, status := range []string{"failed", "failed", "succeeded"} {
			if _, err := a.pool.Exec(context.Background(), `UPDATE sys_source_schedules SET next_run_at=now()-interval '1 minute' WHERE id=$1`, plan["id"]); err != nil {
				t.Fatal(err)
			}
			if err := service.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err := repo.ClaimSourceTask(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.FinishSourceTask(context.Background(), job, status, "通知状态转换样例", map[string]any{}, nil); err != nil {
				t.Fatal(err)
			}
		}
		var notices int
		if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM sys_notifications n JOIN sys_source_tasks t ON n.event_key='source-task:'||t.id||':'||t.status WHERE t.schedule_id=$1`, plan["id"]).Scan(&notices); err != nil || notices != 2 {
			t.Fatal("periodic success/failure repeated notifications", notices, err)
		}
	})
	t.Run("workspace-isolation-and-no-owner-secret-bypass", func(t *testing.T) {
		a.t = t
		job := a.call("POST", "/source-tasks", a.admin, map[string]any{"connection_id": a.connID, "kind": "check", "request_id": testRequestID()}, 202)
		created := a.call("POST", "/workspaces", a.admin, map[string]any{"request_id": testRequestID(), "name": "任务隔离空间"}, 200)
		base := fmt.Sprintf("/workspaces/%d", int64(created["id"].(float64)))
		a.call("GET", base+"/source-tasks/"+job["id"].(string), a.admin, nil, 404)
		if a.call("GET", base+"/source-tasks", a.admin, nil, 200)["total"] != float64(0) {
			t.Fatal("cross-workspace task leak")
		}
		a.call("POST", base+"/source-tasks", a.admin, map[string]any{"connection_id": a.connID, "kind": "check", "request_id": testRequestID()}, 404)
	})
}

func TestAutomationRetentionAndAlerts(t *testing.T) {
	a := setupAPI(t)
	repo := db.NewAutomationRepository(a.deps.UserRepo)
	ctx := context.Background()
	for _, outcome := range []string{"succeeded", "unknown", "in_progress"} {
		if _, e := a.pool.Exec(ctx, `INSERT INTO sys_audit_logs(event_id,action,workspace_id,created_at,context) VALUES($1::uuid,'RETENTION_PROBE',1,now()-interval '100 days',jsonb_build_object('outcome',$2))`, testRequestID(), outcome); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := a.pool.Exec(ctx, `INSERT INTO sys_query_history(connection_id,user_id,sql_text,created_at,workspace_id) VALUES($1,1,'old',now()-interval '100 days',1),($1,1,'recent',now(),1)`, a.connID); e != nil {
		t.Fatal(e)
	}
	if _, e := a.pool.Exec(ctx, `INSERT INTO sys_notifications(workspace_id,recipient_id,event_key,kind,title,resource_type,created_at,read_at) SELECT 1,1,'retention-'||i,'test','旧通知','report',now()-interval '100 days',now()-interval '100 days' FROM generate_series(1,501) i; INSERT INTO sys_notifications(workspace_id,recipient_id,event_key,kind,title,resource_type,created_at) VALUES(1,1,'unread-retention','test','旧未读','report',now()-interval '100 days')`); e != nil {
		t.Fatal(e)
	}
	counts, e := repo.PurgeRetention(ctx, config.OperationsConfig{})
	if e != nil || counts["audit"] != 0 || counts["query_history"] != 0 {
		t.Fatal("disabled retention deleted rows", counts, e)
	}
	_, _ = a.pool.Exec(ctx, `UPDATE sys_operations SET last_success_at=now()-interval '2 hours' WHERE name='retention'`)
	counts, e = repo.PurgeRetention(ctx, config.OperationsConfig{AuditRetentionDays: 30, HistoryRetentionDays: 7, ReadNotificationRetentionDays: 7})
	if e != nil || counts["audit"] != 1 || counts["query_history"] != 1 || counts["read_notifications"] != 500 {
		t.Fatal("retention bounds failed", counts, e)
	}
	var n int
	if e = a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_audit_logs WHERE action='RETENTION_PROBE'`).Scan(&n); e != nil || n != 2 {
		t.Fatal("unknown/in-progress audit removed", n, e)
	}
	if counts, e = repo.PurgeRetention(ctx, config.OperationsConfig{ReadNotificationRetentionDays: 7}); e != nil || counts != nil {
		t.Fatal("hourly guard failed", counts, e)
	}
	_, _ = a.pool.Exec(ctx, `UPDATE sys_operations SET last_success_at=now()-interval '2 hours' WHERE name='retention'`)
	if counts, e = repo.PurgeRetention(ctx, config.OperationsConfig{ReadNotificationRetentionDays: 7}); e != nil || counts["read_notifications"] != 1 {
		t.Fatal("remaining batch not processed", counts, e)
	}
	if e = a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_notifications WHERE event_key='unread-retention'`).Scan(&n); e != nil || n != 1 {
		t.Fatal("unread notification was purged")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.SetOperationalAlert(ctx, "backup_failed", true, "备份失败", "检查备份服务")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if e = a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_notifications WHERE kind='operations'`).Scan(&n); e != nil || n != 1 {
		t.Fatal("alert repeated for unchanged condition", n, e)
	}
	if e = repo.SetOperationalAlert(ctx, "backup_failed", false, "备份失败", ""); e != nil {
		t.Fatal(e)
	}
	if e = a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_notifications WHERE kind='operations'`).Scan(&n); e != nil || n != 2 {
		t.Fatal("recovery notice missing", n, e)
	}
	a.call("GET", "/platform/operations", a.admin, nil, 200)
	a.call("GET", "/platform/operations", a.developer, nil, 403)
	newAdmin := a.call("POST", "/users", a.admin, map[string]any{"username": "space_admin", "password": "TestPassword123!", "role": "admin"}, 201)
	if newAdmin["id"] == nil {
		t.Fatal("workspace admin missing")
	}
	token := a.call("POST", "/auth/login", "", map[string]any{"username": "space_admin", "password": "TestPassword123!"}, 200)["access_token"].(string)
	a.call("GET", "/platform/operations", token, nil, 403)
}

func TestAutomationTaskBudgets(t *testing.T) {
	a := setupAPI(t)
	ctx := context.Background()
	seed := func(workspace, actor, count int) {
		_, e := a.pool.Exec(ctx, `INSERT INTO sys_source_tasks(id,workspace_id,actor_id,connection_name,kind,request_id,request_hash,member_version,config_updated_at)
 SELECT md5($1||i::text)::uuid,$2,$3,'队列容量样例','check',md5('request:'||$1||i::text)::uuid,repeat('0',64),1,now() FROM generate_series(1,$4) i`, fmt.Sprintf("budget-%d-%d-", workspace, actor), workspace, actor, count)
		if e != nil {
			t.Fatal(e)
		}
	}
	request := func(want int) {
		a.call("POST", "/source-tasks", a.admin, map[string]any{"connection_id": a.connID, "kind": "check", "request_id": testRequestID()}, want)
	}
	seed(1, 1, 10)
	request(429)
	if _, e := a.pool.Exec(ctx, `DELETE FROM sys_source_tasks`); e != nil {
		t.Fatal(e)
	}
	seed(1, 2, 100)
	request(429)
	if _, e := a.pool.Exec(ctx, `DELETE FROM sys_source_tasks;INSERT INTO sys_workspaces(id,name) VALUES(2,'容量隔离样例');INSERT INTO sys_workspace_members(workspace_id,user_id,role_code)VALUES(2,2,'developer')`); e != nil {
		t.Fatal(e)
	}
	seed(2, 2, 200)
	request(429)
	if _, e := a.pool.Exec(ctx, `DELETE FROM sys_source_tasks`); e != nil {
		t.Fatal(e)
	}
	request(202)
}
