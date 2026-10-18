package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/config"
)

// Both budgets are checked and reserved atomically across application instances.
// Fixed windows do not extend the lockout whenever a blocked request arrives.
var loginBudget = redis.NewScript(`
local retry=0
for i=1,2 do
  if tonumber(redis.call('GET',KEYS[i]) or '0')>=tonumber(ARGV[i]) then
    local ttl=redis.call('PTTL',KEYS[i])
    if ttl<1 then redis.call('PEXPIRE',KEYS[i],ARGV[3]);ttl=tonumber(ARGV[3]) end
    retry=math.max(retry,ttl)
  end
end
if retry>0 then return retry end
for i=1,2 do
  if redis.call('INCR',KEYS[i])==1 then redis.call('PEXPIRE',KEYS[i],ARGV[3]) end
end
return 0`)

type loginCount struct {
	count   int
	expires time.Time
}
type loginLimiter struct {
	client      *redis.Client
	secret      []byte
	account, ip int
	window      time.Duration
	trusted     []netip.Prefix
	mu          sync.Mutex
	counts      map[string]loginCount
	now         func() time.Time
}

func newLoginLimiter(cfg *config.Config, client *redis.Client) *loginLimiter {
	account, ip, window := cfg.LoginMaxAttempts, cfg.LoginIPMaxAttempts, cfg.LoginWindow
	if cfg.IsProduction() {
		if account == 0 {
			account = 10
		}
		if ip == 0 {
			ip = 60
		}
		if window == 0 {
			window = 5 * time.Minute
		}
	}
	if account < 1 || ip < 1 || window <= 0 {
		return nil
	}
	return &loginLimiter{client: client, secret: cfg.SecretKey, account: account, ip: ip, window: window, trusted: cfg.TrustedProxyCIDRs, counts: map[string]loginCount{}, now: time.Now}
}

func (l *loginLimiter) key(kind, value string) string {
	hash := hmac.New(sha256.New, l.secret)
	hash.Write([]byte(kind + "\x00" + value))
	// A shared hash tag permits the two-key script on a Redis cluster as well.
	namespace := sha256.Sum256(l.secret)
	return "dbhub:login:{" + hex.EncodeToString(namespace[:8]) + "}:" + hex.EncodeToString(hash.Sum(nil))
}
func (l *loginLimiter) allow(ctx context.Context, username string, request *http.Request) (time.Duration, error) {
	if l == nil {
		return 0, nil
	}
	keys := []string{l.key("account", strings.ToLower(username)), l.key("ip", ClientIP(request, l.trusted))}
	if l.client != nil {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		wait, err := loginBudget.Run(ctx, l.client.WithTimeout(500*time.Millisecond), keys, l.account, l.ip, l.window.Milliseconds()).Int64()
		return time.Duration(wait) * time.Millisecond, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for key, count := range l.counts {
		if !count.expires.After(now) {
			delete(l.counts, key)
		}
	}
	if len(l.counts) > 4096 {
		return 0, errors.New("login budget capacity exceeded")
	}
	var wait time.Duration
	for i, key := range keys {
		limit := l.account
		if i == 1 {
			limit = l.ip
		}
		count := l.counts[key]
		if count.count >= limit && count.expires.Sub(now) > wait {
			wait = count.expires.Sub(now)
		}
	}
	if wait > 0 {
		return wait, nil
	}
	for _, key := range keys {
		count := l.counts[key]
		if count.count == 0 {
			count.expires = now.Add(l.window)
		}
		count.count++
		l.counts[key] = count
	}
	return 0, nil
}

// Forwarded addresses are considered only while walking explicitly trusted
// proxy hops from right to left. Client-supplied headers cannot bypass budgets.
func ClientIP(r *http.Request, prefixes []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	peer = peer.Unmap()
	trusted := func(ip netip.Addr) bool {
		for _, prefix := range prefixes {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !trusted(peer) {
		return peer.String()
	}
	header := r.Header.Get("X-Forwarded-For")
	if len(header) > 2048 {
		return peer.String()
	}
	hops := strings.Split(header, ",")
	if len(hops) > 32 {
		return peer.String()
	}
	for i := len(hops) - 1; i >= 0 && trusted(peer); i-- {
		value, e := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if e != nil {
			break
		}
		peer = value.Unmap()
	}
	return peer.String()
}
