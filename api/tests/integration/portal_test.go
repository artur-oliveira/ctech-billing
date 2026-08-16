//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// The portal surface (ADR 0012). Everyone in the portal shares one tenant, so
// tenant scoping proves nothing here — what these tests check is that each read
// is filtered to the signed-in **customer**, and that nothing internal reaches
// the wire.

// portalEnv is an API whose tenant zero is the environment's organization, plus
// one customer inside it who has a ctech-account subject.
type portalEnv struct {
	*apiEnv
	customer *billing.Customer
	userID   string
}

func newPortal(t *testing.T) *portalEnv {
	t.Helper()
	ctx := ctxT(t)

	// newAPI's organization plays CTech here: tenant zero, the one whose
	// customers the portal serves.
	base := newAPI(t)

	userID := "usr_" + id.New()
	customer := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: base.org.ID, Livemode: true,
		Name: "Pessoa Que Paga", Email: "pessoa@example.com", UserID: userID,
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, customer, now()); err != nil {
		t.Fatal(err)
	}

	return &portalEnv{apiEnv: base, customer: customer, userID: userID}
}

// withPortal rebuilds the Fiber app with PORTAL_ORGANIZATION_ID set, since the
// portal's tenant is configuration rather than a request value.
func (e *portalEnv) withPortal(t *testing.T) {
	t.Helper()
	cfg := *testCfg
	cfg.DynamoDBEndpoint = mustEnv(t, "DYNAMODB_ENDPOINT")
	cfg.CtechIssuerURL = testIssuer
	cfg.CtechJWKSURL = e.jwksURL
	cfg.ServiceAudience = testAudience
	cfg.PortalOrganizationID = e.org.ID

	server, err := app.Build(ctxT(t), &cfg, func() time.Time { return now() })
	if err != nil {
		t.Fatal(err)
	}
	e.app = server
}

// newInvoiceFor is a draft with the addressee named, which is what the portal
// filters on and therefore the only thing these tests need to vary.
func newInvoiceFor(t *testing.T, org *billing.Organization, customerID string) *billing.Invoice {
	t.Helper()
	return newDraftInvoiceFor(t, org, customerID, "")
}

// newSubscriptionInvoice is the same draft bound to a subscription.
//
// Anything testing what a payment does to *service* needs this one rather than
// newInvoiceFor: a one-off invoice gates nothing, so it can never show a
// subscription being activated or recovered — and a test written on one would
// pass whether or not that code exists.
func newSubscriptionInvoice(t *testing.T, org *billing.Organization, sub *billing.Subscription) *billing.Invoice {
	t.Helper()
	return newDraftInvoiceFor(t, org, sub.CustomerID, sub.ID)
}

func newDraftInvoiceFor(t *testing.T, org *billing.Organization, customerID, subscriptionID string) *billing.Invoice {
	t.Helper()
	inv := &billing.Invoice{
		SubscriptionID: subscriptionID,
		ID:             id.NewWithPrefix(id.PrefixInvoice),
		OrganizationID: org.ID,
		Livemode:       org.Livemode,
		CustomerID:     customerID,
		Status:         billing.InvoiceDraft,
		Period:         marchPeriod(),
		Currency:       billing.CurrencyBRL,
		Total:          4990,
		Subtotal:       4990,
	}
	items := []billing.InvoiceItem{{Description: "DF-e Basic", Period: marchPeriod(), Quantity: 1, UnitAmount: 4990, Amount: 4990}}
	if err := repositories.NewInvoiceRepository(testDB, testCfg).
		Create(ctxT(t), inv, items, "gen_"+id.New(), now()); err != nil {
		t.Fatal(err)
	}
	return inv
}

func (e *portalEnv) portalToken(t *testing.T, scopes ...string) string {
	t.Helper()
	return e.token(t, e.userID, "sess_"+id.New(), scopes...)
}

func TestPortalResolvesTheCustomerFromTheAccount(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMySubscriptionsRead)

	res := e.console(t, "/v1.0/portal/session", token, "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		CustomerID string `json:"customer_id"`
		Name       string `json:"name"`
	}
	res.decode(t, &body)
	if body.CustomerID != e.customer.ID {
		t.Fatalf("customer = %q, want %q", body.CustomerID, e.customer.ID)
	}
}

// Somebody with a valid CTech account who has never bought anything is not a
// portal user, and the answer must not depend on why.
func TestPortalRejectsANonCustomer(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	stranger := e.token(t, "usr_"+id.New(), "sess_"+id.New(), middleware.ScopeMySubscriptionsRead)

	res := e.console(t, "/v1.0/portal/session", stranger, "")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	// Typed, because the portal renders this one as an empty state rather than
	// an error — somebody who has bought nothing yet did nothing wrong. The type
	// is the contract; `detail` is prose and gets rewritten.
	if got := problemType(t, res.body); got != problem.TypeNoBillingAccount {
		t.Errorf("type = %q, want %q", got, problem.TypeNoBillingAccount)
	}
}

