package db

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
)

func iamBound(t *testing.T, u *UserRepository, id int64) context.Context {
	t.Helper()
	ctx, e := u.BindWorkspace(auth.WithClaims(context.Background(), &auth.Claims{Subject: id, Role: "admin"}), 1)
	if e != nil {
		t.Fatal(e)
	}
	return ctx
}
func iamBase(ctx context.Context) IAMBaseline {
	c, _ := auth.ClaimsFromContext(ctx)
	return IAMBaseline{Config: c.IAM.ConfigVersion, Epoch: c.IAM.AuthorizationEpoch}
}
func iamGovernanceFixture(t *testing.T) (*pgxpool.Pool, *UserRepository, string, string) {
	t.Helper()
	pool, u, ctx := iamTestDatabase(t)
	if _, e := pool.Exec(ctx, `UPDATE sys_workspace_members SET role_code='admin' WHERE workspace_id=1 AND user_id=2`); e != nil {
		t.Fatal(e)
	}
	a, b := dashboardUUID(), dashboardUUID()
	for _, d := range []struct {
		id               string
		issuer, delegate int64
	}{{a, 2, 1}, {b, 1, 2}} {
		if _, e := pool.Exec(ctx, `INSERT INTO sys_iam_delegations(workspace_id,delegation_id,issuer_id,delegate_id,governance_actions,grantable_actions,target_ids,source_ids,valid_until,proposal_id) SELECT 1,$1::uuid,$2,$3,$4,ARRAY(SELECT action_id FROM sys_iam_capabilities WHERE scope='workspace' AND runtime_enabled ORDER BY action_id),ARRAY[1,2,3]::bigint[],'{}'::uuid[],now()+interval '1 day',$5::uuid`, d.id, d.issuer, d.delegate, governanceOperations(), dashboardUUID()); e != nil {
			t.Fatal(e)
		}
	}
	return pool, u, a, b
}
func TestIAMGovernanceIndependentReviewCASReceiptsAndFailure(t *testing.T) {
	pool, u, delegation, reviewDelegation := iamGovernanceFixture(t)
	ctx := iamBound(t, u, 1)
	until := time.Now().Add(time.Hour).UTC()
	p := IAMProposalCommand{RequestID: dashboardUUID(), Baseline: iamBase(ctx), DelegationID: delegation, DelegationVersion: 1, ValidUntil: until, Patch: IAMChange{Kind: "role_upsert", RoleID: dashboardUUID(), Name: "区域经营伙伴", Actions: []string{"report.page", "report.view", "dashboard.page", "dashboard.view"}, Enabled: true, ValidUntil: until}}
	if _, e := u.IAMPreview(ctx, p); e != nil {
		t.Fatal(e)
	}
	result, e := u.IAMPropose(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	var proposal struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(result, &proposal)
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM sys_iam_role_heads`).Scan(&count); e != nil || count != 0 {
		t.Fatal("pending proposal granted a role", e)
	}
	replay, e := u.IAMPropose(ctx, p)
	if e != nil || string(replay) != string(result) {
		t.Fatal("submit receipt mismatch", e)
	}
	p.Patch.Name = "changed"
	if _, e = u.IAMPropose(ctx, p); !errors.Is(e, ErrManagedConflict) {
		t.Fatal("request ID content substitution", e)
	}
	p.Patch.Name = "区域经营伙伴"
	review := IAMReviewCommand{RequestID: dashboardUUID(), ProposalID: proposal.ID, Version: 1, Decision: "approve", DelegationID: delegation, DelegationVersion: 1}
	if _, e = u.IAMReview(ctx, review); !errors.Is(e, ErrManagedDenied) {
		t.Fatal("requester approved own definition", e)
	}
	review.DelegationID = reviewDelegation
	reviewer := iamBound(t, u, 2)
	broken := auditctx.WithCheckpoint(reviewer, func() error { return errors.New("isolated audit outage") })
	if _, e = u.IAMReview(broken, review); e == nil {
		t.Fatal("approval survived missing audit")
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM sys_iam_role_heads`).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial grant survived rollback", e)
	}
	approved, e := u.IAMReview(reviewer, review)
	if e != nil {
		t.Fatal(e)
	}
	replay, e = u.IAMReview(iamBound(t, u, 2), review)
	if e != nil || string(replay) != string(approved) {
		t.Fatal("review receipt mismatch", e)
	}
	// A new grant to the reviewer itself must be approved by a different person.
	ctx = iamBound(t, u, 1)
	p = IAMProposalCommand{RequestID: dashboardUUID(), Baseline: iamBase(ctx), DelegationID: delegation, DelegationVersion: 1, ValidUntil: until, Patch: IAMChange{Kind: "assignment_add", RoleID: p.Patch.RoleID, RoleVersion: 1, RoleRevision: 1, SubjectType: "user", SubjectID: 2, ValidUntil: until}}
	result, e = u.IAMPropose(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal(result, &proposal)
	review = IAMReviewCommand{RequestID: dashboardUUID(), ProposalID: proposal.ID, Version: 1, Decision: "approve", DelegationID: reviewDelegation, DelegationVersion: 1}
	if _, e = u.IAMReview(iamBound(t, u, 2), review); !errors.Is(e, ErrManagedDenied) {
		t.Fatal("beneficiary approved own assignment", e)
	}
	// Changing the epoch after preview never partially applies a pending grant.
	p.RequestID = dashboardUUID()
	p.Patch.SubjectID = 3
	result, e = u.IAMPropose(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal(result, &proposal)
	if _, e = pool.Exec(ctx, `UPDATE sys_iam_workspace_state SET authorization_epoch=authorization_epoch+1 WHERE workspace_id=1`); e != nil {
		t.Fatal(e)
	}
	review.RequestID = dashboardUUID()
	review.ProposalID = proposal.ID
	result, e = u.IAMReview(iamBound(t, u, 2), review)
	if e != nil {
		t.Fatal(e)
	}
	var receipt struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(result, &receipt)
	if receipt.State != "conflict" {
		t.Fatal("stale baseline applied", string(result))
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM sys_iam_assignments`).Scan(&count); e != nil || count != 0 {
		t.Fatal("conflicting grant left partial assignment", e)
	}
}

func iamTargetFixture(t *testing.T, pool *pgxpool.Pool, u *UserRepository, uid int64, actions []string) (string, string) {
	t.Helper()
	ctx := iamBound(t, u, uid)
	role, assignment, bundle := dashboardUUID(), dashboardUUID(), dashboardUUID()
	if _, e := pool.Exec(ctx, `INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by) VALUES(1,$1::uuid,1,'任意名称', $2,true,1)`, role, actions); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled) VALUES(1,$1::uuid,1,'任意名称',true)`, role); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id) VALUES(1,$1::uuid,$2::uuid,1,'user',$3,ARRAY[$3::bigint],now()+interval '1 hour',1,$4::uuid)`, assignment, role, uid, dashboardUUID()); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO sys_iam_bundles(workspace_id,bundle_id,config_version,minimum_bridge,manifest,state,created_by) SELECT 1,$1::uuid,config_version+1,'phase1-bridge-2','{}','shadow',1 FROM sys_iam_workspace_state WHERE workspace_id=1`, bundle); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO sys_iam_principal_modes(workspace_id,user_id,mode,confirmed,legacy_data_role,legacy_capabilities,envelope_ref,bundle_id) SELECT 1,user_id,'TARGET',true,role_code,capabilities,'{"fixture":"isolated-only"}',$2::uuid FROM sys_workspace_members WHERE workspace_id=1 AND user_id=$1 ON CONFLICT(workspace_id,user_id) DO NOTHING`, uid, bundle); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `UPDATE sys_iam_workspace_state SET mode='COMPAT_ACTIVE',activation_ready=true,active_bundle_id=$1::uuid,minimum_bridge='phase1-bridge-2' WHERE workspace_id=1`, bundle); e != nil {
		t.Fatal(e)
	}
	return role, assignment
}
func TestIAMTargetGoSQLParityDataCeilingAndUnknownPrincipal(t *testing.T) {
	pool, u, _ := iamTestDatabase(t)
	actions := []string{"report.page", "report.view", "dashboard.page", "dashboard.view", "query.page", "query.read", "query.write"}
	_, assignment := iamTargetFixture(t, pool, u, 3, actions)
	ctx := iamBound(t, u, 3)
	c, _ := auth.ClaimsFromContext(ctx)
	if !c.IAM.Target || c.Role != "readonly" || auth.HasAction(c, "query.write") || !auth.HasAction(c, "report.view") || auth.HasCapability(c, "future.admin") {
		t.Fatal("functional roles changed data identity", c.IAM)
	}
	tx, e := u.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, cap := range iam.ReadCatalog().Capabilities {
		var allowed bool
		if e = tx.QueryRow(ctx, `SELECT dbhub_iam_has_action($1,$2)`, int64(3), cap.ID).Scan(&allowed); e != nil {
			t.Fatal(e)
		}
		if allowed != auth.HasAction(c, cap.ID) {
			t.Fatal("Go SQL mismatch", cap.ID, allowed)
		}
	}
	_ = tx.Rollback(ctx)
	unknown := iamBound(t, u, 2)
	other, _ := auth.ClaimsFromContext(unknown)
	if other.IAM.Target || other.Role != "developer" || !auth.HasAction(other, "query.write") {
		t.Fatal("unknown principal lost complete legacy path", other.IAM)
	}
	// Group and direct source unions retain a grant until the last source ends.
	if _, e = pool.Exec(ctx, `UPDATE sys_iam_assignments SET valid_until=now()+interval '2 seconds' WHERE workspace_id=1 AND assignment_id=$1::uuid`, assignment); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE sys_iam_principal_modes SET valid_until=now()-interval '1 second' WHERE workspace_id=1 AND user_id=3`); e != nil {
		t.Fatal(e)
	}
	if _, e = u.BindWorkspace(auth.WithClaims(context.Background(), &auth.Claims{Subject: 3, Role: "admin"}), 1); e == nil {
		t.Fatal("expired target fell back to legacy")
	}
	if !slices.Contains(actions, "query.write") {
		t.Fatal("fixture was weakened")
	}
}
