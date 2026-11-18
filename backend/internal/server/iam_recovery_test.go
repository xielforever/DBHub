package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"github.com/user/dbhub/internal/auth"
	appcrypto "github.com/user/dbhub/pkg/crypto"
	"strings"
	"testing"
	"time"
)

func TestIAMRecoveryTwoActualIdentitiesNewSessionAndNoBusinessRevival(t *testing.T) {
	t.Run("password-proofs", func(t *testing.T) { runIAMRecovery(t, false) })
	t.Run("password-and-fresh-mfa-proof", func(t *testing.T) { runIAMRecovery(t, true) })
}
func runIAMRecovery(t *testing.T, withMFA bool) {
	a := setupAPI(t)
	ctx := a.actor()
	hash, e := auth.HashPassword("RecoveryFixture123!")
	if e != nil {
		t.Fatal(e)
	}
	ids := []int64{}
	tokens := []string{}
	for _, name := range []string{"recovery-verifier-a", "recovery-verifier-b"} {
		var id int64
		if e = a.pool.QueryRow(ctx, `INSERT INTO sys_users(username,password_hash,role_id) SELECT $1,$2,id FROM sys_roles WHERE code='readonly' RETURNING id`, name, hash).Scan(&id); e != nil {
			t.Fatal(e)
		}
		if _, e = a.pool.Exec(ctx, `INSERT INTO sys_iam_recovery_roster(workspace_id,user_id) VALUES(1,$1)`, id); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
		login := a.call("POST", "/auth/login", "", map[string]any{"username": name, "password": "RecoveryFixture123!"}, 200)
		tokens = append(tokens, login["access_token"].(string))
	}
	if ids[0] == ids[1] || tokens[0] == tokens[1] {
		t.Fatal("recovery did not use two independent identities")
	}
	seed := ""
	var enabledCounter int64
	if withMFA {
		setup := a.call("POST", "/auth/mfa/setup", tokens[0], map[string]any{"password": "RecoveryFixture123!"}, 200)
		state, err := a.deps.UserRepo.MFAState(ctx, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		mac := hmac.New(sha256.New, []byte("test-main-key-32-bytes-long-123456789"))
		_, _ = mac.Write([]byte("dbhub-identity-mfa-v1"))
		cipher, err := appcrypto.NewCipher(mac.Sum(nil))
		if err != nil {
			t.Fatal(err)
		}
		plain, err := cipher.Decrypt(state.EncryptedPending)
		prefix := fmt.Sprintf("%d:", ids[0])
		if err != nil || !strings.HasPrefix(plain, prefix) {
			t.Fatal("isolated MFA fixture binding invalid")
		}
		seed = strings.TrimPrefix(plain, prefix)
		enabledCounter = time.Now().Unix() / 30
		enabled := a.call("POST", "/auth/mfa/enable", tokens[0], map[string]any{"setup_id": setup["setup_id"], "code": identityTestCode(seed, enabledCounter)}, 200)
		a.call("GET", "/auth/me", tokens[0], nil, 401)
		tokens[0] = enabled["access_token"].(string)
	}
	if _, e = a.pool.Exec(ctx, `INSERT INTO sys_iam_revocations(workspace_id,actor_id,target_id,request_id,kind,reason) VALUES(1,1,1,$1::uuid,'member_freeze','isolated last governor freeze')`, testRequestID()); e != nil {
		t.Fatal(e)
	}
	a.call("GET", "/workspaces/1/iam/effective", a.admin, nil, 404)
	requestID := testRequestID()
	body := map[string]any{"request_id": requestID, "workspace_id": 1, "target_id": 1, "reason": "isolated recovery after last governor freeze"}
	a.call("POST", "/iam-recovery/requests", a.admin, body, 200)
	verify := map[string]any{"request_id": requestID, "workspace_id": 1, "password": "wrong", "code": ""}
	a.call("POST", "/iam-recovery/verifications", tokens[0], verify, 403)
	verify["password"] = "RecoveryFixture123!"
	if withMFA {
		a.call("POST", "/iam-recovery/verifications", tokens[0], verify, 403)
		verify["code"] = identityTestCode(seed, enabledCounter)
		a.call("POST", "/iam-recovery/verifications", tokens[0], verify, 403)
		verify["code"] = identityTestCode(seed, max(enabledCounter+1, time.Now().Unix()/30))
	}
	one := a.call("POST", "/iam-recovery/verifications", tokens[0], verify, 200)
	if one["state"] != "pending" || one["verified_count"] != float64(1) {
		t.Fatal("one identity completed recovery", one)
	}
	if withMFA {
		a.call("POST", "/iam-recovery/verifications", tokens[0], verify, 403)
	} else {
		a.call("POST", "/iam-recovery/verifications", tokens[0], verify, 409)
	}
	a.call("GET", "/workspaces/1/iam/effective", a.admin, nil, 404)
	verify["code"] = ""
	two := a.call("POST", "/iam-recovery/verifications", tokens[1], verify, 200)
	if two["state"] != "applied" || two["verified_count"] != float64(2) || two["business_permissions_restored"] != false {
		t.Fatal("two actual proofs did not create limited recovery", two)
	}
	// Refreshing/reusing the old session cannot turn it into a recovery session.
	a.call("GET", "/workspaces/1/iam/effective", a.admin, nil, 404)
	login := a.call("POST", "/auth/login", "", map[string]any{"username": "admin", "password": "TestPassword123!"}, 200)
	fresh := login["access_token"].(string)
	effective := a.call("GET", "/iam/effective", fresh, nil, 200)
	snapshot := effective["snapshot"].(map[string]any)
	if snapshot["governance_only"] != true || snapshot["legacy_data_role"] != "" {
		t.Fatal("recovery restored old data role", snapshot)
	}
	for _, value := range effective["actions"].([]any) {
		action := fmt.Sprint(value)
		if action == "report.view" || action == "query.read" || action == "connection.use" {
			t.Fatal("recovery restored business rights", action)
		}
	}
	a.call("GET", "/connections", fresh, nil, 403)
	a.call("GET", "/published-reports", fresh, nil, 403)
	a.call("GET", "/iam/members", fresh, nil, 200)
	var frozen int
	if e = a.pool.QueryRow(ctx, `SELECT count(*) FROM sys_iam_revocations WHERE workspace_id=1 AND target_id=1 AND kind='member_freeze'`).Scan(&frozen); e != nil || frozen != 1 {
		t.Fatal("recovery erased freeze", e)
	}
	if _, e = a.pool.Exec(ctx, `UPDATE sys_iam_recovery_grants SET valid_until=now()-interval '1 second' WHERE workspace_id=1`); e != nil {
		t.Fatal(e)
	}
	a.call("GET", "/workspaces/1/iam/effective", fresh, nil, 404)
}
