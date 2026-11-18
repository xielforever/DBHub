package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type oidcClient struct {
	provider *oidc.Provider
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	http     *http.Client
}

func identityURL(raw string, allowLocalHTTP bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || allowLocalHTTP && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

// Discovery, JWT/JWKS validation and key rotation use the maintained OpenID SDK.
// No provider is contacted until an explicitly configured login is started.
func newOIDCClient(ctx context.Context, issuer, clientID, clientSecret, callback string, allowLocalHTTP bool) (*oidcClient, error) {
	if !identityURL(issuer, allowLocalHTTP) || !identityURL(callback, allowLocalHTTP) || clientID == "" {
		return nil, errors.New("SSO 配置无效")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("SSO 后端请求不接受重定向")
	}}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, errors.New("无法读取 SSO 配置")
	}
	endpoint := provider.Endpoint()
	var discovery struct {
		JWKSURL string `json:"jwks_uri"`
	}
	if err = provider.Claims(&discovery); err != nil {
		return nil, errors.New("SSO 发现文档无效")
	}
	if !identityURL(endpoint.AuthURL, allowLocalHTTP) || !identityURL(endpoint.TokenURL, allowLocalHTTP) || !identityURL(discovery.JWKSURL, allowLocalHTTP) {
		return nil, errors.New("SSO 端点必须使用受支持的安全地址")
	}
	return &oidcClient{provider: provider, oauth: oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, RedirectURL: callback, Endpoint: endpoint, Scopes: []string{oidc.ScopeOpenID, "profile"}}, verifier: provider.VerifierContext(ctx, &oidc.Config{ClientID: clientID, SupportedSigningAlgs: []string{"RS256", "ES256"}}), http: client}, nil
}
