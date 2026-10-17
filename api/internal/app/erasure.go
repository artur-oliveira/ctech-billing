package app

import (
	"fmt"
	"log/slog"
	"net/url"

	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/config"
)

// revocationURL is VALKEY_URL with its logical-DB path removed. ctech-account
// writes the token revocation list in DB 0 of the shared server (jwtverify,
// saga protocol § 5). Billing's own cache lives in DB 3, where those keys are
// invisible. Derived rather than configured: the userdata is at its 16 KiB
// ceiling (terraform/README).
func revocationURL(valkeyURL string) (string, error) {
	u, err := url.Parse(valkeyURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("valkey url %q has no host", valkeyURL)
	}
	u.Path, u.RawPath = "", ""
	return u.String(), nil
}

// revocationBackend is the DB 0 connection the verifier checks revoked subjects
// against. It is nil when there is no Valkey: the check is then off, and a
// locked person is still refused by the erasure lock.
func revocationBackend(cfg *config.Config) cache.Backend {
	if cfg.RedisURL == "" {
		return nil
	}
	raw, err := revocationURL(cfg.RedisURL)
	if err != nil {
		slog.Error("VALKEY_URL unusable for the revocation list — revoked tokens are accepted until they expire", "error", err)
		return nil
	}
	backend, err := cache.NewRedisBackend(raw)
	if err != nil {
		slog.Error("valkey DB 0 unavailable — revoked tokens are accepted until they expire", "error", err)
		return nil
	}
	return backend
}
