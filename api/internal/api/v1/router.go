package v1

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/settlement"
)

// Deps is everything the routes need, passed explicitly rather than assembled
// from a container here — wiring belongs in one place, and that place is app.
type Deps struct {
	Customers     *repositories.CustomerRepository
	Subs          *repositories.SubscriptionRepository
	Invoices      *repositories.InvoiceRepository
	Usage         *repositories.UsageRepository
	Catalog       *repositories.CatalogRepository
	Credentials   *repositories.CredentialRepository
	Idempotency   *repositories.IdempotencyRepository
	Organizations *repositories.OrganizationRepository
	Audit         *repositories.AuditRepository
	Payments      *repositories.PaymentRepository
	Subscriber    *services.Subscriber
	// Collector is nil when the deployment has no wallet configuration. Every
	// route that collects money is then not mounted at all — a checkout that 404s
	// is a deployment somebody notices, while one that fails after showing a QR
	// code is a customer who thinks they paid.
	Collector *services.Collector
	// Links signs public payment links. Nil disables them, including the console's
	// "send the link" field.
	Links    *services.PayLink
	Verifier *middleware.Verifier
	Clock    func() time.Time
	// PortalOrganizationID is tenant zero (ADR 0012). Empty disables the portal:
	// its routes 404 rather than falling back to some other organization.
	PortalOrganizationID string

	// SettlementBus is optional. Absent, the payment stream re-reads the invoice
	// on a short interval instead of waiting for a notification.
	SettlementBus settlement.Bus

	// The health report's inputs. All four may be zero — a test wires the app
	// without them and gets a report that says so rather than one that panics.
	// InvoicesTable is the **physical** name, prefix included, because proving
	// the prefix resolves to a table that exists is half of what the check is
	// for.
	DB            *dynamodb.Client
	Cache         cache.Backend
	InvoicesTable string
	AppVersion    string
}

// Register mounts the v1 routes.
//
// The middleware order is the security model, not a style choice:
//
//  1. requestID — so even a rejected request is traceable.
//  2. auth — the token is valid.
//  3. resolveTenant — the credential says which organization and which mode.
//     Nothing before this may touch tenant data, and nothing after it may take
//     the tenant from the request.
//  4. scope — this credential may do this specific thing.
//  5. idempotency — on every mutating route, without exception.
func Register(app *fiber.App, d Deps) {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	h := &handlers{
		customers:  d.Customers,
		subs:       d.Subs,
		invoices:   d.Invoices,
		usage:      d.Usage,
		subscriber: d.Subscriber,
		clock:      clock,

		db:            d.DB,
		cache:         d.Cache,
		invoicesTable: d.InvoicesTable,
		appVersion:    d.AppVersion,
	}
	// Only when the routes the links point at are actually mounted. A signed URL
	// for a checkout this deployment does not serve is a 404 published to a paying
	// customer, which is worse than the absent field a consumer already has to
	// handle.
	if checkoutMounted(d) {
		h.links = d.Links
	}

	app.Use(middleware.RequestID())

	v1 := app.Group("/v1.0")
	// Both unauthenticated, and neither behind the tenant resolver: a probe has
	// no credential, and the balancer that runs one is not a tenant. What that
	// costs is bounded by the payloads — the report publishes latencies,
	// utilisation and a release id, and never an error string from a dependency
	// (see healthEntry.Output).
	v1.Get("/health", h.health)
	v1.Get("/health-check", h.healthCheck)

	auth := d.Verifier.Middleware()
	tenant := middleware.ResolveTenant(d.Credentials)
	idem := middleware.Idempotency(d.Idempotency, clock)

	// One group per resource rather than a single `v1.Group("", auth, tenant)`.
	// An empty prefix means "every route under /v1", which would silently drag
	// the credential resolver onto the console routes too — and the console has
	// no credential to resolve, so every one of them would 403. Naming the
	// prefixes makes the two surfaces independent of registration order.
	m2m := func(prefix string) fiber.Router { return v1.Group(prefix, auth, tenant) }

	customers := m2m("/customers")
	customers.Post("",
		middleware.RequireM2MScope(middleware.ScopeCustomersWrite), idem, h.createCustomer)
	customers.Get("/:id",
		middleware.RequireM2MScope(middleware.ScopeCustomersRead), h.getCustomer)

	subscriptions := m2m("/subscriptions")
	subscriptions.Post("",
		middleware.RequireM2MScope(middleware.ScopeSubscriptionsWrite), idem, h.createSubscription)
	subscriptions.Get("/:id",
		middleware.RequireM2MScope(middleware.ScopeSubscriptionsRead), h.getSubscription)
	subscriptions.Post("/:id/cancel",
		middleware.RequireM2MScope(middleware.ScopeSubscriptionsWrite), idem, h.cancelSubscription)

	m2m("/usage").Post("",
		middleware.RequireM2MScope(middleware.ScopeUsageWrite), idem, h.reportUsage)

	invoices := m2m("/invoices")
	invoices.Get("/:id",
		middleware.RequireM2MScope(middleware.ScopeInvoicesRead), h.getInvoice)
	invoices.Get("",
		middleware.RequireM2MScope(middleware.ScopeInvoicesRead), h.listInvoices)

	m2m("/entitlements").Get("",
		middleware.RequireM2MScope(middleware.ScopeEntitlementsRead), h.getEntitlements)

	registerConsole(v1, d, h, auth)
	registerPortal(v1, d, h, auth)
	registerCheckout(v1, d, h)
}

