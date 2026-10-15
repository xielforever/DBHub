package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestSessionRotationRevocationAndPasswordChanges(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			var client *redis.Client
			if backend == "redis" {
				u := os.Getenv("TEST_REDIS_URL")
				if u == "" {
					t.Skip("TEST_REDIS_URL not configured")
				}
				opt, e := redis.ParseURL(u)
				if e != nil {
					t.Fatal("invalid Redis URL")
				}
				client = redis.NewClient(opt)
				defer client.Close()
			}
			store := NewSessionStore(client)
			user := &User{ID: 991, Username: "session-test", Role: "admin", PasswordHash: "initial-hash"}
			secret := []byte("unit-test-only-signing-key-longer-than-32")
			pair, e := IssuePair(secret, user, time.Minute, time.Hour)
			if e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			if e = store.Create(ctx, pair.SessionID, pair.RefreshID, user, time.Hour); e != nil {
				t.Fatal(e)
			}
			defer store.Revoke(ctx, pair.SessionID)
			access, e := ParseToken(secret, pair.AccessToken, TokenTypeAccess)
			if e != nil {
				t.Fatal(e)
			}
			if e = store.Validate(ctx, access, user); e != nil {
				t.Fatal(e)
			}
			old, e := ParseToken(secret, pair.RefreshToken, TokenTypeRefresh)
			if e != nil {
				t.Fatal(e)
			}
			next, e := issuePairForSession(secret, user, time.Minute, time.Hour, pair.SessionID)
			if e != nil {
				t.Fatal(e)
			}
			if e = store.Rotate(ctx, old, next.RefreshID, user, time.Hour); e != nil {
				t.Fatal(e)
			}
			if e = store.Rotate(ctx, old, "replay", user, time.Hour); !errors.Is(e, ErrSession) {
				t.Fatal("used refresh token accepted")
			}
			changed := *user
			changed.PasswordHash = "new-hash"
			if e = store.Validate(ctx, access, &changed); !errors.Is(e, ErrSession) {
				t.Fatal("old session survived password change")
			}
			if e = store.Revoke(ctx, pair.SessionID); e != nil {
				t.Fatal(e)
			}
			if e = store.Validate(ctx, access, user); !errors.Is(e, ErrSession) {
				t.Fatal("revoked access token accepted")
			}
		})
	}
}
func TestRedisFailureDoesNotAuthenticate(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer client.Close()
	store := NewSessionStore(client)
	e := store.Validate(context.Background(), &Claims{SessionID: "unavailable", Subject: 1}, &User{ID: 1})
	if e == nil || errors.Is(e, ErrSession) {
		t.Fatal("infrastructure failure must remain distinct from missing session")
	}
}
