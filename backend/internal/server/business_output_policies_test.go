package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/db"
)

func TestBusinessOutputPoliciesAndReveal(t *testing.T) {
	const isolation = "p2-business-output-isolated"
	a := setupConfiguredAPI(t, "", func(cfg *config.Config, _ string) {
		cfg.IAMIsolationID, cfg.Phase2ReportVerification = isolation, true
	})
	f := setupManagedAPI(t, a)
	ctx := context.Background()
	view := a.table + "_p2_output"
	qualifiedView := pgx.Identifier{"public", view}.Sanitize()
	gate := pgx.Identifier{"public", a.table + "_p2_gate"}.Sanitize()
	gateID := time.Now().UnixNano()
	if _, err := a.targetPool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION %s(value text) RETURNS text
		LANGUAGE plpgsql VOLATILE AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN value; END; $$`, gate, gateID)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := a.targetPool.Exec(ctx, "DROP FUNCTION "+gate+"(text)"); err != nil {
			t.Error(err)
		}
	})
	if _, err := a.targetPool.Exec(ctx, "CREATE VIEW "+qualifiedView+` AS
		SELECT row_id, revision, department, title, `+gate+`(note) AS note, masked_input, derived_note, private_key, confidential_note, hidden_note
		FROM (VALUES
		 (1,'v1','dept-a','visible','sample-one','legacy-hidden','derived-a','secret-a','high-a','hidden-a'),
		 (1,'v2','dept-a','visible','sample-previous','legacy-hidden','derived-b','secret-b','high-b','hidden-b'),
		 (2,'v1','dept-b','other','sample-outside','legacy-hidden','derived-c','secret-c','high-c','hidden-c')
		) AS fixture(row_id, revision, department, title, note, masked_input, derived_note, private_key, confidential_note, hidden_note)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := a.targetPool.Exec(ctx, "DROP VIEW "+qualifiedView); err != nil {
			t.Error(err)
		}
	})
	if _, err := a.targetPool.Exec(ctx, "GRANT SELECT ON "+qualifiedView+" TO "+pgx.Identifier{f.username}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	createAdmin := func(name string) (int64, string) {
		t.Helper()
		const syntheticPassword = "TestPassword123!"
		user := a.call("POST", "/users", a.admin, map[string]any{"username": name, "password": syntheticPassword, "role": "admin"}, 201)
		session := a.call("POST", "/auth/login", "", map[string]any{"username": name, "password": syntheticPassword}, 200)
		return int64(user["id"].(float64)), session["access_token"].(string)
	}
	reviewerID, reviewer := createAdmin("p2_designated_reviewer")
	otherID, other := createAdmin("p2_unlisted_admin")
	const dataset = "/workspaces/1/business-datasets/orders"
	const output = dataset + "/output-policy"
	const planPath = "/workspaces/1/business-identities/1/datasets/orders/output-plan"
	now := time.Now().UTC().Truncate(time.Second)
	source := db.BusinessMappingSource{Authority: "验收目录", SourceVersion: "v1", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}
	zero, one := int64(0), int64(1)
	a.call("PUT", dataset, a.admin, db.BusinessDatasetInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, AccountID: f.account, AccountVersion: 1,
	}, 200)
	deptA := db.OrganizationScopeEntry{DepartmentKey: "dept-a"}
	deptB := db.OrganizationScopeEntry{DepartmentKey: "dept-b"}
	registerIdentity := func(userID int64, scope db.OrganizationScopeEntry) string {
		t.Helper()
		base := fmt.Sprintf("/workspaces/1/business-identities/%d", userID)
		a.call("PUT", base, a.admin, db.BusinessIdentityInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, BusinessKey: fmt.Sprintf("employee-%d", userID),
			Datasets: []db.BusinessDatasetVersion{{Key: "orders", Version: 1}},
		}, 200)
		path := base + "/datasets/orders/organization-scope"
		a.call("PUT", path, a.admin, db.OrganizationScopeInput{
			BusinessMappingSource: source, ExpectedVersion: &zero, IdentityVersion: 1, DatasetVersion: 1,
			Scopes: []db.OrganizationScopeEntry{scope},
		}, 200)
		return path
	}
	registerIdentity(1, deptA)
	reviewerScope := registerIdentity(reviewerID, deptB)
	registerIdentity(otherID, deptA)
	approvedColumns := []string{"row_id", "title", "note", "derived_note", "private_key", "confidential_note", "hidden_note"}
	projection := make([]string, 0, len(approvedColumns))
	for _, col := range approvedColumns {
		projection = append(projection, pgx.Identifier{col}.Sanitize())
	}
	reportID, _ := f.draft("SELECT " + strings.Join(projection, ", ") + " FROM " + qualifiedView)
	review := f.submit(reportID, 1)
	f.approve(review, f.validate(review, []int64{1, reviewerID}, nil))
	f.release(reportID, 0, review, "publish")
	verifiedPath := fmt.Sprintf("/workspaces/1/published-reports/%d/policy-verification", reportID)
	a.call("PUT", dataset+"/row-column-policy", a.admin, db.BusinessRowColumnPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1,
		Definition: db.BusinessRowColumnDefinition{
			Schema: "public", View: view, Dimensions: db.OrganizationDimensionColumns{Department: "department"},
			AllowedScopes:  []db.OrganizationScopeEntry{deptA, deptB},
			AllowedColumns: approvedColumns,
		},
	}, 200)
	fields := map[string]db.BusinessFieldClassification{
		"row_id": {Classification: "internal"}, "revision": {Classification: "internal"}, "title": {Classification: "internal", LegacyLabel: "normal"},
		"note": {Classification: "sensitive", LegacyLabel: "sensitive"}, "masked_input": {Classification: "sensitive", LegacyProtection: "masked"},
		"derived_note": {Classification: "sensitive", DependsOn: []string{"masked_input"}},
		"private_key":  {Classification: "credential_secret"}, "hidden_note": {Classification: "internal", LegacyProtection: "hidden"},
	}
	classification := db.BusinessOutputPolicyInput{
		BusinessMappingSource: source, ExpectedVersion: &zero, DatasetVersion: 1, RowColumnPolicyVersion: 1,
		BusinessOutputDefinition: db.BusinessOutputDefinition{Fields: fields, RecordKeyColumn: "row_id",
			RecordVersionColumn: "revision", ReviewerIDs: []int64{1, reviewerID}, ReportID: reportID, PublicationNo: 1},
	}
	defaultPlan := db.BusinessOutputPlanInput{
		BusinessReadPlanInput: db.BusinessReadPlanInput{ScopeVersion: 1, PolicyVersion: 1, Columns: []string{"title", "note", "derived_note", "confidential_note"}},
		OutputPolicyVersion:   1,
	}
	a.call("POST", planPath, a.admin, defaultPlan, 403)
	a.call("PUT", output, a.admin, classification, 400)
	fields["confidential_note"] = db.BusinessFieldClassification{Classification: "high_sensitive", LegacyLabel: "confidential"}
	a.call("PUT", output, a.admin, classification, 400)
	fields["confidential_note"] = db.BusinessFieldClassification{Classification: "high_sensitive", LegacyLabel: "confidential", ReviewReference: "review-v1"}
	fields["derived_note"] = db.BusinessFieldClassification{Classification: "internal", DependsOn: []string{"masked_input"}}
	a.call("PUT", output, a.admin, classification, 400)
	fields["derived_note"] = db.BusinessFieldClassification{Classification: "sensitive", DependsOn: []string{"derived_note"}}
	a.call("PUT", output, a.admin, classification, 400)
	fields["derived_note"] = db.BusinessFieldClassification{Classification: "sensitive", DependsOn: []string{"masked_input"}}
	a.call("PUT", output, a.admin, classification, 200)
	a.call("GET", output, a.admin, nil, 200)
	got := a.call("POST", planPath, a.admin, defaultPlan, 200)
	if !reflect.DeepEqual(got["masked_columns"], []any{"note", "derived_note", "confidential_note"}) || got["statement"] != nil || got["args"] != nil {
		t.Fatalf("unexpected default output summary: %#v", got)
	}
	type verificationBody struct {
		db.BusinessOutputPlanInput
		DatasetKey    string `json:"dataset_key"`
		PublicationNo int    `json:"publication_no"`
	}
	verify := func(in db.BusinessOutputPlanInput, actor string, pub int, want int) map[string]any {
		t.Helper()
		return a.call("POST", verifiedPath, actor, verificationBody{in, "orders", pub}, want)
	}
	verify(defaultPlan, a.admin, 1, 403) // An environment flag alone is insufficient.
	if _, err := a.pool.Exec(ctx, "INSERT INTO sys_iam_isolation_guard(isolation_id,database_name,description) VALUES($1,current_database(),'P2 合成验收')", isolation); err != nil {
		t.Fatal(err)
	}
	verify(defaultPlan, a.admin, 2, 409)
	verify(defaultPlan, other, 1, 403) // This admin has valid dataset scope but is outside the publication audience.
	maskedResponse := verify(defaultPlan, a.admin, 1, 200)
	if !reflect.DeepEqual(maskedResponse["rows"], []any{[]any{"visible", nil, nil, nil}, []any{"visible", nil, nil, nil}}) ||
		!reflect.DeepEqual(maskedResponse["columns"], []any{"title", "note", "derived_note", "confidential_note"}) {
		t.Fatal("published report verification returned unexpected default projection")
	}
	for _, col := range []string{"private_key", "hidden_note"} {
		bad := defaultPlan
		bad.Columns = []string{col}
		a.call("POST", planPath, a.admin, bad, 403)
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
	execute := func(in db.BusinessOutputPlanInput) [][]any {
		t.Helper()
		p, e := a.deps.UserRepo.CompileBusinessOutputPlan(a.actor(), 1, "orders", in)
		if e != nil {
			t.Fatal(e)
		}
		rs, e := target.Query(ctx, p.Statement, p.Args...)
		if e != nil {
			t.Fatal(e)
		}
		values, e := pgx.CollectRows(rs, func(row pgx.CollectableRow) ([]any, error) { return row.Values() })
		if e != nil {
			t.Fatal(e)
		}
		return values
	}
	if rows := execute(defaultPlan); !reflect.DeepEqual(rows, [][]any{{"visible", nil, nil, nil}, {"visible", nil, nil, nil}}) {
		t.Fatal("unexpected default output from approved view")
	}
	req := db.BusinessRevealInput{BusinessReadPlanInput: defaultPlan.BusinessReadPlanInput, OutputPolicyVersion: 1,
		Purpose: "合成业务核对", Records: []db.BusinessRevealRecord{{Key: "1", Version: "v1"}}, TTLSeconds: 24 * 3600}
	for _, col := range []string{"private_key", "confidential_note", "derived_note"} {
		req.Columns = []string{col}
		a.call("POST", dataset+"/reveal-requests", a.admin, req, 403)
	}
	req.Columns = []string{"note"}
	req.TTLSeconds++
	a.call("POST", dataset+"/reveal-requests", a.admin, req, 400)
	req.TTLSeconds--
	makeRequest := func() string {
		t.Helper()
		o := a.call("POST", dataset+"/reveal-requests", a.admin, req, 200)
		if o["state"] != "pending" {
			t.Fatal("expected independently pending request")
		}
		return o["id"].(string)
	}
	id := makeRequest()
	decision := "/workspaces/1/business-reveal-requests/" + id
	clear := db.BusinessOutputPlanInput{BusinessReadPlanInput: req.BusinessReadPlanInput, OutputPolicyVersion: 1,
		RevealRequestID: id, Purpose: req.Purpose, Channel: "interactive_check"}
	a.call("POST", planPath, a.admin, clear, 403)
	approve := map[string]int{"ttl_seconds": 8 * 3600}
	a.call("POST", decision+"/approve", a.admin, approve, 403)
	a.call("POST", decision+"/approve", other, approve, 403)
	a.call("POST", decision+"/approve", reviewer, approve, 403)
	a.call("PUT", reviewerScope, a.admin, db.OrganizationScopeInput{BusinessMappingSource: source,
		ExpectedVersion: &one, IdentityVersion: 1, DatasetVersion: 1, Scopes: []db.OrganizationScopeEntry{deptA},
	}, 200)
	a.call("POST", decision+"/approve", reviewer, map[string]int{"ttl_seconds": 8*3600 + 1}, 400)
	granted := a.call("POST", decision+"/approve", reviewer, approve, 200)
	expires, err := time.Parse(time.RFC3339Nano, granted["approved_until"].(string))
	if err != nil || expires.After(source.ValidUntil) || granted["reviewer_id"] != float64(reviewerID) {
		t.Fatal("approval did not honor independent reviewer and shorter deadline")
	}
	a.call("POST", planPath, a.admin, clear, 200)
	if rows := execute(clear); !reflect.DeepEqual(rows, [][]any{{"sample-one"}}) {
		t.Fatal("approved output did not match exactly one record version and field")
	}
	clearResponse := verify(clear, a.admin, 1, 200)
	if !reflect.DeepEqual(clearResponse["rows"], []any{[]any{"sample-one"}}) ||
		!reflect.DeepEqual(clearResponse["columns"], []any{"note"}) {
		t.Fatal("published report verification exceeded the approved field or row revision")
	}
	for _, channel := range []string{"file", "notification"} {
		bad := clear
		bad.Channel = channel
		a.call("POST", planPath, a.admin, bad, 403)
		verify(bad, a.admin, 1, 403)
	}
	bad := clear
	bad.Purpose = "其他用途"
	a.call("POST", planPath, a.admin, bad, 403)
	a.call("POST", planPath, reviewer, clear, 403)
	if _, err = a.pool.Exec(ctx, "UPDATE sys_business_reveal_requests SET approved_until=statement_timestamp()-interval '1 second' WHERE id=$1::uuid", id); err != nil {
		t.Fatal(err)
	}
	a.call("POST", planPath, a.admin, clear, 403)
	verify(clear, a.admin, 1, 403)

	clear.RevealRequestID = makeRequest()
	decision = "/workspaces/1/business-reveal-requests/" + clear.RevealRequestID
	a.call("POST", decision+"/approve", reviewer, approve, 200)
	a.call("POST", decision+"/revoke", other, nil, 403)

	// Hold the synthetic target projection in flight while revoking its grant.
	// The pre-dispatch check has already passed once the target reaches the gate.
	lock, err := a.targetPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", gateID); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(verificationBody{clear, "orders", 1})
	if err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	call, err := http.NewRequestWithContext(run, http.MethodPost, a.server.URL+"/api/v1"+verifiedPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	call.Header.Set("Content-Type", "application/json")
	call.Header.Set("Authorization", "Bearer "+a.admin)
	type response struct {
		status int
		data   json.RawMessage
		err    error
	}
	done := make(chan response, 1)
	go func() {
		res, err := a.server.Client().Do(call)
		if err != nil {
			done <- response{err: err}
			return
		}
		defer res.Body.Close()
		var envelope struct{ Data json.RawMessage }
		err = json.NewDecoder(res.Body).Decode(&envelope)
		done <- response{status: res.StatusCode, data: envelope.Data, err: err}
	}()
	started := time.Now()
	for {
		var waiting bool
		if err = a.targetPool.QueryRow(run, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE usename=$1 AND wait_event='advisory' AND position($2 in query)>0)`, f.username, view).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case res := <-done:
			t.Fatalf("verification did not reach the target gate: %d, %v", res.status, res.err)
		default:
		}
		if time.Since(started) > 3*time.Second {
			t.Fatal("verification did not reach the target gate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.call("POST", decision+"/revoke", a.admin, nil, 200)
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	inFlight := <-done
	if inFlight.err != nil || inFlight.status != 409 || len(inFlight.data) != 0 {
		t.Fatalf("in-flight revoked verification returned data or unexpected status: %d, %v", inFlight.status, inFlight.err)
	}
	a.call("POST", planPath, a.admin, clear, 403)
	verify(clear, a.admin, 1, 403)
	verify(defaultPlan, a.admin, 1, 200)
	if err = a.deps.AuditJournal.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var ok, blocked int
	if err = a.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status=1),count(*) FILTER (WHERE status=0)
		FROM sys_audit_logs WHERE workspace_id=1 AND action='REPORT_POLICY_VERIFICATION'`).Scan(&ok, &blocked); err != nil || ok < 3 || blocked < 1 {
		t.Fatalf("missing successful/withheld verification audit: %d %d %v", ok, blocked, err)
	}
	f.release(reportID, 1, 0, "unpublish")
	verify(defaultPlan, a.admin, 1, 403)
}
