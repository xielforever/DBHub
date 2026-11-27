package db

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/user/dbhub/internal/auth"
)

func TestIAMDirectRolesPreserveSpaceAndRecheckOldSession(t *testing.T) {
	pool, users, _ := iamTestDatabase(t)
	identity := func(id int64) context.Context {
		return auth.WithClaims(context.Background(), &auth.Claims{Subject: id, Role: "untrusted"})
	}
	root := identity(1)
	var otherID int64
	if err := pool.QueryRow(root, `INSERT INTO sys_users(username,password_hash,role_id,is_platform_admin)
 SELECT 'second-root','opaque-fixture-hash',id,true FROM sys_roles WHERE code='admin' RETURNING id`).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	left, err := users.CreateMatrixWorkspace(root, "空间 A", dashboardUUID())
	if err != nil {
		t.Fatal(err)
	}
	right, err := users.CreateMatrixWorkspace(identity(otherID), "空间 B", dashboardUUID())
	if err != nil {
		t.Fatal(err)
	}
	add := func(acting, workspace int64, user, role string) {
		t.Helper()
		ctx, err := users.BindWorkspace(identity(acting), workspace)
		if err == nil {
			err = users.AddMember(ctx, user, role)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	add(1, left.ID, "developer", "admin")
	add(1, left.ID, "readonly", "readonly")
	add(otherID, right.ID, "readonly", "readonly")
	read := func(actor context.Context, workspace, member int64) IAMDirectMember {
		t.Helper()
		out, err := users.IAMDirectMembers(actor, workspace)
		if err != nil || !out.Enabled || len(out.Roles) != 3 {
			t.Fatalf("read workspace %d: %#v, %v", workspace, out, err)
		}
		for _, m := range out.Members {
			if m.UserID == member {
				return m
			}
		}
		t.Fatalf("missing member %d in %d", member, workspace)
		return IAMDirectMember{}
	}
	command := func(member IAMDirectMember, roles ...string) IAMDirectRoleCommand {
		return IAMDirectRoleCommand{RequestID: dashboardUUID(), ExpectedVersion: member.Version, RoleCodes: append([]string{}, roles...)}
	}
	bind := func(id, workspace int64) context.Context {
		t.Helper()
		ctx, err := users.BindWorkspace(identity(id), workspace)
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	legacySpace, err := users.CreateWorkspace(root, "既有授权方式空间", dashboardUUID())
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := users.IAMDirectMembers(root, legacySpace.ID)
	if err != nil || legacy.Enabled {
		t.Fatalf("existing workspace changed: %#v %v", legacy, err)
	}
	if _, err = users.BindWorkspace(root, right.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("platform identity received business membership: %v", err)
	}
	localAdmin, ordinary := identity(2), identity(3)
	leftMember := read(localAdmin, left.ID, 3)
	rightMember := read(root, right.ID, 3)
	if _, err = users.IAMSetDirectRoles(localAdmin, right.ID, 3, command(rightMember, "viewer")); !errors.Is(err, ErrManagedDenied) {
		t.Fatalf("cross-space operator was admitted: %v", err)
	}
	if _, err = users.IAMSetDirectRoles(ordinary, left.ID, 3, command(leftMember, "viewer")); !errors.Is(err, ErrManagedDenied) {
		t.Fatalf("ordinary member could allocate: %v", err)
	}
	if _, err = users.IAMSetDirectRoles(localAdmin, left.ID, 3, command(leftMember, "admin")); !errors.Is(err, ErrManagedInput) {
		t.Fatalf("administrator identity accepted as role: %v", err)
	}

	// A profile grant from before activation must remain dormant when the
	// direct role path selects this principal for the first time.
	oldRole := dashboardUUID()
	if _, err = pool.Exec(root, `INSERT INTO sys_iam_role_revisions(workspace_id,role_id,revision,name,actions,enabled,created_by)
 VALUES($1,$2,1,'dormant fixture',ARRAY['query.page','query.read'],true,1);
 INSERT INTO sys_iam_role_heads(workspace_id,role_id,revision,display_name,enabled) VALUES($1,$2,1,'dormant fixture',true);
 INSERT INTO sys_iam_assignments(workspace_id,assignment_id,role_id,role_revision,subject_type,subject_id,approved_members,valid_until,granted_by,proposal_id)
 VALUES($1,gen_random_uuid(),$2,1,'user',3,ARRAY[3::bigint],now()+interval '1 day',1,gen_random_uuid())`, left.ID, oldRole); err != nil {
		t.Fatal(err)
	}
	grant := command(leftMember, "viewer", "business_supervisor")
	assigned, err := users.IAMSetDirectRoles(localAdmin, left.ID, 3, grant)
	if err != nil || assigned.Version <= leftMember.Version || !slices.Equal(assigned.RoleCodes, []string{"business_supervisor", "viewer"}) {
		t.Fatalf("save and readback: %#v, %v", assigned, err)
	}
	if replay, err := users.IAMSetDirectRoles(localAdmin, left.ID, 3, grant); err != nil || replay.Version != assigned.Version {
		t.Fatalf("receipt replay = %#v %v", replay, err)
	}
	if _, err := users.IAMSetDirectRoles(localAdmin, left.ID, 3, command(leftMember, "viewer")); !errors.Is(err, ErrManagedConflict) {
		t.Fatalf("stale member write = %v", err)
	}
	ctx := bind(3, left.ID)
	claims, _ := auth.ClaimsFromContext(ctx)
	if !claims.IAM.Target || !auth.HasAction(claims, "report.review") || !auth.HasAction(claims, "report.view") || auth.HasAction(claims, "query.read") {
		t.Fatalf("unexpected active or dormant permissions: %#v", claims.IAM.Actions)
	}
	guardTx, err := users.iamBegin(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = iamImpact(ctx, guardTx, IAMChange{
		Kind: "assignment_add", RoleID: oldRole, RoleVersion: 1, RoleRevision: 1,
		SubjectType: "user", SubjectID: 2, ValidUntil: time.Now().Add(time.Hour),
	})
	_ = guardTx.Rollback(ctx)
	if !errors.Is(err, ErrManagedDenied) {
		t.Fatalf("legacy assignment path skipped current manager identity: %v", err)
	}
	var supervisor bool
	if err := users.pool.QueryRow(ctx, `SELECT dbhub_iam_builtin_role(3,'business_supervisor')`).Scan(&supervisor); err != nil || !supervisor {
		t.Fatalf("supervisor not visible from restricted report transaction: %v %v", supervisor, err)
	}
	viewer, err := users.IAMSetDirectRoles(localAdmin, left.ID, 3, command(read(localAdmin, left.ID, 3), "viewer"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.IAMCurrent(ctx); !errors.Is(err, ErrConnectionChanged) {
		t.Fatalf("old action context survived revoke: %v", err)
	}
	ctx = bind(3, left.ID)
	claims, _ = auth.ClaimsFromContext(ctx)
	if auth.HasAction(claims, "report.review") || !auth.HasAction(claims, "report.view") {
		t.Fatalf("role union changed incorrectly: %v", claims.IAM.Actions)
	}
	if _, err := users.IAMSetDirectRoles(localAdmin, left.ID, 3, command(viewer)); err != nil {
		t.Fatal(err)
	}
	claims, _ = auth.ClaimsFromContext(bind(3, left.ID))
	if !claims.IAM.Target || auth.HasAction(claims, "report.view") {
		t.Fatalf("last revoke fell back to legacy: %#v", claims.IAM)
	}
	if _, err := users.IAMSetDirectRoles(root, right.ID, 3, command(rightMember, "viewer")); err != nil {
		t.Fatalf("nonmember platform authority rejected: %v", err)
	}
	if _, err := users.IAMSetDirectRoles(root, right.ID, 3, command(read(root, right.ID, 3))); err != nil {
		t.Fatalf("nonmember platform revoke rejected: %v", err)
	}
	if _, err := users.IAMSetDirectRoles(root, left.ID, 2, command(read(root, left.ID, 2), "viewer")); err != nil {
		t.Fatal(err)
	}
	admin := bind(2, left.ID)
	adminClaims, _ := auth.ClaimsFromContext(admin)
	if adminClaims.Role != "admin" || !auth.HasAction(adminClaims, "space.page") {
		t.Fatal("ordinary role assignment disabled workspace administration")
	}
	if _, err := users.IAMSetDirectRoles(root, right.ID, 3, command(read(root, right.ID, 3), "business_it")); err != nil {
		t.Fatal(err)
	}
	developer, _ := auth.ClaimsFromContext(bind(3, right.ID))
	if !auth.HasAction(developer, "connection.manage") || auth.HasAction(developer, "query.write") || auth.HasAction(developer, "source.definition.manage") {
		t.Fatalf("business IT role changed a resource or write boundary: %v", developer.IAM.Actions)
	}
	var events int
	if err := pool.QueryRow(root, `SELECT count(*) FROM sys_iam_events WHERE workspace_id=$1 AND actor_id=1
	 AND operation='matrix.direct_roles.set' AND payload ? 'previous_role_codes'`, right.ID).Scan(&events); err != nil || events != 3 {
		t.Fatalf("global change evidence = %d %v", events, err)
	}
}
