package db

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/pkg/crypto"
)

func TestLegacyConnectionsMigrateWithoutSharingSecrets(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real PostgreSQL not configured")
	}
	ctx := context.Background()
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	schema := fmt.Sprintf("personal_migration_%d", time.Now().UnixNano())
	if _, e = root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	defer root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if _, e = pool.Exec(ctx, `CREATE TABLE schema_migrations(version varchar(255) PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		t.Fatal(e)
	}
	entries, _ := fs.ReadDir(migrationFS, "migrations")
	for _, entry := range entries {
		if entry.Name() >= "0022_" || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, _ := migrationFS.ReadFile("migrations/" + entry.Name())
		if _, e = pool.Exec(ctx, string(raw)); e != nil {
			t.Fatal(entry.Name(), e)
		}
		if _, e = pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.Name()); e != nil {
			t.Fatal(e)
		}
	}
	if e = NewUserRepository(pool).EnsureAdmin(ctx, "legacy-owner", "opaque-hash"); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO sys_users(username,password_hash,role_id) SELECT 'legacy-other','opaque-hash',id FROM sys_roles WHERE code='developer'`); e != nil {
		t.Fatal(e)
	}
	cipher, _ := crypto.NewCipher([]byte("personal-migration-fixture-32-byte-key"))
	secret := "LegacyFixturePassword9!"
	encrypted, _ := cipher.Encrypt(secret)
	var source int64
	if e = pool.QueryRow(ctx, `INSERT INTO sys_connections(user_id,name,type,host,port,database,username,password,ssl_mode) VALUES(1,'legacy-inventory','postgres','fixture.invalid',5432,'inventory','legacy-db-user',$1,'disable') RETURNING id`, encrypted).Scan(&source); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO sys_tickets(title,type,table_name,connection_id,sql_text,risk,status,requester_id,execution_result)
 VALUES('legacy-pending','dml','inventory',$1,'UPDATE inventory SET id=1 WHERE id=2','low','approved',2,NULL),
 ('legacy-result','dml','inventory',$1,'UPDATE inventory SET id=1 RETURNING id','low','executed',2,'{"kind":"write","affected_rows":1,"rows":[["private-fixture"]],"columns":["id"]}'::jsonb)`, source); e != nil {
		t.Fatal(e)
	}
	applied, e := Migrate(ctx, pool)
	if e != nil || len(applied) != 2 || applied[0] != "0022_personal_connections.sql" || applied[1] != "0023_definition_binding_index.sql" {
		t.Fatal("legacy upgrade failed", applied, e)
	}
	var invalid int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM sys_tickets WHERE (title='legacy-pending' AND status<>'rejected') OR (execution_result ? 'rows')`).Scan(&invalid); e != nil || invalid != 0 {
		t.Fatal("legacy shared tickets retained another identity or its result", e)
	}
	again, e := Migrate(ctx, pool)
	if e != nil || len(again) != 0 {
		t.Fatal("migration is not repeatable", e)
	}
	actor := context.WithValue(ctx, auth.ClaimsContextKey, &auth.Claims{Subject: 1, Role: "admin"})
	repo := NewConnectionRepository(pool, cipher)
	owned, password, e := repo.GetSecrets(actor, source)
	if e != nil || password != secret || owned.Definition == nil || owned.Definition.Visibility != "private" {
		t.Fatal("creator lost its personal credential", e)
	}
	other := context.WithValue(ctx, auth.ClaimsContextKey, &auth.Claims{Subject: 2, Role: "developer"})
	if _, _, e = repo.GetSecrets(other, source); e == nil {
		t.Fatal("migration shared the old password")
	}
	defs, e := repo.Definitions(other)
	if e != nil || len(defs) != 0 {
		t.Fatal("migration published the old target", e)
	}
	var leaked int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM sys_source_definitions WHERE config::text LIKE '%'||$1||'%' OR config ?| ARRAY['username','password']`, secret).Scan(&leaked); e != nil || leaked != 0 {
		t.Fatal("published authentication material", e)
	}
}
