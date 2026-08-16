// Package app wires the billing API. It is the only place that knows how the
// pieces fit together; everything else takes its dependencies as arguments.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"gopkg.aoctech.app/api-commons/cache"

	v1 "gopkg.aoctech.app/billing/api/internal/api/v1"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/oauthresource"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/provision"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/settlement"
	"gopkg.aoctech.app/billing/api/internal/wallet"
)

// Build assembles the Fiber app and every repository and service behind it.
//
// It takes an explicit clock so integration tests can pin "today" — a billing
// service tested against the wall clock passes in March and fails in February.
func Build(ctx context.Context, cfg *config.Config, clock func() time.Time) (*fiber.App, error) {
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}

	customers := repositories.NewCustomerRepository(db, cfg)
	subs := repositories.NewSubscriptionRepository(db, cfg)
	invoices := repositories.NewInvoiceRepository(db, cfg)
	usage := repositories.NewUsageRepository(db, cfg)
	catalog := repositories.NewCatalogRepository(db, cfg)
	creds := repositories.NewCredentialRepository(db, cfg)
	idem := repositories.NewIdempotencyRepository(db, cfg)
	orgs := repositories.NewOrganizationRepository(db, cfg)
	audit := repositories.NewAuditRepository(db, cfg)
	payments := repositories.NewPaymentRepository(db, cfg)

	invoicer := services.NewInvoicer(subs, invoices, catalog, usage)
	subscriber := services.NewSubscriber(subs, catalog, invoicer)

	cacheBackend := newCache(cfg)

	// The settlement bus is optional and shares Valkey with the JWKS cache. A
	// deployment without one still works: the payment stream falls back to
	// re-reading the invoice, which is what it did before this existed.
	var bus settlement.Bus
	if cfg.RedisURL != "" {
		if vb, err := settlement.NewValkeyBus(cfg.RedisURL); err != nil {
			slog.Error("valkey unavailable — the payment screen will poll instead of being notified", "error", err)
		} else {
			bus = vb
		}
	}

	// Collection is wired only when wallet is fully configured. A half-configured
	// one leaves Collector nil, which unmounts every route that collects money —
	// see v1.registerCheckout for why that is the safe direction.
	var collector *services.Collector
	walletCfg := wallet.Config{
		BaseURL:       cfg.WalletBaseURL,
		TokenURL:      cfg.WalletTokenURL,
		ClientID:      cfg.WalletClientID,
		ClientSecret:  cfg.WalletClientSecret,
		WebhookSecret: cfg.WalletWebhookSecret,
		Cache:         cacheBackend,
	}
	if walletCfg.Enabled() {
		collector = services.NewCollector(invoices, payments, customers, orgs, wallet.New(walletCfg)).
			WithSettlementBus(bus)
	} else {
		slog.Warn("wallet not configured — checkout and payment routes are not mounted")
	}

	links := services.NewPayLink(cfg.CheckoutLinkSecret, cfg.CheckoutBaseURL)
	if !links.Enabled() {
		slog.Warn("CHECKOUT_LINK_SECRET not set — public payment links are disabled")
	}

	app := newFiber(cfg)

	// RFC 9728, before the v1 routes and outside them: a client asks this to
	// find out which authorization server issues tokens for billing and which
	// scopes exist, and it has to be able to ask before it holds one.
	oauthresource.Register(app, cfg.ServiceAudience, cfg.CtechIssuerURL)

	v1.Register(app, v1.Deps{
		Customers:   customers,
		Subs:        subs,
		Invoices:    invoices,
		Usage:       usage,
		Catalog:     catalog,
		Credentials: creds,
		Idempotency: idem,

		Organizations: orgs,
		Audit:         audit,
		Payments:      payments,

		Subscriber: subscriber,
		Collector:  collector,
		Links:      links,
		Verifier:   middleware.NewVerifier(cfg.CtechJWKSURL, cfg.ServiceAudience, cfg.CtechIssuerURL, cacheBackend),
		Clock:      clock,

		PortalOrganizationID: cfg.PortalOrganizationID,
		SettlementBus:        bus,

		DB:    db,
		Cache: cacheBackend,
		// The physical name, resolved the same way every repository resolves it,
		// so the health report proves the prefix this deployment is actually
		// configured with — not a name assembled a second way that could agree
		// with nothing.
		InvoicesTable: repositories.TableName(cfg, repositories.TableInvoices),
		AppVersion:    cfg.AppVersion,
	})
	return app, nil
}

// BuildInvoicer wires only what the daily sweep needs.
//
// The sweep is a separate binary, so it must not build the HTTP app: no Fiber,
// no JWKS verifier, no idempotency store, and above all no listener. A
// cross-tenant job that opens a port is one misconfigured security group away
// from being a cross-tenant endpoint.
func BuildInvoicer(ctx context.Context, cfg *config.Config) (*services.Invoicer, error) {
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return services.NewInvoicer(
		repositories.NewSubscriptionRepository(db, cfg),
		repositories.NewInvoiceRepository(db, cfg),
		repositories.NewCatalogRepository(db, cfg),
		repositories.NewUsageRepository(db, cfg),
	), nil
}

