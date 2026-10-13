//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/smithy-go/middleware"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	mw "gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type collectMode int

const (
	collectLive       collectMode = iota // live BYOK tenant, merchant connection, entitled
	collectTest                          // test BYOK tenant on the shared sandbox
	collectTestWallet                    // test tenant on the wallet rail
)

// collectScopes is a console session that may also configure collection.
var collectScopes = append(append([]string{}, byokScopes...), mw.ScopeOrganizationWrite)

// collectEnv is a BYOK-collecting tenant (spec 2026-10-11 § 7) behind the
// real app, plus the same services built directly (the jobs and the
// confirmation have no route by design) against the same fake bank.
type collectEnv struct {
	*byokAPI
	tz        string
	livemode  bool
	tenant    *billing.Organization
	customer  *billing.Customer
	sp        space.ResolvedSpace
	conn      *byokconn.Connection
	byok      *services.ByokCollector
	collector *services.Collector
	invoicer  *services.Invoicer
	conns     *services.ByokConnections
	statement *services.ByokStatement
	gates     *services.ChargeGates
	orgs      *repositories.OrganizationRepository
	payments  *repositories.PaymentRepository
	invoices  *repositories.InvoiceRepository
	repo      *repositories.ByokRepository
	webhooks  *services.ByokWebhooks
	merchant  merchantFixture
	hookID    string
	secret    string
	cfg       config.Config
}

func newByokAPIWith(t *testing.T, configure func(*config.Config)) *byokAPI {
	t.Helper()
	f := byoktest.New()
	calls := &atomic.Int64{}
	app.ByokInvoker = newFakeInvoker(f)
	app.DynamoDBAPIOptions = []func(*middleware.Stack) error{byokTableCalls(calls)}
	t.Cleanup(func() { app.ByokInvoker, app.DynamoDBAPIOptions = nil, nil })
	logs := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	e := newAPIWith(t, func(c *config.Config) {
		c.ByokOutboundFunction = "fake"
		if configure != nil {
			configure(c)
		}
	})
	calls.Store(0)
	return &byokAPI{apiEnv: e, fake: f, byokCalls: calls, logs: logs}
}

func newCollectEnv(t *testing.T, mode collectMode) *collectEnv {
	t.Helper()
	ctx := ctxT(t)
	tz, _ := seedTenantZero(t)
	var cfg config.Config
	e := newByokAPIWith(t, func(c *config.Config) {
		c.PortalOrganizationID = tz
		c.ByokHooksHost = "billing-byok-hooks-test.aoctech.app"
		cfg = *c
	})
	live := mode == collectLive
	orgs := repositories.NewOrganizationRepository(testDB, testCfg)
	test := &billing.Organization{ID: e.org.ID, DisplayName: e.org.DisplayName, Livemode: false, PayoutStatus: billing.PayoutEnabled,
		OwnerUserID: e.org.OwnerUserID, AccountOrganizationID: e.accountOrg}
	if err := orgs.Create(ctx, test, now()); err != nil {
		t.Fatal(err)
	}
	merchant := withOrgCustomer(t, tz, e.accountOrg, e.org, test)
	merchant.entitle(t)
	tenant := test
	if live {
		tenant = e.org
	}
	env := &collectEnv{byokAPI: e, tz: tz, livemode: live, tenant: tenant, orgs: orgs, cfg: cfg, merchant: merchant,
		payments: repositories.NewPaymentRepository(testDB, testCfg), invoices: repositories.NewInvoiceRepository(testDB, testCfg),
		repo: repositories.NewByokRepository(testDB, testCfg)}
	env.wire(t)
	sp, err := space.ForJob(e.accountOrg, live)
	if err != nil {
		t.Fatal(err)
	}
	env.sp = sp
	if mode != collectTestWallet {
		env.connect(t)
		if err := orgs.SetPayoutRail(ctx, tenant, billing.RailBYOK, "inter", billing.PayoutEnabled, "user:test", "req", now()); err != nil {
			t.Fatal(err)
		}
	}
	env.customer = env.newCustomer(t, "52998224725")
	return env
}

