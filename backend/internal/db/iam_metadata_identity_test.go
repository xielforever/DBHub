package db

import (
	"reflect"
	"testing"

	"github.com/user/dbhub/internal/auth"
)

func TestIAMMetadataIdentityKeepsCurrentSessionFields(t *testing.T) {
	pool, users, ctx := iamTestDatabase(t)
	before, e := users.FindByUsername(ctx, "developer")
	if e != nil {
		t.Fatal(e)
	}
	for range 8 {
		actual, returned, e := users.FindMetadataIdentity(ctx, "developer")
		if e != nil || returned != ctx || !reflect.DeepEqual(before, actual) {
			t.Fatal("metadata identity changed session fields or context", e)
		}
	}
	if _, e = pool.Exec(ctx, `UPDATE sys_users SET auth_version=auth_version+1 WHERE id=2`); e != nil {
		t.Fatal(e)
	}
	actual, _, e := users.FindMetadataIdentity(ctx, "developer")
	if e != nil || actual.AuthVersion != before.AuthVersion+1 {
		t.Fatal("metadata identity reused an older session version", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE sys_users SET is_active=false WHERE id=2`); e != nil {
		t.Fatal(e)
	}
	if _, _, e = users.FindMetadataIdentity(ctx, "developer"); e != auth.ErrUserNotFound {
		t.Fatal("metadata identity accepted an inactive user", e)
	}
}
