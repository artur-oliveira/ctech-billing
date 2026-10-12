package byokrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/lock"
	"gopkg.aoctech.app/api-commons/ratelimit"

	"gopkg.aoctech.app/billing/api/internal/crypto"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

const (
	tokenFetchesPerMinute = 3 // below Inter's 5/min (spec § 4.3)
	tokenSafety           = 300 * time.Second
	waitForWinner         = 5 * time.Second
)

type TokenManager struct {
	cache   cache.Backend
	locks   *lock.Locker
	counter ratelimit.Counter
	sealer  *crypto.Sealer
}

func NewTokenManager(c cache.Backend, counter ratelimit.Counter, sealer *crypto.Sealer) *TokenManager {
	return &TokenManager{cache: c, locks: lock.New(c, 30*time.Second), counter: counter, sealer: sealer}
}

func connID(conn *byokconn.Connection) string {
	if conn.Kind == byokconn.KindSandboxShared {
		return "sandbox" // one token for every test tenant
	}
	return conn.ID
}

func tokenKey(conn *byokconn.Connection, set byok.Capability) string {
	mode := "test"
	if conn.Livemode {
		mode = "live"
	}
	return fmt.Sprintf("byok:token:%s:%s:%s:%d:%s", conn.Provider, mode, connID(conn), conn.Version, set)
}

func (m *TokenManager) read(ctx context.Context, key string) (byok.Token, bool) {
	raw, ok, err := m.cache.Get(ctx, key)
	if err != nil || !ok {
		return byok.Token{}, false
	}
	plain, err := m.sealer.Open(string(raw))
	if err != nil {
		return byok.Token{}, false
	}
	var t byok.Token
	if json.Unmarshal([]byte(plain), &t) != nil || t.Value == "" {
		return byok.Token{}, false
	}
	return t, true
}

// Bearer returns a cached token or fetches one: single-flight per key, at most
// three fetches per minute per connection (one key for the shared sandbox).
func (m *TokenManager) Bearer(ctx context.Context, p *LambdaProvider, conn *byokconn.Connection, set byok.Capability) (byok.Token, error) {
	key := tokenKey(conn, set)
	if t, ok := m.read(ctx, key); ok {
		return t, nil
	}
	release, got, err := m.locks.Acquire(ctx, "byok:token-lock:"+key)
	if err != nil {
		return byok.Token{}, fmt.Errorf("%w: token lock", byok.ErrUnavailable)
	}
	if !got {
		deadline := time.Now().Add(waitForWinner)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return byok.Token{}, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			if t, ok := m.read(ctx, key); ok {
				return t, nil
			}
		}
		return byok.Token{}, fmt.Errorf("%w: token fetch in progress elsewhere", byok.ErrUnavailable)
	}
	defer release()
	if t, ok := m.read(ctx, key); ok { // the previous holder finished meanwhile
		return t, nil
	}
	n, err := m.counter.Incr(ctx, "byok:token-rate:"+conn.Provider+":"+connID(conn), time.Minute)
	if err != nil {
		return byok.Token{}, fmt.Errorf("%w: token limiter", byok.ErrUnavailable)
	}
	if n > tokenFetchesPerMinute {
		return byok.Token{}, fmt.Errorf("%w: local token limiter", byok.ErrRateLimited)
	}
	t, err := p.Token(ctx, byok.Credentials{Ref: refPtr(conn)}, set)
	if err != nil {
		return byok.Token{}, err
	}
	ttl := time.Duration(t.ExpiresIn)*time.Second - tokenSafety
	if ttl > 0 {
		plain, _ := json.Marshal(t)
		if sealed, err := m.sealer.Seal(string(plain)); err == nil {
			_ = m.cache.Set(ctx, key, []byte(sealed), int(ttl/time.Second))
		}
	}
	return t, nil
}

func refPtr(conn *byokconn.Connection) *byok.ConnectionRef { r := conn.Ref(); return &r }

// Invalidate drops every cached token of the connection (renewal, disconnect, 401).
func (m *TokenManager) Invalidate(ctx context.Context, conn *byokconn.Connection) {
	for _, set := range []byok.Capability{byok.CapabilityStatement, byok.CapabilityCollection} {
		_ = m.cache.Delete(ctx, tokenKey(conn, set))
	}
}