// wire builds the services the way app.Build does, against the same fake.
func (e *collectEnv) wire(t *testing.T) {
	t.Helper()
	_, ledger, st, conns := byokServices(t, e.byokAPI.fakeInvoker())
	customers := repositories.NewCustomerRepository(testDB, testCfg)
	subs := hookedSubs(e.tz)
	e.gates = services.NewChargeGates(e.repo, services.NewBillingPlans(customers, subs,
		repositories.NewCatalogRepository(testDB, testCfg), e.tz))
	finance := services.NewFinanceInvoices(repositories.NewBillRepository(testDB, testCfg), e.orgs, customers, e.invoices,
		repositories.NewCreditNoteRepository(testDB, testCfg), e.tz).WithByok(services.ByokPostingFrom(e.payments, e.repo))
	settler := services.NewCollector(e.invoices, e.payments, customers, e.orgs, subs, nil).WithFinance(finance).WithGates(e.gates)
	e.byok = services.NewByokCollector(e.invoices, e.payments, customers, e.orgs, e.repo, e.gates, conns.Session, settler)
	e.collector = services.NewCollector(e.invoices, e.payments, customers, e.orgs, subs, nil).WithFinance(finance).
		WithGates(e.gates).WithByok(e.byok)
	e.invoicer = services.NewInvoicer(subs, e.invoices, repositories.NewCatalogRepository(testDB, testCfg),
		repositories.NewUsageRepository(testDB, testCfg)).WithOrganizations(e.orgs).WithCharges(e.byok)
	st.WithLinker(services.NewByokLinker(e.byok, e.payments, repositories.NewBillRepository(testDB, testCfg), ledger).WithClock(now))
	e.conns, e.statement = conns, st
	e.webhooks = services.NewByokWebhooks(e.repo, e.payments, e.byok, nil)
	_ = ledger
}

func (b *byokAPI) fakeInvoker() *fakeInvoker { return app.ByokInvoker.(*fakeInvoker) }

// connect gives the organization its connection: a live merchant one written
// as part A's connect would leave it, or the shared sandbox toggled on.
func (e *collectEnv) connect(t *testing.T) {
	t.Helper()
	ctx := ctxT(t)
	if !e.livemode {
		conn, err := e.conns.EnableSandbox(ctx, e.sp, "inter", "user:test", "req", now())
		if err != nil {
			t.Fatal(err)
		}
		e.conn = conn
		return
	}
	conn := &byokconn.Connection{AccountOrganizationID: e.accountOrg, Livemode: true, Provider: "inter", ID: id.New(),
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, SyncState: byokconn.SyncActive, Version: 1, Sealed: "b1.fake",
		AccountNumber: "1234567", PixKey: "evp-" + id.New(), NotifyEmail: "financeiro@example.com",
		Capabilities: []byok.Capability{byok.CapabilityStatement, byok.CapabilityCollection},
		FirstDay:     brcal.FromTime(now()), BackfillFrom: brcal.FromTime(now()), BackfillDone: true}
	conn.LedgerAccountID = services.LedgerAccountID("inter", conn.AccountNumber+conn.ID)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	if _, err := ledger.EnsureByokAccount(ctx, e.sp, conn.LedgerAccountID, "Inter ···4567", "inter", now()); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.CreateConnection(ctx, conn, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	e.conn = conn
}

func (e *collectEnv) mode() string {
	if e.livemode {
		return "live"
	}
	return "test"
}

func (e *collectEnv) newCustomer(t *testing.T, taxID string) *billing.Customer {
	t.Helper()
	c := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.tenant.ID, Livemode: e.livemode,
		Name: "Cliente Pagador", Email: "cliente@example.com", TaxID: taxID}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctxT(t), c, "test", "req", now()); err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *collectEnv) customerWithoutTaxID(t *testing.T) *billing.Customer {
	return e.newCustomer(t, "")
}

func (e *collectEnv) setCollection(t *testing.T, s billing.CollectionSettings) {
	t.Helper()
	org := e.tenantNow(t)
	if err := e.orgs.SetCollection(ctxT(t), org, s, "user:test", "req", now()); err != nil {
		t.Fatal(err)
	}
}

func (e *collectEnv) tenantNow(t *testing.T) *billing.Organization {
	t.Helper()
	org, err := e.orgs.Get(ctxT(t), e.tenant.ID, e.livemode)
	if err != nil {
		t.Fatal(err)
	}
	return org
}

func (e *collectEnv) finalizeInvoice(t *testing.T, amount billing.Cents) *billing.Invoice {
	t.Helper()
	return e.finalizeInvoiceFor(t, e.customer, amount)
}

// finalizeInvoiceFor issues a one-off invoice through the invoicer (the
// console's issue path), which runs the BYOK hook after Finalize.
func (e *collectEnv) finalizeInvoiceFor(t *testing.T, c *billing.Customer, amount billing.Cents) *billing.Invoice {
	t.Helper()
	inv := &billing.Invoice{ID: id.NewWithPrefix(id.PrefixInvoice), OrganizationID: e.tenant.ID, Livemode: e.livemode,
		CustomerID: c.ID, Status: billing.InvoiceDraft, Period: marchPeriod(), Currency: billing.CurrencyBRL, Total: amount, Subtotal: amount}
	items := []billing.InvoiceItem{{Description: "Serviço", Period: marchPeriod(), Quantity: 1, UnitAmount: amount, Amount: amount}}
	if err := e.invoices.Create(ctxT(t), inv, items, "gen_"+id.New(), now()); err != nil {
		t.Fatal(err)
	}
	if err := e.invoicer.Issue(ctxT(t), inv, "user:test", "req", now()); err != nil {
		t.Fatal(err)
	}
	return e.reloadInvoice(t, inv)
}

