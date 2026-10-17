package app

import (
	"errors"
	"log/slog"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// revocationURL is VALKEY_URL with its logical-DB path removed. ctech-account
// writes the token revocation list in DB 0 of the shared server (jwtverify,
// saga protocol § 5). Billing's own cache lives in DB 3, where those keys are
// invisible. Derived rather than configured: the userdata is at its 16 KiB
// ceiling (terraform/README).
func revocationURL(valkeyURL string) (string, error) {
	// The URL can carry the password, so no error here quotes it, and url.Parse's
	// own error (which does) is dropped.
	u, err := url.Parse(valkeyURL)
	if err != nil {
		return "", errors.New("valkey url is not parseable")
	}
	if u.Host == "" {
		return "", errors.New("valkey url has no host")
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

// portalInvoicesURL is the portal's invoice list, where a person settles what
// blocks their erasure. Derived from CHECKOUT_BASE_URL's origin, because both
// pages are the same static site (ADR 0013), and so the userdata carries
// nothing new.
func portalInvoicesURL(checkoutBaseURL string) string {
	u, err := url.Parse(checkoutBaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/invoices"
}

// newEraser is the one way the API and the consumer build the Eraser, so both
// look for the same customer in the same tenant.
func newEraser(db *dynamodb.Client, cfg *config.Config) *services.Eraser {
	return services.NewEraser(
		repositories.NewCustomerRepository(db, cfg),
		repositories.NewSubscriptionRepository(db, cfg),
		repositories.NewInvoiceRepository(db, cfg),
		repositories.NewOrganizationRepository(db, cfg),
		repositories.NewCatalogRepository(db, cfg),
		repositories.NewLevelRepository(db, cfg),
		cfg.PortalOrganizationID,
		portalInvoicesURL(cfg.CheckoutBaseURL),
	)
}
