package db

import (
	"testing"
	"time"
)

func TestIAMUnavailableFunctionsCannotBeProposedAsAvailable(t *testing.T) {
	pool, users, delegation, _ := iamGovernanceFixture(t)
	ctx := iamBound(t, users, 1)
	for _, action := range []string{"source.runtime.manage", "space.resources.request", "space.policy.manage", "audit.export"} {
		var enabled bool
		if e := pool.QueryRow(ctx, `SELECT runtime_enabled FROM sys_iam_capabilities WHERE action_id=$1`, action).Scan(&enabled); e != nil || enabled {
			t.Fatal("unimplemented function advertised as available", action, e)
		}
		_, e := users.IAMPreview(ctx, IAMProposalCommand{RequestID: dashboardUUID(), Baseline: iamBase(ctx), DelegationID: delegation, DelegationVersion: 1, ValidUntil: time.Now().Add(time.Hour), Patch: IAMChange{Kind: "role_upsert", RoleID: dashboardUUID(), Name: "不可用功能反例", Actions: []string{action}, Enabled: true, ValidUntil: time.Now().Add(time.Hour)}})
		if e == nil {
			t.Fatal("unimplemented function accepted in a new role", action)
		}
	}
}
