package db

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/user/dbhub/internal/auth"
)

func iamApprovedChange(t *testing.T, u *UserRepository, a, b string, patch IAMChange) json.RawMessage {
	t.Helper()
	ctx := iamBound(t, u, 1)
	result, e := u.IAMPropose(ctx, IAMProposalCommand{RequestID: dashboardUUID(), Baseline: iamBase(ctx), DelegationID: a, DelegationVersion: 1, ValidUntil: patch.ValidUntil, Patch: patch})
	if e != nil {
		t.Fatal(e)
	}
	var p struct {
		ID string `json:"id"`
	}
	if e = json.Unmarshal(result, &p); e != nil {
		t.Fatal(e)
	}
	result, e = u.IAMReview(iamBound(t, u, 2), IAMReviewCommand{RequestID: dashboardUUID(), ProposalID: p.ID, Version: 1, Decision: "approve", DelegationID: b, DelegationVersion: 1})
	if e != nil {
		t.Fatal(e)
	}
	var receipt struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(result, &receipt)
	if receipt.State != "applied" {
		t.Fatal("governance not applied", string(result))
	}
	return result
}
func TestIAMShadowApprovedBundleIsolationAndMonotonicRollback(t *testing.T) {
	pool, u, a, b := iamGovernanceFixture(t)
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	role := dashboardUUID()
	iamApprovedChange(t, u, a, b, IAMChange{Kind: "role_upsert", RoleID: role, Name: "经营成员自定义名称", Actions: []string{"report.page", "report.view", "dashboard.page", "dashboard.view"}, Enabled: true, ValidUntil: until})
	iamApprovedChange(t, u, a, b, IAMChange{Kind: "assignment_add", RoleID: role, RoleVersion: 1, RoleRevision: 1, SubjectType: "user", SubjectID: 3, ValidUntil: until})
	ctx := iamBound(t, u, 1)
	base := iamBase(ctx)
	var before int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM sys_iam_events`).Scan(&before)
	shadow, e := u.IAMShadow(ctx, IAMShadowCommand{UserIDs: []int64{3}})
	if e != nil {
		t.Fatal(e)
	}
	if !shadow.ReadOnly || shadow.TargetConnectionsOpened != 0 || len(shadow.Principals) != 1 || len(shadow.Principals[0].Differences) == 0 {
		t.Fatal("missing narrowing/read-only proof", shadow)
	}
	if !auth.HasAction(claim(t, iamBound(t, u, 3)), "query.read") {
		t.Fatal("shadow changed legacy query right")
	}
	var after int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM sys_iam_events`).Scan(&after)
	if before != after || iamBase(iamBound(t, u, 1)) != base {
		t.Fatal("shadow wrote governance state")
	}
	ack := map[int64][]IAMActionDifference{3: append([]IAMActionDifference{}, shadow.Principals[0].Differences...)}
	for i := range ack[3] {
		ack[3][i].Reason = "隔离验收：业务查看角色明确收窄技术工具"
	}
	cmd := IAMBundleCommand{RequestID: dashboardUUID(), Baseline: base, DelegationID: a, DelegationVersion: 1, UserIDs: []int64{3}, ShadowFingerprint: shadow.Fingerprint, Differences: ack, ValidUntil: until}
	if _, e = u.IAMPrepareBundle(ctx, cmd); !errors.Is(e, ErrManagedDenied) {
		t.Fatal("default deployment enabled target", e)
	}
	u.ConfigureIAMIsolation("isolated-unit-proof", "finite-route-fingerprint")
	if _, e = u.IAMPrepareBundle(ctx, cmd); !errors.Is(e, ErrManagedDenied) {
		t.Fatal("environment flag bypassed isolation marker", e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO sys_iam_isolation_guard VALUES('isolated-unit-proof',current_database(),'isolated unit fixture, not a release approval')`); e != nil {
		t.Fatal(e)
	}
	raw, e := u.IAMPrepareBundle(ctx, cmd)
	if e != nil {
		t.Fatal(e)
	}
	var bundle struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &bundle)
	replay, e := u.IAMPrepareBundle(ctx, cmd)
	if e != nil || string(replay) != string(raw) {
		t.Fatal("bundle receipt", e)
	}
	p := IAMProposalCommand{RequestID: dashboardUUID(), Baseline: base, DelegationID: a, DelegationVersion: 1, ValidUntil: until, Patch: IAMChange{Kind: "bundle_activate", BundleID: bundle.ID, ValidUntil: until}}
	raw, e = u.IAMPropose(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	var proposal struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &proposal)
	review := IAMReviewCommand{RequestID: dashboardUUID(), ProposalID: proposal.ID, Version: 1, Decision: "approve", DelegationID: b, DelegationVersion: 1}
	if _, e = u.IAMReview(iamBound(t, u, 2), review); !errors.Is(e, ErrManagedDenied) {
		t.Fatal("missing runtime fleet proof accepted", e)
	}
	for _, kind := range []string{"api", "worker", "database"} {
		if e = u.IAMHeartbeat(ctx, kind, "finite-route-fingerprint"); e != nil {
			t.Fatal(kind, e)
		}
	}
	if _, e = u.IAMReview(iamBound(t, u, 2), review); e != nil {
		t.Fatal(e)
	}
	target := claim(t, iamBound(t, u, 3))
	if !target.IAM.Target || !auth.HasAction(target, "report.view") || auth.HasAction(target, "query.read") {
		t.Fatal("target bundle mismatch", target.IAM)
	}
	if claim(t, iamBound(t, u, 2)).IAM.Target || !auth.HasAction(claim(t, iamBound(t, u, 2)), "query.write") {
		t.Fatal("unknown principal lost complete legacy path")
	}
	var assignment string
	if e = pool.QueryRow(ctx, `SELECT assignment_id::text FROM sys_iam_assignments WHERE role_id=$1::uuid`, role).Scan(&assignment); e != nil {
		t.Fatal(e)
	}
	ctx = iamBound(t, u, 1)
	if _, e = u.IAMRestrict(ctx, IAMRestrictionCommand{RequestID: dashboardUUID(), Baseline: iamBase(ctx), DelegationID: a, DelegationVersion: 1, Kind: "assignment.revoke", ID: assignment, Version: 1, Reason: "隔离测试撤销唯一来源"}); e != nil {
		t.Fatal(e)
	}
	ctx = iamBound(t, u, 1)
	rb := IAMRollbackCommand{RequestID: dashboardUUID(), Baseline: iamBase(ctx), BundleID: bundle.ID, DelegationID: a, DelegationVersion: 1, Reason: "隔离回退演练"}
	raw, e = u.IAMRollback(ctx, rb)
	if e != nil {
		t.Fatal(e)
	}
	reverted := claim(t, iamBound(t, u, 3))
	if reverted.IAM.Target || !reverted.IAM.RollbackGuard || auth.HasAction(reverted, "report.view") || auth.HasAction(reverted, "query.read") {
		t.Fatal("rollback revived removed grant or old tool", reverted.IAM)
	}
	replay, e = u.IAMRollback(iamBound(t, u, 1), rb)
	if e != nil || string(replay) != string(raw) {
		t.Fatal("rollback receipt", e)
	}
	if !slices.Contains(reverted.IAM.DeniedActions, "report.view") {
		t.Fatal("rollback narrowing was not persisted")
	}
	if _, e = pool.Exec(ctx, `UPDATE sys_iam_principal_modes SET valid_until=now()-interval '1 second' WHERE user_id=3`); e != nil {
		t.Fatal(e)
	}
	_, e = u.BindWorkspace(auth.WithClaims(context.Background(), &auth.Claims{Subject: 3, Role: "readonly"}), 1)
	if e == nil {
		t.Fatal("rollback revived expired principal")
	}
}

func TestIAMIsolatedMigrationControlsRequireRegistrationAndCurrentDelegation(t *testing.T) {
	pool, users, prepareDelegation, parent := iamGovernanceFixture(t)
	readEffective := func(subject int64) map[string]any {
		t.Helper()
		result, err := users.IAMEffective(iamBound(t, users, subject))
		if err != nil {
			t.Fatal(err)
		}
		return result.(map[string]any)
	}
	legacyActions := readEffective(1)["actions"].([]string)
	users.ConfigureIAMIsolation("migration-access-test", "finite-test-routes")
	if readEffective(1)["isolated_migration"] != nil {
		t.Fatal("a server setting alone exposed isolated migration controls")
	}
	ctx := iamBound(t, users, 1)
	if _, err := pool.Exec(ctx, `INSERT INTO sys_iam_isolation_guard VALUES('migration-access-test',current_database(),'isolated fixture')`); err != nil {
		t.Fatal(err)
	}
	qualified := readEffective(1)
	if access := qualified["isolated_migration"]; access != (iamIsolatedMigrationAccess{Prepare: true, Review: true, Rollback: true}) {
		t.Fatal("eligible legacy administrator did not receive migration controls", access)
	}
	if !slices.Equal(legacyActions, qualified["actions"].([]string)) || readEffective(3)["isolated_migration"] != nil {
		t.Fatal("migration controls changed legacy actions or appeared for another subject")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sys_iam_delegations(workspace_id,delegation_id,issuer_id,delegate_id,governance_actions,grantable_actions,target_ids,source_ids,valid_until,proposal_id,parent_id,parent_version)
		VALUES(1,$1::uuid,2,1,ARRAY['bundle.activate'],ARRAY['report.page','report.view'],ARRAY[3::bigint],'{}'::uuid[],now()+interval '1 hour',$2::uuid,$3::uuid,1)`, dashboardUUID(), dashboardUUID(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sys_iam_delegations SET revoked_at=now(),version=version+1 WHERE delegation_id=$1::uuid`, prepareDelegation); err != nil {
		t.Fatal(err)
	}
	if access := readEffective(1)["isolated_migration"]; access != (iamIsolatedMigrationAccess{Review: true}) {
		t.Fatal("current child delegation did not retain review-only controls", access)
	}
	if _, err := pool.Exec(ctx, `UPDATE sys_iam_delegations SET revoked_at=now(),version=version+1 WHERE delegation_id=$1::uuid`, parent); err != nil {
		t.Fatal(err)
	}
	if readEffective(1)["isolated_migration"] != nil {
		t.Fatal("revoked parent left child migration controls available")
	}
}

func claim(t *testing.T, ctx context.Context) *auth.Claims {
	t.Helper()
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c == nil {
		t.Fatal("claims missing")
	}
	return c
}

func TestIAMLegacyGroupMutationWaitsForTargetBoundary(t *testing.T) {
	pool, u, _ := iamTestDatabase(t)
	admin := iamBound(t, u, 1)
	if _, e := pool.Exec(admin, `INSERT INTO sys_user_groups(workspace_id,id,name,created_by) VALUES(1,700,'隔离并发组',1);INSERT INTO sys_user_group_members(workspace_id,group_id,user_id) VALUES(1,700,3)`); e != nil {
		t.Fatal(e)
	}
	iamTargetFixture(t, pool, u, 3, []string{"report.page", "report.view"})
	ctx := iamBound(t, u, 3)
	c := *claim(t, ctx)
	c.CurrentAction = "report.view"
	ctx = auth.WithClaims(ctx, &c)
	release, e := u.IAMLease(ctx)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, e := pool.Exec(context.Background(), `DELETE FROM sys_user_group_members WHERE workspace_id=1 AND group_id=700 AND user_id=3`)
		done <- e
	}()
	select {
	case e := <-done:
		release()
		t.Fatal("legacy group writer acknowledged before target boundary", e)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("legacy writer remained blocked")
	}
	if e = u.IAMCurrent(ctx); e == nil {
		t.Fatal("old target context survived legacy group change")
	}
}

func TestIAMRevokeCutsOffImmediatelyAndSeparatelyConfirmsExecutionFence(t *testing.T) {
	pool, u, a, _ := iamGovernanceFixture(t)
	iamTargetFixture(t, pool, u, 3, []string{"report.page", "report.view"})
	ctx := iamBound(t, u, 3)
	c := *claim(t, ctx)
	c.CurrentAction = "report.view"
	ctx = auth.WithClaims(ctx, &c)
	verify, release, e := u.IAMRequestFence(ctx, false)
	if e != nil {
		t.Fatal(e)
	}
	gov := iamBound(t, u, 1)
	id := dashboardUUID()
	raw, e := u.IAMRestrict(gov, IAMRestrictionCommand{RequestID: id, Baseline: iamBase(gov), DelegationID: a, DelegationVersion: 1, Kind: "member.freeze", TargetID: 3, Reason: "隔离测试先落库收紧，再核实执行边界"})
	if e != nil {
		release()
		t.Fatal(e)
	}
	var result struct {
		Fence string `json:"execution_fence"`
	}
	_ = json.Unmarshal(raw, &result)
	if result.Fence != "pending" {
		release()
		t.Fatal("inflight operation was falsely confirmed stopped")
	}
	if e = u.IAMCurrent(ctx); e == nil {
		release()
		t.Fatal("cutoff waited for a long operation instead of immediately revoking access")
	}
	if e = verify(); e == nil {
		release()
		t.Fatal("admission transaction reused a snapshot from before TARGET freeze")
	}
	if _, e = u.BindWorkspace(auth.WithClaims(context.Background(), &auth.Claims{Subject: 3, Role: "readonly"}), 1); e == nil {
		release()
		t.Fatal("frozen actor admitted a new operation")
	}
	var count int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM sys_iam_restriction_settlements WHERE request_id=$1::uuid`, id).Scan(&count); e != nil || count != 0 {
		release()
		t.Fatal("fence settled while target was admitted", e)
	}
	release()
	u.ConfigureIAMIsolation("unit-only", "finite-restriction-test")
	for _, kind := range []string{"api", "database", "worker"} {
		if e = u.IAMHeartbeat(context.Background(), kind, "finite-restriction-test"); e != nil {
			t.Fatal(e)
		}
	}
	if e = u.IAMSettleRestrictions(context.Background()); e != nil {
		t.Fatal(e)
	}
	own := auth.WithClaims(context.Background(), &auth.Claims{Subject: 1, Role: "admin"})
	status, e := u.IAMOwnRestrictionStatus(own, 1, id)
	if e != nil {
		t.Fatal(e)
	}
	var records []struct {
		Fence string `json:"execution_fence"`
	}
	_ = json.Unmarshal(status, &records)
	if len(records) != 1 || records[0].Fence != "confirmed" {
		t.Fatal("drained operation did not settle", string(status))
	}
	other := auth.WithClaims(context.Background(), &auth.Claims{Subject: 3, Role: "readonly"})
	status, e = u.IAMOwnRestrictionStatus(other, 1, id)
	if e != nil || string(status) != "[]" {
		t.Fatal("receipt exposed to another identity", e)
	}
}
