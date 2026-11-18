package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// This provider exists only in *_test.go. It exercises the real authorization
// code flow and RSA/JWKS verification without contacting an enterprise tenant.
type oidcTestCode struct {
	Nonce, Challenge, Subject, Mode string
	Expires                         time.Time
}
type oidcTestProvider struct {
	server                 *httptest.Server
	issuer, callback, mode string
	mu                     sync.Mutex
	codes                  map[string]oidcTestCode
	key                    *rsa.PrivateKey
}

const oidcTestClient = "dbhub-local-acceptance"
const oidcTestSecret = "isolated-oidc-fixture-client-secret"

func newOIDCTestProvider(t *testing.T, listen, publicIssuer string) *oidcTestProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &oidcTestProvider{codes: map[string]oidcTestCode{}, key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"issuer": p.issuer, "authorization_endpoint": p.issuer + "/authorize", "token_endpoint": p.issuer + "/token", "jwks_uri": p.issuer + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"}})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"keys": []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "local-acceptance", Use: "sig", Algorithm: "RS256"}}})
	})
	form := template.Must(template.New("login").Parse(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>本机 SSO 模拟身份提供方</title><main><h1>本机 SSO 模拟身份提供方</h1><p>仅用于隔离验收，不连接企业身份系统。</p><form method="post">{{range $key,$values:=.}}{{range $values}}<input type="hidden" name="{{$key}}" value="{{.}}">{{end}}{{end}}<label>模拟身份 <select name="identity" aria-label="模拟身份"><option value="developer">BI 开发者</option><option value="readonly">启用 MFA 的业务员</option><option value="unbound">未关联的身份</option></select></label><button type="submit">使用模拟身份登录</button></form></main></html>`))
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		q := r.Form
		p.mu.Lock()
		callback, mode := p.callback, p.mode
		p.mu.Unlock()
		if q.Get("client_id") != oidcTestClient || q.Get("redirect_uri") != callback || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" {
			http.Error(w, "invalid authorization request", 400)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html;charset=utf-8")
			form.Execute(w, q)
			return
		}
		subject := "acceptance-" + q.Get("identity")
		if subject != "acceptance-developer" && subject != "acceptance-readonly" {
			subject = "acceptance-unbound"
		}
		code := testRequestID()
		p.mu.Lock()
		p.codes[code] = oidcTestCode{q.Get("nonce"), q.Get("code_challenge"), subject, mode, time.Now().Add(time.Minute)}
		p.mu.Unlock()
		target, _ := url.Parse(callback)
		target.RawQuery = url.Values{"state": {q.Get("state")}, "code": {code}}.Encode()
		http.Redirect(w, r, target.String(), 303)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		id, secret, ok := r.BasicAuth()
		if !ok || id != oidcTestClient || secret != oidcTestSecret {
			http.Error(w, "invalid client", 401)
			return
		}
		p.mu.Lock()
		code, exists := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		callback := p.callback
		p.mu.Unlock()
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !exists || !code.Expires.After(time.Now()) || base64.RawURLEncoding.EncodeToString(digest[:]) != code.Challenge || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != callback {
			http.Error(w, "invalid grant", 400)
			return
		}
		claims := map[string]any{"iss": p.issuer, "sub": code.Subject, "aud": oidcTestClient, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "nonce": code.Nonce, "email": "developer@example.test", "email_verified": true}
		switch code.Mode {
		case "wrong_nonce":
			claims["nonce"] = "different"
		case "wrong_audience":
			claims["aud"] = "other-client"
		case "wrong_issuer":
			claims["iss"] = "https://other.example.test"
		case "expired":
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		}
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "local-acceptance"})
		payload, _ := json.Marshal(claims)
		input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
		sum := sha256.Sum256([]byte(input))
		signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
		if err != nil {
			http.Error(w, "signing unavailable", 500)
			return
		}
		if code.Mode == "bad_signature" {
			signature[0] ^= 1
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "test-provider-token", "token_type": "Bearer", "expires_in": 60, "id_token": input + "." + base64.RawURLEncoding.EncodeToString(signature)})
	})
	p.server = httptest.NewUnstartedServer(mux)
	if listen != "" {
		p.server.Listener.Close()
		p.server.Listener, err = net.Listen("tcp", listen)
		if err != nil {
			t.Fatal(err)
		}
	}
	p.issuer = publicIssuer
	if p.issuer == "" {
		p.issuer = "http://" + p.server.Listener.Addr().String()
	}
	p.server.Start()
	t.Cleanup(p.server.Close)
	return p
}
func (p *oidcTestProvider) configure(callback, mode string) {
	p.mu.Lock()
	p.callback = callback
	p.mode = mode
	p.mu.Unlock()
}
func oidcTestResponseBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var packet struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&packet); err != nil {
		t.Fatal("identity response is not JSON")
	}
	return strings.TrimSpace(string(packet.Data))
}
