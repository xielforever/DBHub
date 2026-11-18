package auth

import (
	"encoding/json"
	"testing"
)

func TestCapabilityClaimsCannotImportIAMFromJWT(t *testing.T) {
	var c Claims
	if e := json.Unmarshal([]byte(`{"sub":3,"role":"readonly","capabilities":["report_review"],"IAM":{"mode":"COMPAT_ACTIVE","legacy_data_role":"admin"}}`), &c); e != nil {
		t.Fatal(e)
	}
	if c.IAM != nil || len(c.Capabilities) != 0 || HasCapability(&c, "report_review") {
		t.Fatal("client supplied trusted workspace state")
	}
	c.Role = "admin"
	if HasCapability(&c, "report.authorize") || HasCapability(&c, "invented") || !HasCapability(&c, "report_review") {
		t.Fatal("admin responsibility boundary changed")
	}
	if HasCapability(nil, "audit_view") || HasCapability(&Claims{Role: "admin"}, "audit_view") {
		t.Fatal("missing actor accepted")
	}
}
