package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/user/dbhub/internal/automation"
	"github.com/user/dbhub/internal/config"
)

func verifyDeliveryNotice(t *testing.T, a *apiTest, id int64, resource, token, other string) map[string]any {
	t.Helper()
	ctx := context.Background()
	var notice int64
	var created, expiry time.Time
	if err := a.pool.QueryRow(ctx, `SELECT id,created_at,link_expires_at FROM sys_notifications
		WHERE recipient_id=$1 AND resource_type=$2 ORDER BY id DESC LIMIT 1`, id, resource).Scan(&notice, &created, &expiry); err != nil {
		t.Fatal(err)
	}
	if lifetime := expiry.Sub(created); lifetime < 24*time.Hour || lifetime > 24*time.Hour+time.Second {
		t.Fatalf("unexpected notification lifetime: %s", lifetime)
	}
	path := fmt.Sprintf("/inbox/delivery/%d", notice)
	link := a.call("GET", path, token, nil, 200)
	if link["resource_type"] != resource || len(link) > 3 {
		t.Fatal("unexpected delivery reference")
	}
	a.call("GET", path, other, nil, 404)
	if _, err := a.pool.Exec(ctx, `UPDATE sys_notifications SET link_expires_at=statement_timestamp()-interval '1 second' WHERE id=$1`, notice); err != nil {
		t.Fatal(err)
	}
	a.call("GET", path, token, nil, 404)
	for _, raw := range a.call("GET", "/inbox", token, nil, 200)["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] == float64(notice) {
			if item["resource_id"] != float64(0) {
				t.Fatal("expired notification still exposes an entry")
			}
			return link
		}
	}
	t.Fatal("expired notification lost its status history")
	return nil
}

func verifyFormalRetention(t *testing.T, a *apiTest) {
	t.Helper()
	ctx := context.Background()
	link := verifyDeliveryNotice(t, a, 2, "formal_export_delivery", a.developer, a.readonly)
	id := int64(link["resource_id"].(float64))
	path := fmt.Sprintf("/report-exports/%d", id)
	secret := a.call("POST", path+"/credential", a.developer, nil, 200)["credential"]
	var hash string
	if err := a.pool.QueryRow(ctx, `UPDATE sys_report_export_files SET generated_at=statement_timestamp()-interval '25 hours',
		expires_at=statement_timestamp()-interval '1 hour' WHERE export_id=$1 RETURNING sha256`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	a.call("POST", path+"/redeem", a.developer, map[string]any{"credential": secret}, 403)
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_exports SET created_at=statement_timestamp()-interval '8 days',
		expires_at=statement_timestamp()-interval '1 day' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_export_credentials SET issued_at=statement_timestamp()-interval '11 minutes',
		expires_at=statement_timestamp()-interval '1 minute' WHERE export_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	for range 2 {
		if err := service.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var valid bool
	if err := a.pool.QueryRow(ctx, `SELECT f.encrypted_file='' AND f.payload_purged_at IS NOT NULL AND f.sha256=$2
		AND e.generation_preview='' AND e.preview_purged_at IS NOT NULL AND e.generation_request<>'' AND j.status='succeeded'
		AND NOT EXISTS(SELECT 1 FROM sys_report_export_credentials c WHERE c.export_id=$1)
		FROM sys_report_export_files f JOIN sys_report_exports e ON e.id=f.export_id
		JOIN sys_report_export_jobs j ON j.export_id=f.export_id WHERE f.export_id=$1`, id, hash).Scan(&valid); err != nil || !valid {
		t.Fatalf("expired export payload and durable receipts did not separate: %v", err)
	}
	t.Log("formal file and credential expired, cleanup retained the content hash and execution receipt")
}

func verifyAutomaticTeamRetention(t *testing.T, a *apiTest, service *automation.Service, plan int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_team_subscriptions SET decided_at=statement_timestamp()-interval '28 minutes',
		review_until=statement_timestamp()-interval '28 minutes'+interval '30 days',schedule_interval_minutes=NULL WHERE id=$1`, plan); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := service.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var currentSlot bool
	if err := a.pool.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(scheduled_for>statement_timestamp()-interval '5 minutes'),false)
		FROM sys_report_team_periods WHERE plan_id=$1 AND scheduled_for IS NOT NULL`, plan).Scan(&count, &currentSlot); err != nil || count != 1 || !currentSlot {
		t.Fatalf("automatic team slots should skip missed periods: count=%d current=%v %v", count, currentSlot, err)
	}
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_team_subscriptions SET schedule_checked_at=NULL WHERE id=$1`, plan); err != nil {
		t.Fatal(err)
	}
	if err := service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_report_team_periods WHERE plan_id=$1 AND scheduled_for IS NOT NULL`, plan).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replaying the cursor duplicated the automatic period: %d %v", count, err)
	}
	for range 2 {
		if _, err := service.RunTeamDelivery(ctx); err != nil {
			t.Fatal(err)
		}
	}
	link := verifyDeliveryNotice(t, a, 3, "team_report_delivery", a.readonly, a.developer)
	id := link["delivery_id"].(string)
	path := "/report-team-deliveries/" + id
	a.call("GET", path, a.readonly, nil, 200)
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_team_deliveries SET expires_at=statement_timestamp()-interval '1 second' WHERE id=$1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	a.call("GET", path, a.readonly, nil, 404)
	if err := service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var retained bool
	if err := a.pool.QueryRow(ctx, `SELECT status='succeeded' AND snapshot_sha256<>'' AND encrypted_snapshot='' AND
		payload_purged_at IS NOT NULL FROM sys_report_team_deliveries WHERE id=$1::uuid`, id).Scan(&retained); err != nil || !retained {
		t.Fatalf("team snapshot cleanup lost evidence or retained payload: %v", err)
	}
	t.Log("automatic team delivery skipped old slots, replay stayed idempotent, and notification expired independently from the original snapshot")
}
