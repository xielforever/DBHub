package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func verifyFormalPickup(t *testing.T, a *apiTest, report int64, reviewer string) func() {
	t.Helper()
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, `INSERT INTO sys_iam_isolated_formal_exports VALUES (1,$1)`, report); err != nil {
		t.Fatal(err)
	}
	request := a.call("POST", fmt.Sprintf("/published-reports/%d/exports", report), a.developer,
		map[string]any{"request_id": testRequestID(), "publication_no": 1, "format": "csv", "reason": "一次领取验证"}, 200)
	id := int64(request["id"].(float64))
	path := fmt.Sprintf("/workspaces/1/report-exports/%d", id)
	a.call("POST", path+"/credential", a.developer, nil, 403)
	a.call("POST", path+"/review", reviewer, map[string]any{"version": 1, "decision": "approved"}, 200)
	service := NewAutomation(&config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), a.deps)
	if job, err := service.RunFormalExport(ctx); err != nil || job == nil || job.ExportID != id {
		t.Fatalf("formal artifact generation failed: %v", err)
	}
	a.call("POST", path+"/credential", reviewer, nil, 403)
	a.call("GET", path+"/download", a.developer, nil, 403)
	issue := func(issuePath string) string {
		t.Helper()
		out := a.call("POST", issuePath+"/credential", a.developer, nil, 200)
		exp, err := time.Parse(time.RFC3339Nano, out["expires_at"].(string))
		if err != nil || time.Until(exp) <= 0 || time.Until(exp) > 10*time.Minute {
			t.Fatalf("credential had invalid lifetime: %v", err)
		}
		return out["credential"].(string)
	}
	credential := issue(fmt.Sprintf("/report-exports/%d", id))
	otherSession := a.call("POST", "/auth/login", "", map[string]any{
		"username": "developer", "password": "TestPassword123!"}, 200)["access_token"].(string)
	a.call("POST", path+"/redeem", otherSession, map[string]string{"credential": credential}, 403)
	a.call("POST", path+"/redeem", reviewer, map[string]string{"credential": credential}, 403)

	type outcome struct {
		status int
		hash   string
		data   []byte
		err    error
	}
	try := func() outcome {
		input, _ := json.Marshal(map[string]string{"credential": credential})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.server.URL+"/api/v1"+path+"/redeem", bytes.NewReader(input))
		if err != nil {
			return outcome{err: err}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+a.developer)
		resp, err := a.server.Client().Do(req)
		if err != nil {
			return outcome{err: err}
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		return outcome{status: resp.StatusCode, hash: resp.Header.Get("X-Report-File-SHA256"), data: data, err: err}
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() { results <- try() }()
	}
	counts := map[int]int{}
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		counts[result.status]++
		if result.status == 200 {
			digest := sha256.Sum256(result.data)
			if result.hash != hex.EncodeToString(digest[:]) || !strings.Contains(string(result.data), "\r\n11,red,,") ||
				strings.Contains(string(result.data), "synthetic") || strings.Contains(string(result.data), "\r\n99,red,") {
				t.Fatal("credential did not redeem exactly the original masked file")
			}
		}
	}
	if counts[200] != 1 || counts[403] != 1 {
		t.Fatalf("credential was not atomically consumed: %#v", counts)
	}
	expired := issue(path)
	if _, err := a.pool.Exec(ctx, `UPDATE sys_report_export_credentials SET issued_at=statement_timestamp()-interval '11 minutes',
		expires_at=statement_timestamp()-interval '1 minute' WHERE export_id=$1 AND redeemed_at IS NULL`, id); err != nil {
		t.Fatal(err)
	}
	a.call("POST", path+"/redeem", a.developer, map[string]string{"credential": expired}, 403)
	stale := issue(path)
	t.Log("formal file could be redeemed once, by the original authenticated session, with bytes matching the stored hash")
	return func() {
		t.Helper()
		now := time.Now().UTC()
		expected := int64(1)
		a.call("PUT", "/workspaces/1/business-identities/2/datasets/dash-a/organization-scope", a.admin,
			db.OrganizationScopeInput{BusinessMappingSource: db.BusinessMappingSource{Authority: "正式文件撤权验收", SourceVersion: "v2",
				ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}, ExpectedVersion: &expected,
				IdentityVersion: 1, DatasetVersion: 1, Scopes: []db.OrganizationScopeEntry{}}, 200)
		a.call("POST", path+"/redeem", a.developer, map[string]string{"credential": stale}, 403)
	}
}
