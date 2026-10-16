package target

import "testing"

func TestCompatibilityDistinguishesPatchBranchForkAndPrerelease(t *testing.T) {
	matrix := []TestedVersion{{"mysql", "5.7", "5.7.44"}, {"mysql", "8.0", "8.0.45"}, {"mysql", "8.4", "8.4.11"}, {"postgres", "16", "16.15"}, {"postgres", "18", "18.3"}, {"redis", "7.2", "7.2.13"}}
	for _, item := range []struct{ kind, raw, comment, mode, engine, version, branch, status string }{
		{"mysql", "8.4.11", "MySQL Community Server - GPL", "", "mysql", "8.4.11", "8.4", "verified"},
		{"mysql", "5.7.44-log", "MySQL Community Server (GPL)", "", "mysql", "5.7.44", "5.7", "verified"},
		{"mysql", "8.0.45-commercial", "MySQL Enterprise Server", "", "mysql", "8.0.45", "8.0", "verified"},
		{"mysql", "8.4.12", "", "", "mysql", "8.4.12", "8.4", "branch_verified"},
		{"mysql", "9.0.1", "", "", "mysql", "9.0.1", "9.0", "unverified"},
		{"mysql", "5.5.5-10.11.11-MariaDB", "Debian", "", "mariadb", "10.11.11", "10.11", "unverified"},
		{"mysql", "5.7.25-TiDB-v8.5.2", "", "", "tidb", "8.5.2", "8.5", "unverified"},
		{"mysql", "8.4.11-3", "Percona Server (GPL)", "", "percona", "8.4.11", "8.4", "unverified"},
		{"mysql", "8.4.11-rc1", "", "", "mysql", "8.4.11", "8.4", "unverified"},
		{"postgres", "PostgreSQL 16.15 on aarch64-linux-musl, 64-bit", "", "", "postgres", "16.15", "16", "verified"},
		{"postgres", "PostgreSQL 18.3 on x86_64", "", "", "postgres", "18.3", "18", "verified"},
		{"postgres", "PostgreSQL 16.14", "", "", "postgres", "16.14", "16", "branch_verified"},
		{"postgres", "PostgreSQL 18.3devel on x86_64", "", "", "postgres", "18.3", "18", "unverified"},
		{"postgres", "PostgreSQL 16.15-YugabyteDB", "", "", "yugabytedb", "16.15", "16", "unverified"},
		{"postgres", "PostgreSQL 9.6.24", "", "", "postgres", "9.6.24", "9.6", "unverified"},
		{"redis", "7.2.13", "", "standalone", "redis", "7.2.13", "7.2", "verified"},
		{"redis", "7.2.14", "", "standalone", "redis", "7.2.14", "7.2", "branch_verified"},
		{"redis", "7.2.13", "", "cluster", "redis", "7.2.13", "7.2", "unverified"},
		{"redis", "7.2.13", "valkey", "standalone", "valkey", "7.2.13", "7.2", "unverified"},
		{"mysql", "not-a-version", "", "", "mysql", "", "", "unknown"},
	} {
		t.Run(item.kind+"/"+item.raw+"/"+item.comment+item.mode, func(t *testing.T) {
			got := classifyWithMatrix(item.kind, item.raw, item.comment, item.mode, matrix)
			if got.Engine != item.engine || got.Version != item.version || got.Branch != item.branch || got.Status != item.status || got.Message == "" {
				t.Fatalf("incorrect identity or validation boundary: %+v", got)
			}
		})
	}
}

func TestPendingMatrixEntryIsNotAdvertisedAsVerified(t *testing.T) {
	c := classifyWithMatrix("mysql", "5.7.44", "", "", []TestedVersion{{"mysql", "5.7", ""}})
	if c.Status != "unverified" || c.TestedVersion != "" {
		t.Fatal("pending version was presented as tested")
	}
	one := TestedVersions()
	one[0].TestedVersion = "mutated"
	if TestedVersions()[0].TestedVersion == "mutated" {
		t.Fatal("caller changed embedded evidence")
	}
}
