package connectorruntime

import (
	"testing"

	"github.com/user/dbhub/internal/db"
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

func TestDestinationCIDR(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DBHUB_TARGET_ALLOWLIST", "inventory.example.test:5432, 10.240.0.0/16, 2001:db8:42::/48")
	for _, tc := range []struct {
		name    string
		host    string
		port    int
		allowed bool
	}{
		{"first IP", "10.240.0.0", 5432, true},
		{"last IP on another port", "10.240.255.255", 3306, true},
		{"below range", "10.239.255.255", 5432, false},
		{"above range", "10.241.0.0", 5432, false},
		{"IPv6 in range", "2001:db8:42::1", 6379, true},
		{"IPv6 outside range", "2001:db8:43::1", 6379, false},
		{"exact hostname", "INVENTORY.EXAMPLE.TEST.", 5432, true},
		{"wrong port for exact hostname", "inventory.example.test", 3306, false},
		{"unlisted hostname", "other.example.test", 5432, false},
		{"numeric DNS name", "10.240.1.2.", 5432, false},
		{"invalid port", "10.240.1.2", 65536, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDestination(&db.Connection{Host: tc.host, Port: tc.port})
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v, error=%v", tc.allowed, err)
			}
		})
	}
}

func TestDestinationCIDRChecksProxy(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DBHUB_TARGET_ALLOWLIST", "10.240.0.0/16")
	conn := &db.Connection{
		Host:  "10.240.1.2",
		Port:  5432,
		Proxy: &db.Proxy{Host: "10.240.2.3", Port: 3128},
	}
	if err := ValidateDestination(conn); err != nil {
		t.Fatal(err)
	}
	conn.Proxy.Host = "10.241.2.3"
	if err := ValidateDestination(conn); err == nil {
		t.Fatal("proxy outside the allowlist admitted")
	}
}
