package db

import (
	"context"
	"os"
	"testing"
	"time"
)

// The metadata pool uses simple protocol. Dollar-quoted SQL must not turn a
// placeholder inside a literal into an attacker-controlled SQL fragment.
func TestMetadataDollarQuotedParametersRemainData(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real PostgreSQL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Connect(ctx, dsn, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	for _, tc := range []struct {
		name, query, literal, input string
	}{
		{"dollar", `SELECT $$literal $1$$::text, $1::text`, "literal $1", `$$; SELECT 'injected'; --`},
		{"tagged", `SELECT $body$literal $1$body$::text, $1::text`, "literal $1", `$body$; SELECT 'injected'; --`},
		{"quoted", `SELECT '$1'::text, $1::text`, "$1", `'; SELECT 'injected'; --`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var literal, input string
			if err := pool.QueryRow(ctx, tc.query, tc.input).Scan(&literal, &input); err != nil {
				t.Fatal(err)
			}
			if literal != tc.literal || input != tc.input {
				t.Fatalf("parameter changed SQL literal or value: literal=%q value=%q", literal, input)
			}
			var value int
			if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&value); err != nil || value != 1 {
				t.Fatalf("connection is unusable after parameter query: value=%d error=%v", value, err)
			}
		})
	}
}
