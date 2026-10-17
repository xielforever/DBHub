package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrSession = errors.New("会话已失效")

type session struct {
	UserID          int64     `json:"user_id"`
	RefreshID       string    `json:"refresh_id"`
	PasswordVersion string    `json:"password_version"`
	Expires         time.Time `json:"expires"`
}
type SessionStore struct {
	client *redis.Client
	mu     sync.Mutex
	memory map[string]session
}

func NewSessionStore(client *redis.Client) *SessionStore {
	return &SessionStore{client: client, memory: map[string]session{}}
}
func passwordVersion(user *User) string {
	sum := sha256.Sum256([]byte(user.PasswordHash))
	return hex.EncodeToString(sum[:])
}
func (s *SessionStore) Create(ctx context.Context, sid, jti string, user *User, ttl time.Duration) error {
	value := session{UserID: user.ID, RefreshID: jti, PasswordVersion: passwordVersion(user), Expires: time.Now().Add(ttl)}
	if s.client != nil {
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		return s.client.Set(ctx, "dbhub:session:"+sid, raw, ttl).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, v := range s.memory {
		if time.Now().After(v.Expires) {
			delete(s.memory, key)
		}
	}
	s.memory[sid] = value
	return nil
}
func (s *SessionStore) load(ctx context.Context, sid string) (session, error) {
	if sid == "" {
		return session{}, ErrSession
	}
	if s.client != nil {
		raw, e := s.client.Get(ctx, "dbhub:session:"+sid).Bytes()
		if e == redis.Nil {
			return session{}, ErrSession
		}
		if e != nil {
			return session{}, e
		}
		var value session
		if json.Unmarshal(raw, &value) != nil {
			return session{}, ErrSession
		}
		return value, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.memory[sid]
	if !ok {
		return session{}, ErrSession
	}
	return v, nil
}
func (s *SessionStore) Validate(ctx context.Context, claims *Claims, user *User) error {
	value, e := s.load(ctx, claims.SessionID)
	if e != nil {
		return e
	}
	if value.UserID != user.ID || user.ID != claims.Subject || value.PasswordVersion != passwordVersion(user) || time.Now().After(value.Expires) {
		return ErrSession
	}
	return nil
}
func (s *SessionStore) Rotate(ctx context.Context, old *Claims, newID string, user *User, ttl time.Duration) error {
	if s.client != nil {
		result, e := s.client.Eval(ctx, `local raw=redis.call('GET',KEYS[1]);if not raw then return 0 end;local v=cjson.decode(raw);if v.refresh_id~=ARGV[1] or tostring(v.user_id)~=ARGV[2] or v.password_version~=ARGV[3] then return 0 end;v.refresh_id=ARGV[4];v.expires=ARGV[5];redis.call('SET',KEYS[1],cjson.encode(v),'PX',ARGV[6]);return 1`, []string{"dbhub:session:" + old.SessionID}, old.JWTID, old.Subject, passwordVersion(user), newID, time.Now().Add(ttl).UTC().Format(time.RFC3339Nano), ttl.Milliseconds()).Int()
		if e != nil {
			return e
		}
		if result != 1 {
			return ErrSession
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.memory[old.SessionID]
	if !ok || v.RefreshID != old.JWTID || v.PasswordVersion != passwordVersion(user) || v.UserID != user.ID || time.Now().After(v.Expires) {
		return ErrSession
	}
	v.RefreshID = newID
	v.Expires = time.Now().Add(ttl)
	s.memory[old.SessionID] = v
	return nil
}
func (s *SessionStore) Revoke(ctx context.Context, sid string) error {
	if s.client != nil {
		return s.client.Del(ctx, "dbhub:session:"+sid).Err()
	}
	s.mu.Lock()
	delete(s.memory, sid)
	s.mu.Unlock()
	return nil
}
