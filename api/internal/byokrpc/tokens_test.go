package byokrpc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ratelimit"

	"gopkg.aoctech.app/billing/api/internal/crypto"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

func sealer(t *testing.T) *crypto.Sealer {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	return crypto.NewSealer(base64.StdEncoding.EncodeToString(k))
}

func setup(t *testing.T) (*fakeInvoker, *TokenManager, Session, cache.Backend) {
	inv := &fakeInvoker{fake: byoktest.New()}
	c := cache.NewMemoryBackend(1000)
	counter := ratelimit.NewMemoryCounter()
	m := NewTokenManager(c, counter, sealer(t))
	conn := &byokconn.Connection{AccountOrganizationID: "org", Livemode: true, Provider: "inter", ID: "c1", Version: 1,
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, Sealed: "b1.x"}
	s := Session{P: NewOutbound(inv, "fn").For("inter", true), Conn: conn, Tokens: m, Limits: NewLimits(counter)}
	return inv, m, s, c
}

func TestFiftyConcurrentCallersFetchOneToken(t *testing.T) {
	inv, _, s, _ := setup(t)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Call(context.Background(), s, byok.CapabilityStatement, func(c byok.Credentials, tok byok.Token) (byok.Balance, error) {
				return s.P.Balance(context.Background(), c, tok, byok.Date{})
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if inv.tokens.Load() != 1 {
		t.Fatalf("%d token fetches, want 1", inv.tokens.Load())
	}
}

func TestTheLimiterNeverLetsAFourthFetchOut(t *testing.T) {
	inv, m, s, c := setup(t)
	for i := range 5 {
		_ = c.DeletePrefix(context.Background(), "byok:token:")
		_, err := m.Bearer(context.Background(), s.P, s.Conn, byok.CapabilityStatement)
		if i < 3 && err != nil {
			t.Fatalf("fetch %d: %v", i+1, err)
		}
		if i >= 3 && !errors.Is(err, byok.ErrRateLimited) {
			t.Fatalf("fetch %d: %v, want rate limited", i+1, err)
		}
	}
	if inv.tokens.Load() != 3 {
		t.Fatalf("%d fetches reached the provider, want 3", inv.tokens.Load())
	}
}

func TestA401RefreshesOnceAndRetriesOnce(t *testing.T) {
	inv, _, s, _ := setup(t)
	inv.unauthorized.Store(1)
	if _, err := Call(context.Background(), s, byok.CapabilityStatement, func(c byok.Credentials, tok byok.Token) (byok.Balance, error) {
		return s.P.Balance(context.Background(), c, tok, byok.Date{})
	}); err != nil {
		t.Fatal(err)
	}
	if inv.tokens.Load() != 2 {
		t.Fatalf("%d fetches: one, then the forced refresh", inv.tokens.Load())
	}
	inv.unauthorized.Store(2)
	if _, err := Call(context.Background(), s, byok.CapabilityStatement, func(c byok.Credentials, tok byok.Token) (byok.Balance, error) {
		return s.P.Balance(context.Background(), c, tok, byok.Date{})
	}); !errors.Is(err, byok.ErrUnauthorized) {
		t.Fatalf("a second 401 is a failure: %v", err)
	}
}

func TestTheCachedTokenIsSealed(t *testing.T) {
	_, m, s, c := setup(t)
	if _, err := m.Bearer(context.Background(), s.P, s.Conn, byok.CapabilityStatement); err != nil {
		t.Fatal(err)
	}
	raw, ok, _ := c.Get(context.Background(), tokenKey(s.Conn, byok.CapabilityStatement))
	if !ok || !crypto.IsSealed(string(raw)) {
		t.Fatalf("cached value %q is not sealed", raw)
	}
}

func TestTheSandboxSharedLimiterAnswersBusy(t *testing.T) {
	_, _, s, _ := setup(t)
	s.Conn.Kind = byokconn.KindSandboxShared
	for i := range 9 {
		err := s.Limits.Pix(context.Background(), s.Conn)
		if i < 8 && err != nil || i == 8 && !errors.Is(err, ErrSandboxBusy) {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
}