func (e *collectEnv) reloadInvoice(t *testing.T, inv *billing.Invoice) *billing.Invoice {
	t.Helper()
	got, err := e.invoices.Get(ctxT(t), inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e *collectEnv) attemptList(t *testing.T, inv *billing.Invoice) []billing.PaymentAttempt {
	t.Helper()
	list, err := e.payments.ListAttempts(ctxT(t), inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func (e *collectEnv) attempts(t *testing.T, inv *billing.Invoice) int {
	return len(e.attemptList(t, inv))
}

// attempt is the invoice's latest attempt.
func (e *collectEnv) attempt(t *testing.T, inv *billing.Invoice) billing.PaymentAttempt {
	t.Helper()
	list := e.attemptList(t, inv)
	if len(list) == 0 {
		t.Fatalf("invoice %s has no attempt", inv.ID)
	}
	return list[len(list)-1]
}

func (e *collectEnv) chargeRef(t *testing.T, inv *billing.Invoice) repositories.ChargeRef {
	t.Helper()
	ref, err := e.payments.ChargeRef(ctxT(t), "inter", e.livemode, e.attempt(t, inv).ChargeRef)
	if err != nil {
		t.Fatal(err)
	}
	return *ref
}

func (e *collectEnv) chargeRefOf(inv *billing.Invoice) string {
	return billing.InterTxid(e.attemptOf(inv).ID)
}

func (e *collectEnv) attemptOf(inv *billing.Invoice) billing.PaymentAttempt {
	list, _ := e.payments.ListAttempts(context.Background(), inv.OrganizationID, inv.Livemode, inv.ID)
	if len(list) == 0 {
		return billing.PaymentAttempt{}
	}
	return list[len(list)-1]
}

func (e *collectEnv) checkoutErr(t *testing.T, inv *billing.Invoice) (*billing.CheckoutSession, error) {
	t.Helper()
	s, _, err := e.collector.Pay(ctxT(t), inv.OrganizationID, inv.Livemode, inv.ID, "test", "req", now())
	return s, err
}

func (e *collectEnv) checkout(t *testing.T, inv *billing.Invoice) *billing.CheckoutSession {
	t.Helper()
	s, err := e.checkoutErr(t, inv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *collectEnv) consoleWrite(t *testing.T, method, path, body string) apiResponse {
	t.Helper()
	return e.byokWrite(t, method, path, e.sessionToken(t, collectScopes...), e.mode(), "org:"+e.accountOrg, body)
}

func (e *collectEnv) void(t *testing.T, inv *billing.Invoice) {
	t.Helper()
	if r := e.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/void", ""); r.status != 200 {
		t.Fatalf("void: %d %s", r.status, r.body)
	}
}

func (e *collectEnv) excess(t *testing.T, inv *billing.Invoice) []billing.ExcessPayment {
	t.Helper()
	list, err := e.payments.ListExcess(ctxT(t), inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func (e *collectEnv) auditCount(t *testing.T, inv *billing.Invoice, action string) int {
	t.Helper()
	page, err := repositories.NewAuditRepository(testDB, testCfg).ListForEntity(ctxT(t), inv.OrganizationID, inv.Livemode, inv.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range page {
		if a.Action == action {
			n++
		}
	}
	return n
}

// code is the problem code of a response body.
func (r apiResponse) code() string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(r.body, &p)
	return p.Code
}

func byokToday() byok.Date { return toByokDate(brcal.FromTime(now())) }

var _ = strings.Contains
var _ = time.Second
var _ byokrpc.Session

func todayPlus(n int) brcal.Date { return brcal.FromTime(now()).AddDays(n) }

func bodyHas(b []byte, s string) bool { return strings.Contains(string(b), s) }

// useWallet rebuilds the env's collector with this wallet client.
func (e *collectEnv) useWallet(w services.ChargeOpener) {
	customers := repositories.NewCustomerRepository(testDB, testCfg)
	e.collector = services.NewCollector(e.invoices, e.payments, customers, e.orgs, hookedSubs(e.tz), w).WithGates(e.gates).WithByok(e.byok)
}
