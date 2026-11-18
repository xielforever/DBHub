package auth

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/user/dbhub/internal/auditctx"
	"github.com/user/dbhub/internal/config"
	"github.com/user/dbhub/internal/httpx"
	"golang.org/x/oauth2"
)

type ssoSecurity struct {
	config     config.OIDCConfig
	allowLocal bool
	configured bool
	flows      *identityFlows
	mu         sync.Mutex
	client     *oidcClient
}
type ssoAuthorization struct{ Verifier, Nonce, Redirect string }
type ssoHandoff struct {
	Username        string
	UserID          int64
	PasswordVersion string
}

func newSSOSecurity(cfg *config.Config, security *mfaSecurity, sessions *SessionStore) *ssoSecurity {
	return &ssoSecurity{config: cfg.OIDC, allowLocal: !cfg.IsProduction(), configured: cfg.OIDC.Issuer != "" && config.ValidateOIDC(cfg.OIDC, !cfg.IsProduction()) == nil, flows: &identityFlows{security: security, client: sessions.client, memory: map[string]identityFlowValue{}}}
}
func (s *ssoSecurity) provider(ctx context.Context) (*oidcClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		client, err := newOIDCClient(ctx, s.config.Issuer, s.config.ClientID, s.config.ClientSecret, s.config.CallbackURL, s.allowLocal)
		if err != nil {
			return nil, err
		}
		s.client = client
	}
	return s.client, nil
}
func (h *Handler) SSOInfo(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, map[string]any{"configured": h.sso.configured, "label": "企业单点登录"})
}
func (h *Handler) ssoCookie(w http.ResponseWriter, name, value string, maxAge int) {
	u, _ := url.Parse(h.sso.config.CallbackURL)
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/api/v1/auth/sso", HttpOnly: true, Secure: u != nil && u.Scheme == "https", SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func ssoReturnPath(value string) string {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return ""
	}
	target, err := url.Parse(value)
	if err != nil || strings.HasPrefix(target.Path, "//") || strings.ContainsRune(target.Path, '\\') || strings.IndexFunc(target.Path, unicode.IsControl) >= 0 {
		return ""
	}
	return value
}

func (h *Handler) SSOStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.sso.configured {
		httpx.Fail(w, httpx.NotFound("此环境尚未配置企业单点登录"))
		return
	}
	if _, ok := h.users.(OIDCStore); !ok {
		identityFailure(w)
		return
	}
	if !h.factorBudget(w, r, "sso-start:"+ClientIP(r, h.mfa.limiter.trusted)) {
		return
	}
	client, err := h.sso.provider(r.Context())
	if err != nil {
		identityFailure(w)
		return
	}
	state, err := randomIdentityToken(32)
	if err != nil {
		identityFailure(w)
		return
	}
	nonce, err := randomIdentityToken(32)
	if err != nil {
		identityFailure(w)
		return
	}
	flow := ssoAuthorization{Verifier: oauth2.GenerateVerifier(), Nonce: nonce, Redirect: ssoReturnPath(r.URL.Query().Get("redirect"))}
	if err = h.sso.flows.put(r.Context(), "oidc-state", state, flow, 5*time.Minute); err != nil {
		identityFailure(w)
		return
	}
	h.ssoCookie(w, "dbhub_sso_state", state, 300)
	http.Redirect(w, r, client.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(flow.Verifier)), http.StatusSeeOther)
}
func (h *Handler) ssoResult(w http.ResponseWriter, r *http.Request, result, redirect string) {
	u, _ := url.Parse(h.sso.config.CallbackURL)
	if u == nil || u.Host == "" {
		httpx.Fail(w, httpx.Unauthorized("单点登录失败，请重新开始"))
		return
	}
	u.Path = "/login"
	u.RawPath = ""
	query := url.Values{"sso": {result}}
	if redirect != "" {
		query.Set("redirect", redirect)
	}
	u.RawQuery = query.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}