// problemType reads the `type` out of an RFC 7807 body.
func problemType(t *testing.T, body []byte) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("body is not a problem document: %v\n%s", err, body)
	}
	return p.Type
}

// An unconfigured portal is a portal that does not exist here — 404, not a
// fallback to whichever organization happens to be first.
func TestPortalIsAbsentWithoutTenantZero(t *testing.T) {
	e := newPortal(t) // built by newAPI, which sets no PORTAL_ORGANIZATION_ID
	token := e.portalToken(t, middleware.ScopeMySubscriptionsRead)

	res := e.console(t, "/v1.0/portal/session", token, "")
	if res.status != http.StatusNotFound {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

func TestServiceTokensAreRejectedOnPortalRoutes(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	m2m := e.token(t, e.client, "", middleware.ScopeMySubscriptionsRead)

	res := e.console(t, "/v1.0/portal/session", m2m, "")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	// **Not** the empty-account type. That one is rendered as a welcome, and a
	// service token arriving on a person's route is the opposite of a welcome —
	// it is a caller in the wrong place, and it has to keep looking like one.
	if got := problemType(t, res.body); got == problem.TypeNoBillingAccount {
		t.Error("a machine token was told it merely has no billing account yet")
	}
}

// The console's scopes must not open the portal and vice versa: a consumer token
// must never be one scope away from a merchant's customer list.
func TestPortalAndConsoleScopesDoNotOverlap(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)

	consoleScoped := e.portalToken(t, middleware.ScopeInvoicesRead, middleware.ScopeCustomersRead)
	if res := e.console(t, "/v1.0/portal/invoices", consoleScoped, ""); res.status != http.StatusForbidden {
		t.Fatalf("console scopes opened the portal: %d %s", res.status, res.body)
	}

	portalScoped := e.token(t, e.org.OwnerUserID, "sess_"+id.New(), middleware.ScopeMyInvoicesRead)
	if res := e.console(t, "/v1.0/console/invoices", portalScoped, "live"); res.status != http.StatusForbidden {
		t.Fatalf("portal scopes opened the console: %d %s", res.status, res.body)
	}
}

// The portal shares one tenant across every consumer, so this is the test that
// matters: one customer must not see another's invoices.
func TestPortalShowsOnlyTheSignedInCustomersInvoices(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMyInvoicesRead)

	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	mine := newInvoiceFor(t, e.org, e.customer.ID)
	theirs := newInvoiceFor(t, e.org, "cus_someone_else")
	due := brcal.New(2026, time.March, 10)
	for _, inv := range []*billing.Invoice{mine, theirs} {
		if _, err := invoices.Finalize(ctx, inv, due, due, "test", "req_1", now()); err != nil {
			t.Fatal(err)
		}
	}

	res := e.console(t, "/v1.0/portal/invoices", token, "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var page struct {
		Data []struct {
			ID    string `json:"id"`
			State string `json:"state"`
			Tone  string `json:"tone"`
		} `json:"data"`
	}
	res.decode(t, &page)

	var sawMine bool
	for _, inv := range page.Data {
		if inv.ID == theirs.ID {
			t.Fatal("another customer's invoice appeared in the portal")
		}
		if inv.ID == mine.ID {
			sawMine = true
			if inv.State == "" || inv.Tone == "" {
				t.Error("an invoice reached the portal without a phrase and a tone")
			}
		}
	}
	if !sawMine {
		t.Fatal("the customer's own invoice is missing")
	}
}

