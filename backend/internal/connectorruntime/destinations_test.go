package connectorruntime

import (
	"github.com/user/dbhub/internal/db"
	"testing"
)

func TestProductionTargetsRequireExplicitHostAndPort(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DBHUB_TARGET_ALLOWLIST", "")
	c := &db.Connection{Type: "postgres", Host: "inventory.example.test", Port: 5432}
	if ValidateDestination(c) == nil {
		t.Fatal("production admitted an unapproved destination")
	}
	t.Setenv("DBHUB_TARGET_ALLOWLIST", "inventory.example.test:5432")
	if e := ValidateDestination(c); e != nil {
		t.Fatal(e)
	}
	for _, host := range []string{"127.0.0.1", "169.254.169.254", "backend", "inventory.example.test@backend"} {
		c.Host = host
		if ValidateDestination(c) == nil {
			t.Fatal("unapproved host admitted", host)
		}
	}
	c.Host = "inventory.example.test"
	c.Port = 8080
	if ValidateDestination(c) == nil {
		t.Fatal("allowlist ignored port")
	}
}
