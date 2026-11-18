package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/dbhub/pkg/crypto"
)

func TestIAMConnectionReadPlanRetainsOwnershipAndCurrentRLS(t *testing.T) {
	pool, users, _ := iamTestDatabase(t)
	if _, e := pool.Exec(context.Background(), `UPDATE sys_workspace_members SET role_code='developer' WHERE workspace_id=1 AND user_id=3`); e != nil {
		t.Fatal(e)
	}
	cipher, e := crypto.NewCipher([]byte("workspace-test-key-32-bytes-long-1"))
	if e != nil {
		t.Fatal(e)
	}
	repo := NewConnectionRepository(pool, cipher)
	for _, uid := range []int64{2, 3} {
		if e := repo.Create(iamBound(t, users, uid), &Connection{UserID: uid, Name: "isolated plan fixture", Type: "postgres", Host: "synthetic.invalid", Port: 5432, SSLMode: "disable"}); e != nil {
			t.Fatal(e)
		}
	}
	ctx := iamBound(t, users, 3)
	for range 8 {
		items, e := repo.List(ctx, "postgres", "")
		if e != nil || len(items) != 1 || items[0].UserID != 3 || items[0].HasPassword {
			t.Fatal("cached plan changed connection ownership/columns", items, e)
		}
	}
	tx, e := users.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var count int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM dbhub_connection_list('','',2)`).Scan(&count)
	_ = tx.Rollback(ctx)
	if e != nil || count != 0 {
		t.Fatal("read helper accepted a substituted connection owner", count, e)
	}
	if _, e = pool.Exec(context.Background(), `UPDATE sys_workspace_members SET active=false WHERE workspace_id=1 AND user_id=3`); e != nil {
		t.Fatal(e)
	}
	items, e := repo.List(ctx, "postgres", "")
	if e != nil || len(items) != 0 {
		t.Fatal("warmed read plan retained inactive member rows", items, e)
	}
}

func TestIAMConnectionAudiencePlanMatchesCurrentVisibility(t *testing.T) {
	for _, target := range []bool{false, true} {
		name := "legacy"
		if target {
			name = "target"
		}
		t.Run(name, func(t *testing.T) {
			pool, users, _ := iamTestDatabase(t)
			root := context.Background()
			if _, e := pool.Exec(root, `UPDATE sys_workspace_members SET role_code='developer' WHERE workspace_id=1 AND user_id=3`); e != nil {
				t.Fatal(e)
			}
			if target {
				iamTargetFixture(t, pool, users, 3, []string{"connection.page", "connection.use", "source.page"})
			}
			cipher, e := crypto.NewCipher([]byte("workspace-test-key-32-bytes-long-1"))
			if e != nil {
				t.Fatal(e)
			}
			repo := NewConnectionRepository(pool, cipher)
			ctx := iamBound(t, users, 3)
			conn := &Connection{UserID: 3, Name: "audience plan", Type: "postgres", Host: "synthetic.invalid", Port: 5432, SSLMode: "disable", Password: "private-plan-fixture-secret"}
			if e = repo.Create(ctx, conn); e != nil {
				t.Fatal(e)
			}
			var group int64
			if e = pool.QueryRow(root, `INSERT INTO sys_user_groups(workspace_id,name,created_by) VALUES(1,'plan group',1) RETURNING id`).Scan(&group); e != nil {
				t.Fatal(e)
			}
			check := func(allowed bool) {
				t.Helper()
				// Reuse the same query plans through every audience change. Group
				// mutations advance the authorization epoch, requiring a fresh bind.
				ctx = iamBound(t, users, 3)
				old, err := repo.Get(ctx, conn.ID)
				items, listErr := repo.List(ctx, "", "")
				if err != nil || listErr != nil || old.Definition == nil || len(items) != 1 || items[0].Definition == nil || old.Definition.AccessDenied == allowed || items[0].Definition.AccessDenied != old.Definition.AccessDenied {
					t.Fatal("current source audience differs between detail and list", allowed, old, items, err, listErr)
				}
				encoded, err := json.Marshal(items)
				if err != nil || !items[0].HasPassword || strings.Contains(string(encoded), conn.Password) {
					t.Fatal("connection list changed password boundary", err)
				}
			}
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(root, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			check(true) // own private definition
			exec(`UPDATE sys_source_definitions SET owner_id=2 WHERE id=$1`, *conn.DefinitionRevisionID)
			check(false) // another person's private definition
			exec(`UPDATE sys_source_definitions SET visibility='global' WHERE id=$1`, *conn.DefinitionRevisionID)
			check(true)
			exec(`UPDATE sys_source_families SET access_mode='restricted' WHERE family_id=$1::uuid`, conn.DefinitionFamilyID)
			check(false)
			exec(`INSERT INTO sys_source_grants(workspace_id,family_id,user_id) VALUES(1,$1::uuid,3)`, conn.DefinitionFamilyID)
			check(true)
			exec(`DELETE FROM sys_source_grants WHERE family_id=$1::uuid`, conn.DefinitionFamilyID)
			check(false)
			exec(`INSERT INTO sys_source_grants(workspace_id,family_id,group_id) VALUES(1,$1::uuid,$2)`, conn.DefinitionFamilyID, group)
			exec(`INSERT INTO sys_user_group_members(workspace_id,group_id,user_id) VALUES(1,$1,3)`, group)
			if stale, err := repo.List(ctx, "", ""); err != nil || len(stale) != 0 {
				t.Fatal("group mutation accepted a stale authorization epoch", stale, err)
			}
			check(true)
			exec(`DELETE FROM sys_user_group_members WHERE group_id=$1 AND user_id=3`, group)
			check(false)
		})
	}
}
