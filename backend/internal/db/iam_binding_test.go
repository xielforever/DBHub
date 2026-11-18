package db

import (
	"context"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/iam"
	"testing"
)

func TestIAMSingleStatementBindingMatchesCheckedFallbackAndClearsLocalScope(t *testing.T) {
	pool, users, _ := iamTestDatabase(t)
	iamTargetFixture(t, pool, users, 3, []string{"report.page", "report.view"})
	for _, uid := range []int64{1, 2, 3} {
		identity := auth.WithClaims(context.Background(), &auth.Claims{Subject: uid, Role: "admin"})
		var before, after string
		if e := users.pool.maintenance.QueryRow(identity, `SELECT COALESCE(current_setting('dbhub.actor_id',true),'')`).Scan(&before); e != nil {
			t.Fatal(e)
		}
		fast, e := users.BindWorkspace(identity, 1)
		if e != nil {
			t.Fatal(e)
		}
		if e = users.pool.maintenance.QueryRow(identity, `SELECT COALESCE(current_setting('dbhub.actor_id',true),'')`).Scan(&after); e != nil || before != after {
			t.Fatal("binding left pooled principal scope", before, after, e)
		}
		fallback, e := users.BindWorkspace(identity, 0)
		if e != nil {
			t.Fatal(e)
		}
		a, _ := auth.ClaimsFromContext(fast)
		b, _ := auth.ClaimsFromContext(fallback)
		if a.Role != b.Role || a.IAM.Target != b.IAM.Target || a.IAM.Mode != b.IAM.Mode || a.IAM.MembershipVersion != b.IAM.MembershipVersion || a.IAM.AuthorizationEpoch != b.IAM.AuthorizationEpoch || a.IAM.ConfigVersion != b.IAM.ConfigVersion || len(a.IAM.Sources) != len(b.IAM.Sources) {
			t.Fatal("explicit binding changed current identity/source boundary", uid, a.IAM, b.IAM)
		}
		for _, cap := range iam.ReadCatalog().Capabilities {
			if auth.HasAction(a, cap.ID) != auth.HasAction(b, cap.ID) {
				t.Fatal("explicit binding changed a capability", uid, cap.ID)
			}
		}
	}
}
