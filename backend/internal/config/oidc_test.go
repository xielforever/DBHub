package config

import "testing"

func TestOIDCConfigurationBoundary(t *testing.T) {
	if ValidateOIDC(OIDCConfig{}, false) != nil {
		t.Fatal("disabled SSO rejected")
	}
	valid := OIDCConfig{Issuer: "https://identity.example.test", ClientID: "dbhub", ClientSecret: "test-only-client-secret", CallbackURL: "https://dbhub.example.test/api/v1/auth/sso/callback"}
	if ValidateOIDC(valid, false) != nil {
		t.Fatal("valid HTTPS configuration rejected")
	}
	for _, mutate := range []func(*OIDCConfig){
		func(c *OIDCConfig) { c.ClientSecret = "" }, func(c *OIDCConfig) { c.CallbackURL = "https://dbhub.example.test/other" },
		func(c *OIDCConfig) { c.Issuer = "http://identity.example.test" }, func(c *OIDCConfig) { c.Issuer = "https://user:secret@identity.example.test" },
		func(c *OIDCConfig) { c.CallbackURL += "?next=https://other.example.test" }, func(c *OIDCConfig) { c.Issuer += "#fragment" },
	} {
		c := valid
		mutate(&c)
		if ValidateOIDC(c, false) == nil {
			t.Fatal("unsafe SSO configuration accepted")
		}
	}
	local := valid
	local.Issuer = "http://127.0.0.1:18083"
	local.CallbackURL = "http://localhost:15173/api/v1/auth/sso/callback"
	if ValidateOIDC(local, true) != nil || ValidateOIDC(local, false) == nil {
		t.Fatal("loopback exception must be development-only")
	}
}