// BuildProvisioner wires only what applying a tenant plan needs.
//
// Four repositories and nothing else. The seed is the one process that writes
// without a credential behind it, so it is given the narrowest possible set:
// it cannot reach an invoice, a payment or an audit row even by mistake.
func BuildProvisioner(ctx context.Context, cfg *config.Config) (provision.Repos, error) {
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return provision.Repos{}, err
	}
	return provision.Repos{
		Organizations: repositories.NewOrganizationRepository(db, cfg),
		Credentials:   repositories.NewCredentialRepository(db, cfg),
		Catalog:       repositories.NewCatalogRepository(db, cfg),
		Webhooks:      repositories.NewWebhookRepository(db, cfg),
	}, nil
}

// BuildDeliverer wires only what the outbound webhook job needs.
//
// One repository: the delivery job reads endpoints, events and deliveries, and
// nothing else. It never reads an invoice — the payload is an id and a type by
// design (billing.Payload), so this process has no reason to hold the data it
// would otherwise be trusted not to send.
func BuildDeliverer(ctx context.Context, cfg *config.Config) (*services.Deliverer, error) {
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return services.NewDeliverer(repositories.NewWebhookRepository(db, cfg)), nil
}

// BuildDunner wires only what the dunning job needs.
//
// It refuses to start without a sender address, and that refusal is the point:
// the policy's later steps restrict a service and cancel a subscription, and
// running those without the reminders that precede them is how a customer who
// was never contacted loses access.
func BuildDunner(ctx context.Context, cfg *config.Config) (*services.Dunner, error) {
	links := services.NewPayLink(cfg.CheckoutLinkSecret, cfg.CheckoutBaseURL)
	if !links.Enabled() {
		return nil, fmt.Errorf("dunning needs CHECKOUT_LINK_SECRET: a reminder with no way to pay is not worth sending")
	}
	mail, err := email.New(ctx, cfg.AWSRegion, cfg.EmailFrom)
	if err != nil {
		return nil, fmt.Errorf("dunning needs EMAIL_FROM: %w", err)
	}
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return services.NewDunner(
		repositories.NewInvoiceRepository(db, cfg),
		repositories.NewSubscriptionRepository(db, cfg),
		repositories.NewCustomerRepository(db, cfg),
		links,
		mail,
	), nil
}

// BuildCollector wires only what the reconciliation job needs.
//
// Same discipline as BuildInvoicer, and one addition: this job cannot run at all
// without wallet, so an incomplete configuration is a hard failure here rather
// than the warning it is in Build. The API degrades by unmounting its checkout
// routes; a reconciler that started and reconciled nothing would report success
// every hour while payments sat unsettled.
func BuildCollector(ctx context.Context, cfg *config.Config) (*services.Collector, error) {
	walletCfg := wallet.Config{
		BaseURL:       cfg.WalletBaseURL,
		TokenURL:      cfg.WalletTokenURL,
		ClientID:      cfg.WalletClientID,
		ClientSecret:  cfg.WalletClientSecret,
		WebhookSecret: cfg.WalletWebhookSecret,
		Cache:         newCache(cfg),
	}
	if !walletCfg.Enabled() {
		return nil, fmt.Errorf("reconciliation needs wallet configured (WALLET_BASE_URL, WALLET_TOKEN_URL, WALLET_CLIENT_ID, WALLET_CLIENT_SECRET, WALLET_WEBHOOK_SECRET)")
	}
	db, err := newDynamoDB(ctx, cfg)
	if err != nil {
		return nil, err
	}
	collector := services.NewCollector(
		repositories.NewInvoiceRepository(db, cfg),
		repositories.NewPaymentRepository(db, cfg),
		repositories.NewCustomerRepository(db, cfg),
		repositories.NewOrganizationRepository(db, cfg),
		wallet.New(walletCfg),
	)
	// The reconciler settles invoices too — that is its whole job — and somebody
	// may well have the payment screen open when it does. Without this, the
	// settlement that arrives through reconciliation is the one case that still
	// waits for a re-read.
	if cfg.RedisURL != "" {
		if bus, err := settlement.NewValkeyBus(cfg.RedisURL); err != nil {
			slog.Error("valkey unavailable — reconciled settlements will not be announced", "error", err)
		} else {
			collector = collector.WithSettlementBus(bus)
		}
	}
	return collector, nil
}

func newFiber(cfg *config.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.IdleTimeout) * time.Second,
		// Every unhandled error becomes an RFC 7807 body. Fiber's default is a
		// plain-text message, which a client cannot parse and which leaks
		// whatever the error string happened to say.
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if p := problem.FromError(err); p != nil {
				if p.Status >= 500 {
					slog.Error("request failed",
						"error", err,
						"request_id", middleware.GetRequestID(c),
						"path", c.Path())
				}
				return p.Send(c)
			}
			return c.SendStatus(fiber.StatusNoContent)
		},
	})
	app.Use(recover.New())
	return app
}

func newDynamoDB(ctx context.Context, cfg *config.Config) (*dynamodb.Client, error) {
	awsConf, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	return dynamodb.NewFromConfig(awsConf, func(o *dynamodb.Options) {
		if cfg.DynamoDBEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.DynamoDBEndpoint)
		}
	}), nil
}

func newCache(cfg *config.Config) cache.Backend {
	if cfg.RedisURL == "" {
		slog.Warn("VALKEY_URL not set — JWKS cache is per-instance")
		return cache.NewMemoryBackend(100)
	}
	backend, err := cache.NewRedisBackend(cfg.RedisURL)
	if err != nil {
		slog.Error("valkey unavailable — falling back to an in-process JWKS cache", "error", err)
		return cache.NewMemoryBackend(100)
	}
	return backend
}
