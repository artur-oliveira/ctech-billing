package v1

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/realtime"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/settlement"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Deps is everything the routes need, passed explicitly rather than assembled
// from a container here — wiring belongs in one place, and that place is app.
type Deps struct {
	// BYOK (spec 2026-10-11): bank connections and their statement sync. Nil
	// unmounts the connection routes.
	ByokConnections *services.ByokConnections
	ByokStatement   *services.ByokStatement
	// ChargeGates builds the single charge gate's facts (BYOK spec § 7.1).
	ChargeGates *services.ChargeGates
	// BillingPlanScreen is the console's BYOK plan screen (part C).
	BillingPlanScreen *services.BillingPlanScreen
	// ByokCollector is the BYOK collection rail (spec § 7). Nil when BYOK is
	// not configured: the collection routes are then not mounted.
	ByokCollector *services.ByokCollector
	// ByokWebhooks decides the webhook Lambda's notices; nil unmounts it.
	ByokWebhooks *services.ByokWebhooks
	// ByokSetup is collection setup, webhook registration and the full
	// disconnect (spec § 3.6, § 6.4); nil keeps part A's disconnect.
	ByokSetup *services.ByokCollectionSetup
	// Bills is the bill repository the classification of a bank line settles
	// through (spec § 8.3).
	Bills *repositories.BillRepository

	Customers *repositories.CustomerRepository
	Subs      *repositories.SubscriptionRepository
	Invoices  *repositories.InvoiceRepository
	Usage     *repositories.UsageRepository
	Levels    *repositories.LevelRepository
	// FinancePlan is Finanças → Plano; disabled without a portal organization.
	FinancePlan *services.FinancePlan
	Catalog     *repositories.CatalogRepository
	Credentials *repositories.CredentialRepository
	// Connector admits a merchant's own OAuth client from the console (spec
	// § 5.1). Webhooks is the endpoint store the console manages (§ 5.2).
	Connector *services.CredentialConnector
	Webhooks  *repositories.WebhookRepository
	// WebhookVerifier proves a new endpoint answers its challenge before it is
	// registered (hub.subscribe style).
	WebhookVerifier WebhookVerifier
	// Realtime and RealtimeChanges are the socket gateway and its cursor fallback
	// (docs/specs/2026-10-14-realtime.md). Nil (REALTIME_ENABLED off) leaves both
	// routes unmounted: a 404, exactly as before.
	Realtime        *realtime.Gateway
	RealtimeChanges *realtime.ChangesHandler
	Idempotency     *repositories.IdempotencyRepository
	Organizations   *repositories.OrganizationRepository
	// Payouts pays Pix keys out of the tenant's connected bank account (CTech's own
	// services; internal scopes). Nil unmounts the routes.
	Payouts *services.Payouts
	// Applications is the merchant application lifecycle (spec § 4). Disabled
	// (routes not mounted) without tenant zero.
	Applications *services.Applications
	Audit        *repositories.AuditRepository
	Payments     *repositories.PaymentRepository
	CreditNotes  *repositories.CreditNoteRepository
	Subscriber   *services.Subscriber
	// Invoicer is the console's finalize path: the sweep's own, so an operator
	// issuing a stuck draft produces the same invoice the scheduler would have.
	Invoicer *services.Invoicer
	// Documents renders and serves invoice PDFs. Nil when the deployment has no
	// bucket, and then the download routes are not mounted on either surface.
	Documents *services.Documents
	// Collector is nil when the deployment has no wallet configuration. Every
	// route that collects money is then not mounted at all — a checkout that 404s
	// is a deployment somebody notices, while one that fails after showing a QR
	// code is a customer who thinks they paid.
	Collector *services.Collector
	// Links signs public payment links. Nil disables them, including the console's
	// "send the link" field.
	Links    *services.PayLink
	Verifier *middleware.Verifier
	// Spaces resolves the finance section's space (ADR 0025). Nil disables the
	// finance routes.
	Spaces *space.Resolver
	// The finance section's use cases; used only when Spaces is set.
	FinanceBills *services.FinanceBills
	FinanceJobs  *services.FinanceJobs
	// FinanceInvoices is the posting rule for billing's own invoices (spec
	// § 3.8). The console's credit-note route uses it; nil leaves credit notes
	// out of Finanças.
	FinanceInvoices *services.FinanceInvoices
	// FinanceImports is F6: statement upload and reconciliation.
	FinanceImports *services.FinanceImports
	Recurrences    *repositories.RecurrenceRepository
	Cards          *repositories.CardRepository
	Ledger         *repositories.LedgerRepository
	// SpaceLister feeds the console's space switcher (the same ctech-account
	// client the resolver uses). Informational: the resolver still authorizes.
	SpaceLister spaceLister
	Clock       func() time.Time
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
		documents:  d.Documents,
		customers:  d.Customers,
		subs:       d.Subs,
		invoices:   d.Invoices,
		payments:   d.Payments,
		usage:      d.Usage,
		levels:     d.Levels,
		subscriber: d.Subscriber,
		cat:        d.Catalog,
		clock:      clock,

		db:            d.DB,
		cache:         d.Cache,
		invoicesTable: d.InvoicesTable,
		appVersion:    d.AppVersion,
	}
	if d.Invoicer != nil && d.Invoices != nil && d.Customers != nil {
		h.oneOff = services.NewOneOffInvoices(d.Invoices, d.Customers, d.Invoicer)
	}
	// Only when the routes the links point at are actually mounted. A signed URL
	// for a checkout this deployment does not serve is a 404 published to a paying
	// customer, which is worse than the absent field a consumer already has to
	// handle.
	if checkoutMounted(d) {
		h.links = d.Links
	}

	v1 := app.Group("/v1.0")
	// Both authenticate themselves (the socket by its first frame, the fallback by
	// its Authorization header), so they sit outside every authenticated group.
	if d.Realtime != nil {
		realtime.RegisterGateway(v1, d.Realtime)
	}
	if d.RealtimeChanges != nil {
		realtime.RegisterChanges(v1, d.RealtimeChanges)
	}
	// Both unauthenticated, and neither behind the tenant resolver: a probe has
	// no credential, and the balancer that runs one is not a tenant. What that
	// costs is bounded by the payloads — the report publishes latencies,
	// utilisation and a release id, and never an error string from a dependency
	// (see healthEntry.Output).
	v1.Get("/health", h.health)
	v1.Get("/health-check", h.healthCheck)

	auth := d.Verifier.Middleware()
	registerByokNotify(v1, d, auth, clock)
	tenant := middleware.ResolveTenant(d.Credentials)
	idem := middleware.Idempotency(d.Idempotency, clock)

	// One group per resource rather than a single `v1.Group("", auth, tenant)`.
	// An empty prefix means "every route under /v1", which would silently drag
	// the credential resolver onto the console routes too — and the console has
	// no credential to resolve, so every one of them would 403. Naming the
	// prefixes makes the two surfaces independent of registration order.
	// A suspended live tenant's writes are refused on every M2M route,
	// including those added later (spec § 4.3).
	suspended := middleware.RefuseSuspendedWrites(d.Organizations)
	m2m := func(prefix string) fiber.Router { return v1.Group(prefix, auth, tenant, suspended) }

	customers := m2m("/customers")
	customers.Post("",
		middleware.RequireM2MScope(middleware.ScopeCustomersWrite), idem, h.createCustomer)
	// By user before /:id, so "by-user" is never read as a customer id.
	customers.Get("/by-user/:user_id",
		middleware.RequireM2MScope(middleware.ScopeCustomersRead), h.getCustomerByUser)
	customers.Get("/:id",
		middleware.RequireM2MScope(middleware.ScopeCustomersRead), h.getCustomer)

	subscriptions := m2m("/subscriptions")
	subscriptions.Post("",
		middleware.RequireM2MScope(middleware.ScopeSubscriptionsWrite), idem, h.createSubscription)
	subscriptions.Get("/:id",
		middleware.RequireM2MScope(middleware.ScopeSubscriptionsRead), h.getSubscription)
	subscriptions.Post("/:id/cancel",
		middleware.RequireM2MScope(middleware.ScopeSubscriptionsWrite), idem, h.cancelSubscription)
	// Behind the idempotency middleware and not merely idempotent by design: this
	// route issues an invoice, and the change invoice deliberately carries no
	// generation key (see Subscriber.invoiceChange), so a retried request with no
	// key would bill the proration twice.
	subscriptions.Post("/:id/change",
		middleware.RequireM2MScope(middleware.ScopeSubscriptionsWrite), idem, h.changeSubscription)

	// The catalogue, readable by an integration. It is the same handler the
	// console reads through: an integration rendering a plan picker and an
	// operator looking at the catalogue must see the same prices and the same
	// quota metadata, and one handler is what makes that structural.
	products := m2m("/products")
	products.Get("",
		middleware.RequireM2MScope(middleware.ScopeProductsRead), h.listProducts)
	products.Get("/:id",
		middleware.RequireM2MScope(middleware.ScopeProductsRead), h.getProduct)

	usage := m2m("/usage")
	usage.Post("", middleware.RequireM2MScope(middleware.ScopeUsageWrite), idem, h.reportUsage)
	// Levels are idempotent by the body's key (spec § 6.1), enforced in the
	// repository; the header middleware is deliberately not in front of it.
	usage.Post("/levels", middleware.RequireM2MScope(middleware.ScopeUsageWrite), h.reportLevel)

	invoices := m2m("/invoices")
	// By reference first, so "by-reference" is never read as an invoice id.
	invoices.Get("/by-reference/:reference",
		middleware.RequireM2MScope(middleware.ScopeInvoicesRead), h.getInvoiceByReference)
	if d.CreditNotes != nil {
		// The console's credit note (and "Devolver via Pix") for a service client.
		cn := &consoleHandlers{handlers: h, credit: d.CreditNotes, byok: d.ByokCollector}
		if d.FinanceInvoices != nil {
			cn.finance = d.FinanceInvoices
		}
		invoices.Post("/:id/credit-notes",
			middleware.RequireM2MScope(middleware.ScopeInvoicesWrite), idem, cn.creditInvoiceM2M)
	}
	if h.oneOff != nil {
		invoices.Post("",
			middleware.RequireM2MScope(middleware.ScopeInvoicesWrite), idem, h.createInvoice)
	}
	invoices.Get("/:id",
		middleware.RequireM2MScope(middleware.ScopeInvoicesRead), h.getInvoice)
	invoices.Get("",
		middleware.RequireM2MScope(middleware.ScopeInvoicesRead), h.listInvoices)

	if d.Payouts != nil {
		ph := &payoutHandlers{svc: d.Payouts, clock: clock}
		payouts := m2m("/payouts")
		payouts.Post("", middleware.RequireM2MScope(middleware.ScopePayoutsWrite), idem, ph.create)
		// by-reference before /:id, so "by-reference" is never read as a payout id.
		payouts.Get("/by-reference/:reference", middleware.RequireM2MScope(middleware.ScopePayoutsRead), ph.getByReference)
		payouts.Get("/:id", middleware.RequireM2MScope(middleware.ScopePayoutsRead), ph.get)
	}

	m2m("/entitlements").Get("",
		middleware.RequireM2MScope(middleware.ScopeEntitlementsRead), h.getEntitlements)

	// Finance is registered BEFORE the console group, and the order matters:
	// Fiber mounts a group's handlers as prefix middleware, so the "/console"
	// group's merchant chain (space, role, tenant; mode required)
	// would otherwise also run in front of /console/finance and refuse a person
	// with no organization, and /spaces without a mode. Finance resolves its own
	// space; its handlers end the chain, so the console middleware never runs for
	// them (TestFinanceIsNotBehindTheConsoleTenantResolver).
	registerFinance(v1, d, auth, clock)
	// Bank connections, before the console group for the same reason: an
	// organization with no billing tenant may connect for statement sync (BYOK
	// spec § 3.1), so the merchant tenant resolver must never run in front.
	registerConnections(v1, d, auth, clock)
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
// the security model again: these routes resolve the tenant from the selected
// organization and the person's role in it (ADR 0028), and RequireUserScope rejects a service token on every one of
// them. An M2M credential cannot reach a route that takes the mode from a
// header, which is what keeps the header from becoming a way to cross modes.
func registerConsole(v1 fiber.Router, d Deps, h *handlers, auth fiber.Handler) {
	ch := &consoleHandlers{
		handlers:             h,
		orgs:                 d.Organizations,
		audit:                d.Audit,
		credit:               d.CreditNotes,
		invoicer:             d.Invoicer,
		portalOrganizationID: d.PortalOrganizationID,
		merchants:            d.Organizations,
		applications:         d.Applications,
		creds:                d.Credentials,
		connector:            d.Connector,
		hooks:                d.Webhooks,
		verifier:             d.WebhookVerifier,
		gates:                d.ChargeGates,
		byok:                 d.ByokCollector,
		setup:                d.ByokSetup,
		payments:             d.Payments,
		planScreen:           d.BillingPlanScreen,
	}
	// Assigned only when set: a nil interface value in the field must stay nil.
	if d.SpaceLister != nil {
		ch.spaces = d.SpaceLister
	}
	// Assigned only when set: a nil *FinanceInvoices in the interface field
	// would not compare equal to nil.
	if d.FinanceInvoices != nil {
		ch.finance = d.FinanceInvoices
	}

	// /v1/me carries authentication and nothing else: which tenant this person
	// belongs to is what it answers, so it cannot be behind a tenant resolver,
	// and it publishes only identity, so it is behind no scope.
	v1.Get("/me", auth, ch.me)

	// Before the group, for the reason finance is: the list is what a person
	// picks an organization from, so no organization is resolved yet, and a
	// group's handlers run as prefix middleware for every /console path.
	v1.Get("/console/session", auth,
		middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.session)

	// The billing (merchant) half resolves its tenant from the selected
	// organization (ADR 0028): a person's session, the space and its
	// membership (checked before any table read, the same resolver finance
	// uses), then the owner/admin role and the tenant linked to that
	// organization in the requested mode.
	spaces := d.Spaces
	if spaces == nil {
		// No resolver configured: every organization selector fails closed (503).
		spaces = space.NewResolver(nil, nil)
	}
	// The application runs without a tenant: an organization with none is
	// exactly who applies. Registered before the group for the reason the
	// session is (a group's handlers run as prefix middleware). The mode header
	// is required by ResolveSpace and ignored here (plan decision 6).
	if ch.applications.Enabled() {
		v1.Get("/console/merchant/application", auth, middleware.RequireUserSession(), middleware.ResolveSpace(spaces),
			middleware.RequireMerchantRole(), middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.getMerchantApplication)
		v1.Post("/console/merchant/application", auth, middleware.RequireUserSession(), middleware.ResolveSpace(spaces),
			middleware.RequireMerchantRole(), middleware.RequireUserScope(middleware.ScopeOrganizationWrite),
			middleware.SessionIdempotency(d.Idempotency, h.clock), ch.applyMerchant)
		v1.Get("/console/merchant/companies", auth, middleware.RequireUserSession(), middleware.ResolveSpace(spaces),
			middleware.RequireMerchantRole(), middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.merchantCompanies)
	}

	console := v1.Group("/console", auth,
		middleware.RequireUserSession(),
		middleware.ResolveSpace(spaces),
		middleware.ResolveMerchantTenant(d.Organizations),
		// Every write in the console needs an Idempotency-Key, the M2M rule:
		// a double click or a retried timeout must not void, charge or refund
		// twice, whatever process each request lands on.
		middleware.SessionIdempotency(d.Idempotency, h.clock))

	// C1. Behind the invoices read scope rather than the organization one: every
	// figure on it is an invoice figure, and a token that may not read invoices
	// must not read their totals either.
	console.Get("/overview",
		middleware.RequireUserScope(middleware.ScopeInvoicesRead), ch.overview)

	// C17. The read is behind the organization scope; changing the dunning
	// policy is behind the *invoice write* scope, because what it changes is
	// what happens to unpaid invoices — not what the organization is.
	console.Get("/settings",
		middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.settings)
	// The BYOK plan (part C): what the organization is as a merchant.
	if ch.planScreen != nil {
		console.Get("/plan", middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.billingPlan)
		console.Post("/plan", middleware.RequireUserScope(middleware.ScopeOrganizationWrite), ch.changeBillingPlan)
	}
	// The issuer block is what the invoice PDF is headed by, so changing it is
	// behind the invoice write scope rather than an organization one: it edits
	// what every future document says.
	console.Put("/settings/issuer",
		middleware.RequireUserScope(middleware.ScopeInvoicesWrite), ch.setIssuer)
	console.Put("/settings/dunning",
		middleware.RequireUserScope(middleware.ScopeInvoicesWrite), ch.setDunningPolicy)

	console.Get("/invoices",
		middleware.RequireUserScope(middleware.ScopeInvoicesRead), ch.listInvoices)
	console.Get("/invoices/:id",
		middleware.RequireUserScope(middleware.ScopeInvoicesRead), ch.getInvoice)
	// The document, on the read scope: a PDF of an invoice is a rendering of
	// what this token may already read.
	if d.Documents.Enabled() {
		console.Get("/invoices/:id/pdf",
			middleware.RequireUserScope(middleware.ScopeInvoicesRead), ch.invoicePDF)
	}
	// The three writes on an invoice (C3), each behind the write scope rather
	// than the read one: the token that renders the screen is not the token that
	// can void from it.
	//
	// Behind the group's idempotency middleware like every console write; each
	// is also a no-op when the invoice is already in the state it asks for, and
	// the credit note is refused rather than replayed past the total.
	console.Post("/invoices/:id/finalize",
		middleware.RequireUserScope(middleware.ScopeInvoicesWrite), ch.finalizeInvoice)
	console.Post("/invoices/:id/void",
		middleware.RequireUserScope(middleware.ScopeInvoicesWrite), ch.voidInvoice)
	console.Post("/invoices/:id/credit-notes",
		middleware.RequireUserScope(middleware.ScopeInvoicesWrite), ch.creditInvoice)

	console.Get("/subscriptions",
		middleware.RequireUserScope(middleware.ScopeSubscriptionsRead), ch.listSubscriptions)
	console.Get("/subscriptions/:id",
		middleware.RequireUserScope(middleware.ScopeSubscriptionsRead), ch.getSubscription)
	// The one write on this surface. It is behind the write scope, not the read
	// one — the token that renders the screen is not the token that can cancel
	// from it.
	console.Post("/subscriptions/:id/cancel",
		middleware.RequireUserScope(middleware.ScopeSubscriptionsWrite), ch.cancelSubscription)
	console.Post("/subscriptions/:id/change",
		middleware.RequireUserScope(middleware.ScopeSubscriptionsWrite), ch.changeSubscription)

	console.Post("/customers",
		middleware.RequireUserScope(middleware.ScopeCustomersWrite), ch.createCustomer)
	console.Get("/customers",
		middleware.RequireUserScope(middleware.ScopeCustomersRead), ch.listCustomers)
	console.Get("/customers/:id",
		middleware.RequireUserScope(middleware.ScopeCustomersRead), ch.getCustomer)
	// Revealing a tax id is a POST because it has an effect — it records who
	// looked — and it is behind the *write* scope for the same reason: reading a
	// customer is not being trusted with their CPF.
	console.Post("/customers/:id/tax-id",
		middleware.RequireUserScope(middleware.ScopeCustomersWrite), ch.revealTaxID)

	// The catalogue writes (C8–C9). Behind a write scope that the read-only
	// console session does not hold, and behind `billing:products:write` rather
	// than the invoices one: an operator who may issue a credit note is not
	// thereby somebody who may change what things cost.
	console.Post("/products",
		middleware.RequireUserScope(middleware.ScopeProductsWrite), ch.createProduct)
	console.Post("/prices",
		middleware.RequireUserScope(middleware.ScopeProductsWrite), ch.createPrice)
	// There is no PUT and no PATCH on a price, on any surface. Immutability is
	// the model, and archiving is the only mutation it accepts.
	console.Post("/prices/:id/archive",
		middleware.RequireUserScope(middleware.ScopeProductsWrite), ch.archivePrice)
	// A product's dunning override. Behind the invoice write scope, not the
	// catalogue one: it changes how unpaid bills are chased, and somebody who
	// may set a price is not thereby somebody who may decide when a customer
	// loses access.
	console.Put("/products/:id/details",
		middleware.RequireUserScope(middleware.ScopeProductsWrite), ch.setProductDetails)
	console.Put("/products/:id/dunning",
		middleware.RequireUserScope(middleware.ScopeInvoicesWrite), ch.setProductDunningPolicy)

	console.Get("/products",
		middleware.RequireUserScope(middleware.ScopeProductsRead), ch.listProducts)
	console.Get("/products/:id",
		middleware.RequireUserScope(middleware.ScopeProductsRead), ch.getProduct)

	// The integration tools (spec § 5): credentials and webhooks.
	registerIntegrations(console, ch)

	// BYOK collection (spec 2026-10-11 § 12.1).
	registerCollection(console, ch)

	// Tenant zero's review (spec § 4.2). Any other tenant: 404.
	if ch.applications.Enabled() {
		review := console.Group("/applications", middleware.RequireTenantZero(d.PortalOrganizationID))
		review.Get("", middleware.RequireUserScope(middleware.ScopeOrganizationRead), ch.listApplications)
		review.Post("/:id/approve", middleware.RequireUserScope(middleware.ScopeOrganizationWrite), ch.approveApplication)
		review.Post("/:id/reject", middleware.RequireUserScope(middleware.ScopeOrganizationWrite), ch.rejectApplication)
	}
}

