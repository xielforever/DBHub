package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/user/dbhub/internal/config"
	crypto "github.com/user/dbhub/pkg/crypto"
)

const oidcFixtureTOTP = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

func enableOIDCFixtureMFA(t *testing.T, a *apiTest) {
	t.Helper()
	ctx := context.Background()
	u, err := a.deps.UserRepo.FindByUsername(ctx, "readonly")
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("test-main-key-32-bytes-long-123456789"))
	mac.Write([]byte("dbhub-identity-mfa-v1"))
	cipher, err := crypto.NewCipher(mac.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt(fmt.Sprintf("%d:%s", u.ID, oidcFixtureTOTP))
	if err != nil {
		t.Fatal(err)
	}
	id := testRequestID()
	if err = a.deps.UserRepo.StartMFASetup(ctx, u, id, encrypted, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	state, err := a.deps.UserRepo.MFAState(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	hashes := make([]string, 10)
	for i := range hashes {
		sum := sha256.Sum256([]byte(fmt.Sprintf("isolated-oidc-fixture-recovery-%d", i)))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	if err = a.deps.UserRepo.EnableMFA(ctx, u, id, state.Version, 0, hashes); err != nil {
		t.Fatal(err)
	}
}
func TestOIDCIdentityBindingAndMFARealDatabase(t *testing.T) {
	idp := newOIDCTestProvider(t, "", "")
	var callback string
	a := setupConfiguredAPI(t, "", func(cfg *config.Config, base string) {
		callback = base + "/api/v1/auth/sso/callback"
		cfg.OIDC = config.OIDCConfig{Issuer: idp.issuer, ClientID: oidcTestClient, ClientSecret: oidcTestSecret, CallbackURL: callback}
		idp.configure(callback, "")
	})
	a.call("GET", "/platform/sso-bindings", a.developer, nil, 403)
	// Workspace administration cannot grant global authentication identities.
	if _, err := a.pool.Exec(a.actor(), `UPDATE sys_workspace_members SET role_code='admin' WHERE workspace_id=1 AND user_id=2`); err != nil {
		t.Fatal(err)
	}
	a.call("POST", "/platform/sso-bindings", a.developer, map[string]any{"username": "developer", "subject": "acceptance-developer", "expected_version": 0, "enabled": true}, 403)
	bind := func(username, subject string, version int64, enabled bool, want int) {
		a.call("POST", "/platform/sso-bindings", a.admin, map[string]any{"username": username, "subject": subject, "expected_version": version, "enabled": enabled}, want)
	}
	type flowResult struct {
		client             *http.Client
		callback, location string
	}
	tryMissingCookie := false
	flow := func(identity, mode string, redirect ...string) flowResult {
		idp.configure(callback, mode)
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		startURL := a.server.URL + "/api/v1/auth/sso/start"
		if len(redirect) != 0 {
			startURL += "?" + url.Values{"redirect": {redirect[0]}}.Encode()
		}
		response, err := client.Get(startURL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 303 {
			t.Fatal("SSO authorization did not start")
		}
		authorize, err := url.Parse(response.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		values := authorize.Query()
		if values.Get("code_challenge_method") != "S256" || values.Get("nonce") == "" || values.Get("state") == "" {
			t.Fatal("SSO protection parameters missing")
		}
		if values.Get("redirect_uri") != callback || values.Has("redirect") {
			t.Fatal("application return path altered or leaked through the provider callback configuration")
		}
		values.Set("identity", identity)
		response, err = client.PostForm(idp.issuer+"/authorize", values)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 303 {
			t.Fatal("test provider refused authorization")
		}
		target := response.Header.Get("Location")
		if tryMissingCookie {
			tryMissingCookie = false
			attackResponse, attackError := (&http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(target)
			if attackError != nil {
				t.Fatal(attackError)
			}
			attackResponse.Body.Close()
			if !strings.HasSuffix(attackResponse.Header.Get("Location"), "sso=failed") {
				t.Fatal("valid pending callback accepted without its browser cookie")
			}
		}
		response, err = client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 303 {
			t.Fatal("SSO callback did not redirect to a clean login URL")
		}
		location := response.Header.Get("Location")
		parsed, _ := url.Parse(location)
		query := parsed.Query()
		wantFields := 1
		if len(redirect) != 0 {
			wantFields = 2
			if query.Get("redirect") != redirect[0] {
				t.Fatal("callback did not restore the application's return path")
			}
		}
		if parsed.Path != "/login" || len(query) != wantFields || strings.Contains(location, "token") {
			t.Fatal("callback exposed credential material in navigation")
		}
		return flowResult{client, target, location}
	}
	complete := func(f flowResult, want int) map[string]any {
		req, _ := http.NewRequest("POST", a.server.URL+"/api/v1/auth/sso/complete", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DBHub-CSRF", "1")
		response, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("SSO handoff status %d, expected %d", response.StatusCode, want)
		}
		var packet struct {
			Data map[string]any `json:"data"`
		}
		if err = json.NewDecoder(response.Body).Decode(&packet); err != nil {
			t.Fatal(err)
		}
		return packet.Data
	}
	const destination = "/profile?workspace_id=1#security"
	unbound := flow("unbound", "", destination)
	if !strings.HasSuffix(unbound.location, "sso=failed") {
		t.Fatal("unbound identity was auto-provisioned or email-linked")
	}
	complete(unbound, 401)
	bind("developer", "acceptance-developer", 0, true, 200)
	bind("readonly", "acceptance-developer", 0, true, 409)
	bind("developer", "another-subject", 0, true, 409)
	good := flow("developer", "", destination)
	if !strings.HasSuffix(good.location, "sso=complete") {
		t.Fatal("explicit binding did not authenticate")
	}
	logged := complete(good, 200)
	token := logged["access_token"].(string)
	a.call("GET", "/auth/me", token, nil, 200)
	complete(good, 401)
	for _, mode := range []string{"wrong_nonce", "wrong_audience", "wrong_issuer", "expired", "bad_signature"} {
		bad := flow("developer", mode)
		if !strings.HasSuffix(bad.location, "sso=failed") {
			t.Fatalf("invalid %s was accepted", mode)
		}
		complete(bad, 401)
	}
	// A callback without its browser cookie must not authenticate, even when the
	// authorization code and state were copied from a legitimate browser.
	tryMissingCookie = true
	stolen := flow("developer", "")
	response, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(stolen.callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !strings.HasSuffix(response.Header.Get("Location"), "sso=failed") {
		t.Fatal("callback replay without browser binding accepted")
	}
	bind("developer", "acceptance-developer", 1, false, 200)
	complete(stolen, 401)
	a.call("GET", "/auth/me", token, nil, 401)
	bind("developer", "acceptance-developer", 1, true, 409)
	if !strings.HasSuffix(flow("developer", "").location, "sso=failed") {
		t.Fatal("revoked identity still authenticates")
	}
	bind("developer", "acceptance-developer", 2, true, 200)
	complete(flow("developer", ""), 200)
	bind("readonly", "acceptance-readonly", 0, true, 200)
	enableOIDCFixtureMFA(t, a)
	mfa := complete(flow("readonly", ""), 200)
	if mfa["mfa_required"] != true || mfa["access_token"] != nil {
		t.Fatal("SSO bypassed local MFA")
	}
	verified := a.call("POST", "/auth/mfa/verify", "", map[string]any{"challenge_token": mfa["challenge_token"], "code": identityTestCode(oidcFixtureTOTP, time.Now().Unix()/30)}, 200)
	a.call("GET", "/auth/me", verified["access_token"].(string), nil, 200)
	var users int
	if err = a.pool.QueryRow(a.actor(), `SELECT count(*) FROM sys_users`).Scan(&users); err != nil || users != 3 {
		t.Fatal("SSO unexpectedly created users")
	}
}

// The browser fixture uses the production router and a separate migrated schema.
// It is started only by the local acceptance harness and cleans up on a stop file.
func TestSSOBrowserFixture(t *testing.T) {
	if os.Getenv("TEST_SSO_BROWSER_FIXTURE") != "1" {
		t.Skip("browser fixture not requested")
	}
	idp := newOIDCTestProvider(t, ":18083", "http://127.0.0.1:18083")
	a := setupConfiguredAPI(t, ":18082", func(cfg *config.Config, _ string) {
		cfg.OIDC = config.OIDCConfig{Issuer: idp.issuer, ClientID: oidcTestClient, ClientSecret: oidcTestSecret, CallbackURL: "http://127.0.0.1:15173/api/v1/auth/sso/callback"}
		idp.configure(cfg.OIDC.CallbackURL, "")
	})
	enableOIDCFixtureMFA(t, a)
	if os.Getenv("TEST_SSO_BROWSER_RECOVERY") == "1" {
		prepareBrowserRecoveryCodes(t, a)
	}
	if err := os.WriteFile("/tmp/dbhub-sso-fixture-ready", []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/tmp/dbhub-sso-fixture-stop"); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("SSO browser fixture expired without a verified shutdown")
}

// Enroll the disposable developer through the production MFA endpoints. The
// readonly fixture retains its existing authenticator for the OIDC checks.
func prepareBrowserRecoveryCodes(t *testing.T, a *apiTest) {
	t.Helper()
	setup := a.call("POST", "/auth/mfa/setup", a.developer, map[string]any{"password": "TestPassword123!"}, 200)
	seed := pendingMFATestSeed(t, a, "developer")
	enabled := a.call("POST", "/auth/mfa/enable", a.developer, map[string]any{
		"setup_id": setup["setup_id"],
		"code":     identityTestCode(seed, time.Now().Unix()/30),
	}, 200)
	codes, ok := enabled["recovery_codes"].([]any)
	if !ok || len(codes) != 10 {
		t.Fatal("unexpected recovery code count in browser fixture")
	}
	first, firstOK := codes[0].(string)
	second, secondOK := codes[1].(string)
	if !firstOK || !secondOK || len(first) != 32 || len(second) != 32 || first == second {
		t.Fatal("invalid recovery codes in browser fixture")
	}
	const handoff = "/tmp/dbhub-sso-fixture-recovery"
	file, err := os.OpenFile(handoff, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("cannot create private browser recovery handoff")
	}
	t.Cleanup(func() { _ = os.Remove(handoff) })
	encodeErr := json.NewEncoder(file).Encode([]string{first, second})
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		t.Fatal("cannot write private browser recovery handoff")
	}
}
