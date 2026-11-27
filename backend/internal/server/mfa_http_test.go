package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	crypto "github.com/user/dbhub/pkg/crypto"
)

// The authenticator is implemented independently of the production verifier.
// The seed is read from the isolated test repository, never returned by HTTP.
func identityTestCode(secret string, counter int64) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var value [8]byte
	binary.BigEndian.PutUint64(value[:], uint64(counter))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(value[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[offset:offset+4])&0x7fffffff)%1000000)
}

func pendingMFATestSeed(t *testing.T, a *apiTest, username string) string {
	t.Helper()
	ctx := context.Background()
	repo := a.deps.UserRepo
	user, err := repo.FindByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.MFAState(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("test-main-key-32-bytes-long-123456789"))
	_, _ = mac.Write([]byte("dbhub-identity-mfa-v1"))
	cipher, err := crypto.NewCipher(mac.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(state.EncryptedPending)
	if err != nil {
		t.Fatal("enrollment seed cannot be decrypted with its identity key")
	}
	prefix := fmt.Sprintf("%d:", user.ID)
	if !strings.HasPrefix(plain, prefix) {
		t.Fatal("seed not bound to current identity")
	}
	return strings.TrimPrefix(plain, prefix)
}

func TestMFAHTTPEnrollmentLoginAndRecovery(t *testing.T) {
	a := setupAPI(t)
	ctx := context.Background()
	repo := a.deps.UserRepo
	user, err := repo.FindByUsername(ctx, "readonly")
	if err != nil {
		t.Fatal(err)
	}
	a.call("POST", "/auth/mfa/setup", a.readonly, map[string]any{"password": "incorrect"}, 400)
	setup := a.call("POST", "/auth/mfa/setup", a.readonly, map[string]any{"password": "TestPassword123!"}, 200)
	if setup["secret"] != nil || !strings.HasPrefix(setup["qr_image"].(string), "data:image/png;base64,") {
		t.Fatal("enrollment is not an image-only provisioning response")
	}
	seed := pendingMFATestSeed(t, a, "readonly")
	a.call("POST", "/auth/mfa/enable", a.readonly, map[string]any{"setup_id": testRequestID(), "code": "123456"}, 400)
	a.call("POST", "/auth/mfa/enable", a.readonly, map[string]any{"setup_id": setup["setup_id"], "code": "invalid"}, 400)
	counter := time.Now().Unix() / 30
	enabled := a.call("POST", "/auth/mfa/enable", a.readonly, map[string]any{"setup_id": setup["setup_id"], "code": identityTestCode(seed, counter)}, 200)
	codes := enabled["recovery_codes"].([]any)
	if len(codes) != 10 {
		t.Fatal("missing recovery codes")
	}
	for _, code := range codes {
		if len(code.(string)) != 32 {
			t.Fatal("recovery code has insufficient entropy")
		}
	}
	a.call("GET", "/auth/me", a.readonly, nil, 401)
	newSession := enabled["access_token"].(string)
	status := a.call("GET", "/auth/mfa", newSession, nil, 200)
	if status["enabled"] != true || status["recovery_codes_remaining"] != float64(10) {
		t.Fatal("enabled status mismatch")
	}
	challenge := func() string {
		data := a.call("POST", "/auth/login", "", map[string]any{"username": "readonly", "password": "TestPassword123!"}, 200)
		if data["mfa_required"] != true || data["access_token"] != nil || data["refresh_token"] != nil || data["user"] != nil {
			t.Fatal("primary login bypassed MFA")
		}
		return data["challenge_token"].(string)
	}
	first := challenge()
	a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": first, "code": "bad"}, 401)
	a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": first, "code": identityTestCode(seed, counter)}, 401)
	verified := a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": first, "code": identityTestCode(seed, counter+1)}, 200)
	a.call("GET", "/auth/me", verified["access_token"].(string), nil, 200)
	a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": first, "recovery_code": codes[0]}, 401)
	second := challenge()
	a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": second, "code": identityTestCode(seed, counter+1)}, 401)
	recovered := a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": second, "recovery_code": codes[0]}, 200)
	third := challenge()
	a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": third, "recovery_code": codes[0]}, 401)
	for i := 0; i < 4; i++ {
		a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": third, "code": "bad"}, 401)
	}
	a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": third, "recovery_code": codes[1]}, 401)
	stale := challenge()
	a.call("POST", "/auth/mfa/disable", newSession, map[string]any{"password": "incorrect", "recovery_code": codes[1]}, 400)
	disabled := a.call("POST", "/auth/mfa/disable", newSession, map[string]any{"password": "TestPassword123!", "recovery_code": codes[1]}, 200)
	a.call("GET", "/auth/me", newSession, nil, 401)
	a.call("GET", "/auth/me", recovered["access_token"].(string), nil, 401)
	a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": stale, "recovery_code": codes[2]}, 401)
	a.call("GET", "/auth/me", disabled["access_token"].(string), nil, 200)
	plainLogin := a.call("POST", "/auth/login", "", map[string]any{"username": "readonly", "password": "TestPassword123!"}, 200)
	if plainLogin["access_token"] == nil || plainLogin["mfa_required"] != nil {
		t.Fatal("MFA disable did not restore ordinary login")
	}
	state, err := repo.MFAState(ctx, user.ID)
	if err != nil || state.Enabled || state.EncryptedSecret != "" || state.RecoveryCount != 0 {
		t.Fatal("disabled factors not cleared")
	}
}
