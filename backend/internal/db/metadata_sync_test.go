package db

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/pkg/crypto"
	"io"
	"os"
	"testing"
	"time"
)

// replyLostTx models an acknowledgement lost after a real PostgreSQL commit.
type replyLostTx struct{ pgx.Tx }

func (t replyLostTx) Commit(ctx context.Context) error {
	if e := t.Tx.Commit(ctx); e != nil {
		return e
	}
	return io.ErrUnexpectedEOF
}

func TestMetadataCommitAcknowledgementLossIsUnknown(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real PostgreSQL not configured")
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, &auth.Claims{Subject: 1, Role: "admin"})
	root, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	schema := fmt.Sprintf("metadata_ack_%d", time.Now().UnixNano())
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
	if _, e = Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	if e = NewUserRepository(pool).EnsureAdmin(ctx, "ack-test", "opaque-test-password-hash"); e != nil {
		t.Fatal(e)
	}
	cipher, _ := crypto.NewCipher([]byte("metadata-ack-test-key-32-bytes-long"))
	conns := NewConnectionRepository(pool, cipher)
	source := &Connection{UserID: 1, Name: "ack-test", Type: "postgres", Host: "test-only", Port: 5432, SSLMode: "disable"}
	if e = conns.Create(ctx, source); e != nil {
		t.Fatal(e)
	}
	repo := NewMetadataRepository(pool)
	batch, e := repo.BeginSync(ctx, source.ID, source.UpdatedAt)
	if e != nil {
		t.Fatal(e)
	}
	defer batch.Close()
	batch.tx = replyLostTx{batch.tx}
	e = batch.Publish(ctx, []*MetaSnapshot{{ConnectionID: source.ID, DatabaseName: "test", SchemaName: "public", TableName: "actually_committed", TableType: "table"}})
	if !errors.Is(e, ErrMetadataPublishUnknown) {
		t.Fatal("commit reply loss was treated as definite rollback", e)
	}
	got, e := repo.GetSnapshot(ctx, source.ID, "test", "public", "actually_committed")
	if e != nil || got == nil {
		t.Fatal("real commit did not publish atomically", e)
	}
	next, e := repo.BeginSync(ctx, source.ID, source.UpdatedAt)
	if e != nil {
		t.Fatal("commit did not release collection lock", e)
	}
	next.Close()
}
