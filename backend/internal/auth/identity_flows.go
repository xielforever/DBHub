package auth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"sync"
	"time"
)

type identityFlowValue struct {
	Encrypted string
	Expires   time.Time
}
type identityFlows struct {
	security *mfaSecurity
	client   *redis.Client
	mu       sync.Mutex
	memory   map[string]identityFlowValue
}

func (f *identityFlows) key(kind, token string) string {
	return "dbhub:identity-flow:" + f.security.digest(kind, token)
}
func (f *identityFlows) put(ctx context.Context, kind, token string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encrypted, err := f.security.cipher.Encrypt(kind + ":" + string(raw))
	if err != nil {
		return err
	}
	key := f.key(kind, token)
	if f.client != nil {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		ok, err := f.client.SetNX(ctx, key, encrypted, ttl).Result()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("identity flow collision")
		}
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for k, v := range f.memory {
		if !v.Expires.After(now) {
			delete(f.memory, k)
		}
	}
	if len(f.memory) >= 4096 {
		return errors.New("identity flow capacity reached")
	}
	f.memory[key] = identityFlowValue{encrypted, now.Add(ttl)}
	return nil
}
func (f *identityFlows) consume(ctx context.Context, kind, token string, out any) error {
	key := f.key(kind, token)
	var encrypted string
	if f.client != nil {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		var err error
		encrypted, err = f.client.GetDel(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			return ErrMFAInvalid
		}
		if err != nil {
			return err
		}
	} else {
		f.mu.Lock()
		value, ok := f.memory[key]
		delete(f.memory, key)
		f.mu.Unlock()
		if !ok || !value.Expires.After(time.Now()) {
			return ErrMFAInvalid
		}
		encrypted = value.Encrypted
	}
	plain, err := f.security.cipher.Decrypt(encrypted)
	if err != nil {
		return ErrMFAInvalid
	}
	prefix := kind + ":"
	if len(plain) < len(prefix) || plain[:len(prefix)] != prefix {
		return ErrMFAInvalid
	}
	if err = json.Unmarshal([]byte(plain[len(prefix):]), out); err != nil {
		return ErrMFAInvalid
	}
	return nil
}