func (h *Handler) SSOCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.sso.configured {
		httpx.Fail(w, httpx.NotFound("此环境尚未配置企业单点登录"))
		return
	}
	var redirect string
	fail := func(reason string) {
		auditctx.Update(r.Context(), func(d *auditctx.Data) { d.AuthFailure = reason })
		h.ssoResult(w, r, "failed", redirect)
	}
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("dbhub_sso_state")
	if err != nil || len(state) != 43 || !hmac.Equal([]byte(cookie.Value), []byte(state)) {
		fail("sso_state_invalid")
		return
	}
	h.ssoCookie(w, "dbhub_sso_state", "", -1)
	var flow ssoAuthorization
	if err = h.sso.flows.consume(r.Context(), "oidc-state", state, &flow); err != nil {
		fail("sso_state_expired")
		return
	}
	redirect = flow.Redirect
	code := r.URL.Query().Get("code")
	if code == "" || len(code) > 4096 || r.URL.Query().Get("error") != "" {
		fail("sso_authorization_denied")
		return
	}
	client, err := h.sso.provider(r.Context())
	if err != nil {
		fail("sso_provider_unavailable")
		return
	}
	ctx := oidc.ClientContext(r.Context(), client.http)
	token, err := client.oauth.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		fail("sso_exchange_failed")
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) > 65536 {
		fail("sso_id_token_missing")
		return
	}
	verified, err := client.verifier.Verify(ctx, raw)
	if err != nil || !hmac.Equal([]byte(verified.Nonce), []byte(flow.Nonce)) || verified.Subject == "" || len(verified.Subject) > 255 {
		fail("sso_id_token_invalid")
		return
	}
	store, ok := h.users.(OIDCStore)
	if !ok {
		fail("sso_identity_unavailable")
		return
	}
	u, err := store.FindExternalIdentity(ctx, h.sso.config.Issuer, verified.Subject)
	if err != nil {
		fail("sso_identity_unbound")
		return
	}
	if !identityCheckpoint(w, r, u) {
		return
	}
	ticket, err := randomIdentityToken(32)
	if err != nil {
		identityFailure(w)
		return
	}
	if err = h.sso.flows.put(ctx, "oidc-handoff", ticket, ssoHandoff{u.Username, u.ID, PasswordVersion(u)}, time.Minute); err != nil {
		identityFailure(w)
		return
	}
	h.ssoCookie(w, "dbhub_sso_ticket", ticket, 60)
	h.ssoResult(w, r, "complete", redirect)
}
func (h *Handler) SSOComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.sso.configured || r.Header.Get("X-DBHub-CSRF") != "1" {
		httpx.Fail(w, httpx.Unauthorized("单点登录交接无效，请重新开始"))
		return
	}
	cookie, err := r.Cookie("dbhub_sso_ticket")
	if err != nil || len(cookie.Value) != 43 {
		httpx.Fail(w, httpx.Unauthorized("单点登录交接已失效，请重新开始"))
		return
	}
	h.ssoCookie(w, "dbhub_sso_ticket", "", -1)
	var handoff ssoHandoff
	if err = h.sso.flows.consume(r.Context(), "oidc-handoff", cookie.Value, &handoff); err != nil {
		if !errors.Is(err, ErrMFAInvalid) {
			identityFailure(w)
			return
		}
		httpx.Fail(w, httpx.Unauthorized("单点登录交接已失效，请重新开始"))
		return
	}
	u, err := h.users.FindByUsername(r.Context(), handoff.Username)
	if err != nil {
		userFailure(w, err, "身份不可用")
		return
	}
	if u.ID != handoff.UserID || PasswordVersion(u) != handoff.PasswordVersion {
		httpx.Fail(w, httpx.Unauthorized("身份权限已变化，请重新开始"))
		return
	}
	h.finishAuthentication(w, r, u)
}