// registerPortal mounts the consumer surface (ADR 0012).
//
// It is the third group and the most narrowly scoped: the organization is
// configuration, the mode is always live, and every read is filtered to the
// signed-in customer rather than merely to the tenant — because in the portal
// every user shares one tenant, so tenant scoping alone would show each of them
// all of the others.
func registerPortal(v1 fiber.Router, d Deps, h *handlers, auth fiber.Handler) {
	ph := &portalHandlers{handlers: h, collector: d.Collector, bus: d.SettlementBus, spaces: d.SpaceLister, portalOrg: d.PortalOrganizationID}
	// Before the group: the list must answer for a person who is not themselves
	// a customer but administers an organization that is (scope decision 12).
	v1.Get("/portal/spaces", auth, middleware.RequireUserScope(middleware.ScopeMySubscriptionsRead), ph.listSpaces)
	identity := middleware.ResolvePortalIdentity(d.Customers, d.PortalOrganizationID, d.Spaces)
	portal := v1.Group("/portal", auth, identity, middleware.SessionIdempotency(d.Idempotency, h.clock))

	portal.Get("/session",
		middleware.RequireUserScope(middleware.ScopeMySubscriptionsRead), ph.session,
	)

	// Accepting the terms is behind the *read* scope, which is the one exception
	// on this surface and is deliberate. Every other write here moves money or a
	// subscription; this one records that a person read a document, and it has to
	// be reachable by exactly the token that rendered the screen asking them to.
	// Requiring a write scope would gate the consent screen behind a permission a
	// read-only session does not hold, and leave that session stuck on a modal it
	// cannot dismiss.
	portal.Post("/terms/accept",
		middleware.RequireUserScope(middleware.ScopeMySubscriptionsRead), ph.acceptTerms)

	portal.Get("/subscriptions",
		middleware.RequireUserScope(middleware.ScopeMySubscriptionsRead), ph.listSubscriptions)
	portal.Get("/subscriptions/:id",
		middleware.RequireUserScope(middleware.ScopeMySubscriptionsRead), ph.getSubscription)

	// At-period-end only, and that is enforced in the handler rather than left to
	// a request field: a consumer cancelling mid-period is asking for money back,
	// which is a credit note and a different decision.
	portal.Post("/subscriptions/:id/cancel",
		middleware.RequireUserScope(middleware.ScopeMySubscriptionsWrite), ph.cancelSubscription)

	portal.Get("/invoices",
		middleware.RequireUserScope(middleware.ScopeMyInvoicesRead), ph.listInvoices)
	portal.Get("/invoices/:id",
		middleware.RequireUserScope(middleware.ScopeMyInvoicesRead), ph.getInvoice)
	if d.Documents.Enabled() {
		portal.Get("/invoices/:id/pdf",
			middleware.RequireUserScope(middleware.ScopeMyInvoicesRead), ph.invoicePDF)
	}

	if d.Collector != nil {
		portal.Post("/invoices/:id/pay",
			middleware.RequireUserScope(middleware.ScopeMyInvoicesWrite), ph.payInvoice)

		// The read side of the same screen: an SSE stream that says when the
		// charge settled. Mounted with the pay route because it exists only to
		// answer it — a deployment with no wallet has nothing to wait for. It
		// takes the *read* scope: watching an invoice is reading it.
		portal.Get("/invoices/:id/events",
			middleware.RequireUserScope(middleware.ScopeMyInvoicesRead), ph.invoiceEvents)
	}
}