// Reading somebody else's invoice by id is 404, not 403: every portal user is in
// the same tenant, so a 403 would confirm the id exists.
func TestPortalHidesAnotherCustomersInvoice(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMyInvoicesRead)

	theirs := newInvoiceFor(t, e.org, "cus_someone_else")
	due := brcal.New(2026, time.March, 10)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).
		Finalize(ctx, theirs, due, due, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	res := e.console(t, "/v1.0/portal/invoices/"+theirs.ID, token, "")
	if res.status != http.StatusNotFound {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// The rule from PRODUCT.md, enforced on the wire rather than in the UI: no
// internal status, no metadata, no audit trail reaches a consumer.
func TestPortalPayloadCarriesNoInternalVocabulary(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMyInvoicesRead)

	inv := newInvoiceFor(t, e.org, e.customer.ID)
	due := brcal.New(2026, time.March, 10)
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).
		Finalize(ctx, inv, due, due, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	res := e.console(t, "/v1.0/portal/invoices/"+inv.ID, token, "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	body := string(res.body)
	for _, forbidden := range []string{"OPEN", "DRAFT", "PAID", "VOID", "UNCOLLECTIBLE", "metadata", "timeline", "organization_id"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the portal published %q:\n%s", forbidden, body)
		}
	}

	var payload map[string]any
	if err := json.Unmarshal(res.body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["state"] == "" || payload["tone"] == "" {
		t.Error("the invoice reached the portal without a phrase and a tone")
	}
}

// /v1.0/me answers which shells this person can open, without either resolver.
func TestMeReportsBothIdentities(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)

	// The customer is not an operator.
	consumer := e.portalToken(t)
	res := e.console(t, "/v1.0/me", consumer, "")
	if res.status != http.StatusOK {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
	var body struct {
		Portal  *struct{ ID string } `json:"portal"`
		Console *struct{ ID string } `json:"console"`
	}
	res.decode(t, &body)
	if body.Portal == nil || body.Portal.ID != e.customer.ID {
		t.Fatalf("portal identity = %+v, want customer %s", body.Portal, e.customer.ID)
	}
	if body.Console != nil {
		t.Fatalf("a customer who owns no organization must not get a console identity: %+v", body.Console)
	}

	// The owner is an operator and, in this environment, not a customer.
	operator := e.token(t, e.org.OwnerUserID, "sess_"+id.New())
	res = e.console(t, "/v1.0/me", operator, "")
	res.decode(t, &body)
	if body.Console == nil || body.Console.ID != e.org.ID {
		t.Fatalf("console identity = %+v, want organization %s", body.Console, e.org.ID)
	}
}

func TestMeRejectsServiceTokens(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	m2m := e.token(t, e.client, "")

	res := e.console(t, "/v1.0/me", m2m, "")
	if res.status != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.status, res.body)
	}
}

// One account is one customer per organization. Without the conditional write,
// the second create wins and somebody's portal starts showing another person's
// invoices.
func TestOneAccountCannotBeTwoCustomers(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	customers := repositories.NewCustomerRepository(testDB, testCfg)

	duplicate := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true,
		Name: "Outro Cadastro", UserID: e.userID,
	}
	err := customers.Create(ctx, duplicate, now())
	if err == nil {
		t.Fatal("a second customer claimed an account that was already taken")
	}
	if !strings.Contains(err.Error(), e.userID) {
		t.Errorf("the error must name the subject that was taken: %v", err)
	}
}

// The terms gate, server side.
//
// The gate is a screen, not a security control — the routes behind it stay
// scoped and reachable, because refusing to show somebody a bill until they
// re-read a document is withholding a bill they still owe. What the server owes
// is an honest answer about whether they have agreed, and a record of when.
func TestPortalReportsAndRecordsTermsAcceptance(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMySubscriptionsRead)

	var before struct {
		TermsAccepted bool `json:"terms_accepted"`
	}
	e.console(t, "/v1.0/portal/session", token, "").decode(t, &before)
	if before.TermsAccepted {
		t.Fatal("a new customer must not read as having accepted anything")
	}

	var after struct {
		TermsAccepted bool `json:"terms_accepted"`
	}
	res := e.do(t, http.MethodPost, "/v1.0/portal/terms/accept", token, "", "")
	if res.status != http.StatusOK {
		t.Fatalf("accept: %d %s", res.status, res.body)
	}
	res.decode(t, &after)
	if !after.TermsAccepted {
		t.Fatal("accepting did not change the answer")
	}

	// Persisted, not merely echoed. The response could be right and the row
	// wrong, and the row is what the next visit reads.
	var reread struct {
		TermsAccepted bool `json:"terms_accepted"`
	}
	e.console(t, "/v1.0/portal/session", token, "").decode(t, &reread)
	if !reread.TermsAccepted {
		t.Error("the acceptance did not survive the request that recorded it")
	}

	// Consent nobody can evidence later is the only kind that matters.
	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctxT(t), e.org.ID, true, e.customer.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range trail {
		if entry.Action == "customer.terms_accepted" {
			found = true
			if entry.After != billing.CurrentTermsVersion {
				t.Errorf("audit records version %q, want %q", entry.After, billing.CurrentTermsVersion)
			}
		}
	}
	if !found {
		t.Errorf("no audit entry for the acceptance: %+v", trail)
	}
}

// Accepting twice is a double-click, not two agreements. A second audit row
// would suggest somebody was asked twice and answered twice.
func TestAcceptingTermsTwiceRecordsOneAgreement(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMySubscriptionsRead)

	for range 2 {
		if res := e.do(t, http.MethodPost, "/v1.0/portal/terms/accept", token, "", ""); res.status != http.StatusOK {
			t.Fatalf("accept: %d %s", res.status, res.body)
		}
	}

	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctxT(t), e.org.ID, true, e.customer.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range trail {
		if entry.Action == "customer.terms_accepted" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d acceptance entries, want 1", n)
	}
}