// registerCheckout mounts the public payment link and wallet's notify-back.
//
// These are the only routes in the service with no token and no tenant resolver,
// and the whole group is skipped unless collection is actually configured. Each
// carries its own authentication instead: the checkout routes are authenticated
// by the signed token in the path, and the webhook by wallet's HMAC over the
// body it sent.
//
// The webhook lives under /internal so a load-balancer rule can keep it off the
// public listener entirely. That is defence in depth, not the control — the
// signature is the control, because a path that is merely hard to reach is a
// path somebody eventually reaches.
func registerCheckout(v1 fiber.Router, d Deps, h *handlers) {
	if d.Collector == nil {
		return
	}
	ch := &checkoutHandlers{handlers: h, orgs: d.Organizations, collector: d.Collector}

	// The webhook is mounted whenever there is a collector, links or not: a
	// deployment can collect through the portal alone, and a settlement wallet has
	// no route to report is money received that billing never records.
	v1.Post("/internal/webhooks/wallet", ch.webhook)

	if !checkoutMounted(d) {
		return
	}
	v1.Get("/checkout/:token", ch.view)
	v1.Post("/checkout/:token/pay", ch.pay)
}

// checkoutMounted reports a deployment that serves the public payment link.
//
// Both halves are needed and they fail for different reasons: no collector means
// no wallet configuration and so nothing to pay with, and no enabled links means
// no CHECKOUT_LINK_SECRET to sign a token nobody can forge. It is one predicate
// rather than two checks so the routes and the published `checkout_url` cannot
// disagree about whether the checkout exists.
func checkoutMounted(d Deps) bool {
	return d.Collector != nil && d.Links != nil && d.Links.Enabled()
}

