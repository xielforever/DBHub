module github.com/user/dbhub

go 1.23.0

replace gopkg.in/yaml.v3 => github.com/go-yaml/yaml/v3 v3.0.1

replace golang.org/x/crypto => github.com/golang/crypto v0.39.0

replace golang.org/x/sys => github.com/golang/sys v0.32.0

replace golang.org/x/text => github.com/golang/text v0.24.0

replace golang.org/x/sync => github.com/golang/sync v0.13.0

replace golang.org/x/term => github.com/golang/term v0.31.0

require (
	github.com/go-sql-driver/mysql v1.9.3
	github.com/jackc/pgx/v5 v5.7.4
	github.com/redis/go-redis/v9 v9.7.3
	golang.org/x/crypto v0.0.0-00010101000000-000000000000
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace gopkg.in/check.v1 => github.com/go-check/check v0.0.0-20161208181325-20d25e280405

replace go.uber.org/atomic => github.com/uber-go/atomic v1.11.0

replace filippo.io/edwards25519 => github.com/FiloSottile/edwards25519 v1.1.0
