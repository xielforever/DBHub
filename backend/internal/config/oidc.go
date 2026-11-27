package config

import (
	"errors"
	"net/url"
)

// OIDC credentials are deployment configuration and never part of an API DTO.
type OIDCConfig struct{ Issuer, ClientID, ClientSecret, CallbackURL string }

func ValidateOIDC(c OIDCConfig, allowLocalHTTP bool) error {
	if c.Issuer == "" && c.ClientID == "" && c.ClientSecret == "" && c.CallbackURL == "" {
		return nil
	}
	if c.Issuer == "" || c.ClientID == "" || len(c.ClientID) > 255 || len(c.ClientSecret) < 16 || len(c.ClientSecret) > 4096 {
		return errors.New("配置 OIDC 时必须完整提供发行者、客户端标识、客户端密钥和回调地址")
	}
	for index, raw := range []string{c.Issuer, c.CallbackURL} {
		u, err := url.Parse(raw)
		if err != nil || len(raw) > 2048 || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("OIDC 地址无效")
		}
		if u.Scheme != "https" && !(allowLocalHTTP && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
			return errors.New("OIDC 地址必须使用 HTTPS；开发环境仅允许回环 HTTP")
		}
		if index == 1 && u.Path != "/api/v1/auth/sso/callback" {
			return errors.New("OIDC 回调路径必须为 /api/v1/auth/sso/callback")
		}
	}
	return nil
}