// registerConsole mounts the browser surface (ADR 0011).
//
// It is a separate group with its own tenant resolver, and that separation is
// the security model again: these routes resolve the organization from the
// signed-in owner, and RequireUserScope rejects a service token on every one of
// them. An M2M credential cannot reach a route that takes the mode from a
// header, which is what keeps the header from becoming a way to cross modes.
func registerConsole(v1 fiber.Router, d Deps, h *handlers, auth fiber.Handler) {
	ch := &consoleHandlers{
		handlers:             h,
		orgs:                 d.Organizations,
		audit:                d.Audit,
		cat:                  d.Catalog,
		portalOrganizationID: d.PortalOrganizationID,
	}

	// /v1/me carries authentication and nothing else: which tenant this person
	// belongs to is what it answers, so it cannot be behind a tenant resolver,
	// and it publishes only identity, so it is behind no scope.
	v1.Get("/me", auth, ch.me)

	console := v1.Group("/console", auth, middleware.ResolveConsoleTenant(d.Organizations))

	console.Get("/session",
		middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.session)

	console.Get("/invoices",
		middleware.RequireUserScope(middleware.ScopeInvoicesRead), ch.listInvoices)
	console.Get("/invoices/:id",
		middleware.RequireUserScope(middleware.ScopeInvoicesRead), ch.getInvoice)

	console.Get("/subscriptions",
		middleware.RequireUserScope(middleware.ScopeSubscriptionsRead), ch.listSubscriptions)
	console.Get("/subscriptions/:id",
		middleware.RequireUserScope(middleware.ScopeSubscriptionsRead), ch.getSubscription)
	// The one write on this surface. It is behind the write scope, not the read
	// one — the token that renders the screen is not the token that can cancel
	// from it.
	console.Post("/subscriptions/:id/cancel",
		middleware.RequireUserScope(middleware.ScopeSubscriptionsWrite), ch.cancelSubscription)

	console.Get("/customers",
		middleware.RequireUserScope(middleware.ScopeCustomersRead), ch.listCustomers)
	console.Get("/customers/:id",
		middleware.RequireUserScope(middleware.ScopeCustomersRead), ch.getCustomer)

	console.Get("/products",
		middleware.RequireUserScope(middleware.ScopeProductsRead), ch.listProducts)
	console.Get("/products/:id",
		middleware.RequireUserScope(middleware.ScopeProductsRead), ch.getProduct)
}

// registerPortal mounts the consumer surface (ADR 0012).
//
// It is the third group and the most narrowly scoped: the organization is
// configuration, the mode is always live, and every read is filtered to the
// signed-in customer rather than merely to the tenant — because in the portal
// every user shares one tenant, so tenant scoping alone would show each of them
// all of the others.
func registerPortal(v1 fiber.Router, d Deps, h *handlers, auth fiber.Handler) {
	ph := &portalHandlers{handlers: h, cat: d.Catalog, collector: d.Collector, bus: d.SettlementBus}
	identity := middleware.ResolvePortalIdentity(d.Customers, d.PortalOrganizationID)
	portal := v1.Group("/portal", auth, identity)

	portal.Get("/session",
		middleware.RequireUserScope(middleware.ScopeMeSubscriptionsRead), ph.session)

	portal.Get("/subscriptions",
		middleware.RequireUserScope(middleware.ScopeMeSubscriptionsRead), ph.listSubscriptions)
	portal.Get("/subscriptions/:id",
		middleware.RequireUserScope(middleware.ScopeMeSubscriptionsRead), ph.getSubscription)

	// At-period-end only, and that is enforced in the handler rather than left to
	// a request field: a consumer cancelling mid-period is asking for money back,
	// which is a credit note and a different decision.
	portal.Post("/subscriptions/:id/cancel",
		middleware.RequireUserScope(middleware.ScopeMeSubscriptionsWrite), ph.cancelSubscription)

	portal.Get("/invoices",
		middleware.RequireUserScope(middleware.ScopeMeInvoicesRead), ph.listInvoices)
	portal.Get("/invoices/:id",
		middleware.RequireUserScope(middleware.ScopeMeInvoicesRead), ph.getInvoice)

	if d.Collector != nil {
		portal.Post("/invoices/:id/pay",
			middleware.RequireUserScope(middleware.ScopeMeInvoicesWrite), ph.payInvoice)

		// The read side of the same screen: an SSE stream that says when the
		// charge settled. Mounted with the pay route because it exists only to
		// answer it — a deployment with no wallet has nothing to wait for. It
		// takes the *read* scope: watching an invoice is reading it.
		portal.Get("/invoices/:id/events",
			middleware.RequireUserScope(middleware.ScopeMeInvoicesRead), ph.invoiceEvents)
	}
}
