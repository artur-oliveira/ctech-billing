package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/jwtverify"
)

type downBackend struct{ cache.Backend }

func (downBackend) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("valkey down")
}

// TestVerifierMiddlewareRevocationPolicy drives the real Verifier.Middleware
// (real RS256 token, real JWKS) with the unrevoked guard on: a revoked token
// is refused on reads and writes, and a dead store lets reads through and
// answers writes 503.
func TestVerifierMiddlewareRevocationPolicy(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	defer jwks.Close()

	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "u1", "sid": "s1", "iss": "iss", "aud": []string{"aud"}, "token_use": "access",
		"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "k1"
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}

	revoked := cache.NewMemoryBackend(16)
	if err := jwtverify.Revoke(context.Background(), revoked, "u1", now, time.Hour); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		store  cache.Backend
		method string
		want   int
	}{
		{"revoked write", revoked, http.MethodPost, 401},
		{"revoked read", revoked, http.MethodGet, 401},
		{"clean write", cache.NewMemoryBackend(16), http.MethodPost, 200},
		{"store down, read open", downBackend{}, http.MethodGet, 200},
		{"store down, write closed", downBackend{}, http.MethodPost, 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVerifier(jwks.URL, "aud", "iss", cache.NewMemoryBackend(16)).
				WithErasureLock(func(context.Context, string) (bool, error) { return false, nil }).
				WithUnrevokedGuard(tc.store)
			app := fiber.New()
			app.Use(v.Middleware())
			app.All("/x", func(c fiber.Ctx) error { return c.SendStatus(200) })
			req := httptest.NewRequest(tc.method, "/x", nil)
			req.Header.Set("Authorization", "Bearer "+signed)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}
