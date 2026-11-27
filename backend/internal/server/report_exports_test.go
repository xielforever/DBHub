package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReportExportLifecycle(t *testing.T) {
	f := setupManaged(t)
	a := f.a
	if _, err := a.targetPool.Exec(context.Background(), "INSERT INTO public."+a.table+" SELECT n,1,'user'||n||'@example.test','synthetic' FROM generate_series(3,12002)n"); err != nil {
		t.Fatal(err)
	}
	group := a.call("POST", "/user-groups", a.admin, map[string]any{"name": "export audience", "member_ids": []int64{3}}, 200)
	gid := int64(group["id"].(float64))
	parameters := []any{map[string]any{"name": "page_size", "label": "每页条数", "type": "integer", "default": 100, "min": 1, "max": 20001}, map[string]any{"name": "offset", "label": "跳过条数", "type": "integer", "default": 0, "min": 0}, map[string]any{"name": "lower", "label": "最小编号", "type": "integer"}}
	policy := map[string]any{"enabled": true, "max_rows": 20000, "page_size_parameter": "page_size", "offset_parameter": "offset"}
	body := map[string]any{"request_id": testRequestID(), "name": "受控完整导出", "connection_id": a.connectionFor(a.developer), "sql": "SELECT id,email,9007199254740993::bigint AS exact_id,'=SUM(1,2)'::text AS literal FROM public." + f.view + " WHERE (:lower::integer IS NULL OR id>=:lower::integer) ORDER BY id LIMIT :page_size::integer OFFSET :offset::integer", "chart_type": "table", "chart_config": map[string]any{}, "parameters": parameters, "export_policy": policy}
	created := a.call("POST", "/managed-reports", a.developer, body, 200)
	id := int64(created["id"].(float64))
	review := f.submit(id, 1)
	validation := f.validate(review, []int64{1, 2}, []int64{gid})
	f.approve(review, validation)
	f.release(id, 0, review, "publish")
	path := fmt.Sprintf("/published-reports/%d", id)
	requestID := testRequestID()
	request := map[string]any{"request_id": requestID, "publication_no": 1, "format": "xlsx", "reason": "完整财务对账验证", "params": map[string]any{"page_size": 100, "offset": 50}}
	export := a.call("POST", path+"/exports", a.readonly, request, 200)
	eid := int64(export["id"].(float64))
	epath := fmt.Sprintf("/report-exports/%d", eid)
	if export["row_count"] != float64(12001) || export["status"] != "pending" || export["can_download"] != false {
		t.Fatal("export did not override only approved paging parameters", export)
	}
	a.call("POST", path+"/exports", a.readonly, request, 409)
	a.call("GET", epath, a.developer, nil, 404)
	a.call("GET", epath+"/download", a.readonly, nil, 403)
	a.call("POST", epath+"/review", a.readonly, map[string]any{"version": 1, "decision": "approved"}, 403)
	preview := a.call("GET", epath+"/preview", a.admin, nil, 200)
	if len(preview["result"].(map[string]any)["rows"].([]any)) != 20 || preview["result"].(map[string]any)["truncated"] != true {
		t.Fatal("approval preview is not a bounded sample")
	}
	var payload string
	if err := a.pool.QueryRow(context.Background(), `SELECT encrypted_payload FROM sys_report_exports WHERE id=$1`, eid).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "example.test") || strings.Contains(payload, "9007199254740993") || strings.HasPrefix(payload, "{") {
		t.Fatal("result snapshot stored in plaintext")
	}
	pendingMeta, err := a.deps.Conns.ManagedReports().Export(a.actor(), eid)
	if err != nil {
		t.Fatal(err)
	}
	approved := a.call("POST", epath+"/review", a.admin, map[string]any{"version": 1, "decision": "approved", "comment": "核对版本和数据范围后批准"}, 200)
	if approved["can_download"] != true {
		t.Fatal("approved snapshot unavailable")
	}
	if err = a.deps.Conns.ManagedReports().CheckExportDelivery(a.actor(), pendingMeta, true); err == nil {
		t.Fatal("delivery reused a stale pre-approval version")
	}
	approvedMeta, err := a.deps.Conns.ManagedReports().Export(a.actor(), eid)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.deps.Conns.ManagedReports().CheckExportDelivery(a.actor(), approvedMeta, false); err != nil {
		t.Fatal("current approved delivery refused", err)
	}
	download := func(token string, want int) []byte {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, a.server.URL+"/api/v1/workspaces/1"+epath+"/download", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := a.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("snapshot download got %d want %d: %s", response.StatusCode, want, string(raw))
		}
		if want == 200 && response.Header.Get("X-Report-Snapshot-SHA256") != export["snapshot_sha256"] {
			t.Fatal("download lost snapshot hash")
		}
		return raw
	}
	raw := download(a.readonly, 200)
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range archive.File {
		if file.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		reader, _ := file.Open()
		sheet, _ := io.ReadAll(reader)
		reader.Close()
		if bytes.Count(sheet, []byte("<row ")) != 12002 || !bytes.Contains(sheet, []byte("9007199254740993")) || bytes.Contains(sheet, []byte("<f>")) {
			t.Fatal("workbook is partial or changed exact data")
		}
		found = true
	}
	if !found {
		t.Fatal("workbook missing data sheet")
	}
	a.call("POST", epath+"/review", a.admin, map[string]any{"version": 1, "decision": "rejected", "comment": "stale review"}, 409)
	adminRequest := map[string]any{"request_id": testRequestID(), "publication_no": 1, "format": "json", "reason": "独立审批验证"}
	self := a.call("POST", path+"/exports", a.admin, adminRequest, 200)
	a.call("POST", fmt.Sprintf("/report-exports/%d/review", int64(self["id"].(float64))), a.admin, map[string]any{"version": 1, "decision": "approved"}, 403)
	var otherPayload string
	if err := a.pool.QueryRow(context.Background(), `SELECT encrypted_payload FROM sys_report_exports WHERE id=$1`, self["id"]).Scan(&otherPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(context.Background(), `UPDATE sys_report_exports SET encrypted_payload=$2 WHERE id=$1`, eid, otherPayload); err != nil {
		t.Fatal(err)
	}
	download(a.readonly, 409)
	if _, err := a.pool.Exec(context.Background(), `UPDATE sys_report_exports SET encrypted_payload=$2,expires_at=created_at+interval '1 millisecond' WHERE id=$1`, eid, payload); err != nil {
		t.Fatal(err)
	}
	var expiryTime, creationTime, databaseTime time.Time
	if err := a.pool.QueryRow(context.Background(), `SELECT expires_at,created_at,clock_timestamp() FROM sys_report_exports WHERE id=$1`, eid).Scan(&expiryTime, &creationTime, &databaseTime); err != nil {
		t.Fatal(err)
	}
	t.Logf("expiry boundary fixture: created=%s expires=%s database_now=%s application_now=%s", creationTime.UTC().Format(time.RFC3339Nano), expiryTime.UTC().Format(time.RFC3339Nano), databaseTime.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err = a.deps.Conns.ManagedReports().CheckExportDelivery(a.actor(), approvedMeta, false); err == nil {
		t.Fatal("delivery reused an approval read before file expiry")
	}
	download(a.readonly, 403)
	if _, err := a.pool.Exec(context.Background(), `UPDATE sys_report_exports SET expires_at=created_at+interval '7 days' WHERE id=$1`, eid); err != nil {
		t.Fatal(err)
	}
	// An approved paging parameter must still reveal a result beyond its bound.
	if _, err := a.targetPool.Exec(context.Background(), "INSERT INTO public."+a.table+" SELECT n,1,'user'||n||'@example.test','synthetic' FROM generate_series(12003,20003)n"); err != nil {
		t.Fatal(err)
	}
	a.call("POST", path+"/exports", a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 1, "format": "csv", "reason": "超过批准上限应拒绝"}, 413)
	a.call("PUT", fmt.Sprintf("/user-groups/%d", gid), a.admin, map[string]any{"name": "export audience", "member_ids": []int64{}, "expected_version": group["version"]}, 200)
	download(a.readonly, 403)
	list := a.call("GET", "/report-exports", a.readonly, nil, 200)
	encoded, _ := json.Marshal(list)
	if bytes.Contains(encoded, []byte("encrypted_payload")) || bytes.Contains(encoded, []byte("user3@example.test")) {
		t.Fatal("export list leaked snapshot rows")
	}
	a.call("POST", path+"/exports", a.readonly, map[string]any{"request_id": testRequestID(), "publication_no": 1, "format": "csv", "reason": "revoked"}, 404)
}
