//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/api-commons/accountorgs"
	"gopkg.aoctech.app/api-commons/drain"
	"gopkg.aoctech.app/api-commons/jwtverify"
	"gopkg.aoctech.app/api-commons/ws"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/realtime"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type tokenTable map[string]*jwtverify.Claims

func (t tokenTable) VerifyClaims(_ context.Context, token string) (*jwtverify.Claims, error) {
	if c, ok := t[token]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("bad token")
}

type membersTable map[string]accountorgs.Membership

func (m membersTable) Membership(_ context.Context, org, user string) (accountorgs.Membership, error) {
	return m[org+"|"+user], nil
}

// A socket under test: its credential, what it must hear (written by hand) and
// every frame it received.
type probe struct {
	name string
	auth *wire.Auth
	// owns are the scope keys the principal is entitled to, spelled out here and
	// not computed from the code under test.
	owns []string

	mu     sync.Mutex
	frames [][]byte
	conn   *fws.Conn
}

func (p *probe) record(raw []byte) {
	p.mu.Lock()
	p.frames = append(p.frames, append([]byte(nil), raw...))
	p.mu.Unlock()
}

// entityIDs are the entity ids of the changes the probe received.
func (p *probe) entityIDs(t *testing.T) map[string]bool {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]bool{}
	for _, raw := range p.frames {
		var f wire.ServerFrame
		if err := proto.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		if c := f.GetChange(); c != nil {
			out[c.GetEntityId()] = true
		}
	}
	return out
}

func (p *probe) hears(scopes []string) bool {
	for _, s := range scopes {
		for _, o := range p.owns {
			if s == o {
				return true
			}
		}
	}
	return false
}

type isolationEnv struct {
	acctA, acctB string
	orgA, orgB   *billing.Organization // live
	orgAtest     *billing.Organization
	invoices     *repositories.InvoiceRepository
	bills        *repositories.BillRepository
	ledger       *repositories.LedgerRepository
	customers    map[string]*billing.Customer // by label
	spaces       map[string]space.ResolvedSpace
	probes       []*probe
	links        *services.PayLink
	url          string
}

func createTwin(t *testing.T, org *billing.Organization) *billing.Organization {
	t.Helper()
	twin := *org
	twin.Livemode = false
	if err := repositories.NewOrganizationRepository(testDB, testCfg).Create(ctxT(t), &twin, now()); err != nil {
		t.Fatal(err)
	}
	return &twin
}

func newIsolationEnv(t *testing.T) *isolationEnv {
	t.Helper()
	ctx := ctxT(t)
	e := &isolationEnv{acctA: newSpaceOrgID(), acctB: newSpaceOrgID(), customers: map[string]*billing.Customer{}, spaces: map[string]space.ResolvedSpace{}}
	e.orgA = newLinkedOrg(t, true, e.acctA)
	e.orgAtest = createTwin(t, e.orgA)
	e.orgB = newLinkedOrg(t, true, e.acctB)
	createTwin(t, e.orgB)

	// The realtime wiring, explicit and local to this test.
	customerRepo := repositories.NewCustomerRepository(testDB, testCfg)
	reg := ws.NewMemoryRegistry()
	metrics := &realtime.Metrics{}
	rt := repositories.Realtime{Emitter: realtime.NewEmitter(customerRepo), Publisher: realtime.NewPublisher(reg, metrics)}
	e.invoices = repositories.NewInvoiceRepository(testDB, testCfg).WithRealtime(rt)
	e.bills = repositories.NewBillRepository(testDB, testCfg).WithRealtime(rt)
	e.ledger = repositories.NewLedgerRepository(testDB, testCfg).WithRealtime(rt)

	mkCustomer := func(label string, org *billing.Organization, userID string) {
		c := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: org.ID, Livemode: org.Livemode, Name: label, Email: label + "@example.com", UserID: userID}
		if err := customerRepo.Create(ctx, c, "test", "req", now()); err != nil {
			t.Fatal(err)
		}
		e.customers[label] = c
	}
	mkCustomer("A-live-u1", e.orgA, "u1")
	mkCustomer("A-live-api", e.orgA, "")
	mkCustomer("A-test", e.orgAtest, "")
	mkCustomer("B-live-u2", e.orgB, "u2")
	mkCustomer("B-live-api", e.orgB, "")

	// Finanças spaces, one per (account organization, mode).
	for label, spec := range map[string]struct {
		org  string
		live bool
	}{"A-live": {e.acctA, true}, "A-test": {e.acctA, false}, "B-live": {e.acctB, true}} {
		sp, err := space.ForJob(spec.org, spec.live)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.ledger.EnsureReady(ctx, sp, now()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ledger.EnsureByokAccount(ctx, sp, "byok-inter-abc", "Inter", "inter", now()); err != nil {
			t.Fatal(err)
		}
		e.spaces[label] = sp
	}

	// Credentials. memberA administers account A, memberB account B.
	verifier := tokenTable{
		"tok-memberA": {Sub: "memberA", SID: "s"}, "tok-memberB": {Sub: "memberB", SID: "s"},
		"tok-u1": {Sub: "u1", SID: "s"}, "tok-u2": {Sub: "u2", SID: "s"},
	}
	members := membersTable{
		e.acctA + "|memberA": {Member: true, Role: "admin", Kind: "organization"},
		e.acctB + "|memberB": {Member: true, Role: "owner", Kind: "organization"},
	}
	e.links = services.NewPayLink("an-isolation-test-secret-of-32-bytes!!", "https://pay.example")
	authorizers := map[string]realtime.Authorizer{
		realtime.KindConsole:  &realtime.ConsoleAuthorizer{Verifier: verifier, Spaces: space.NewResolver(members, nil), Tenants: repositories.NewOrganizationRepository(testDB, testCfg)},
		realtime.KindPortal:   &realtime.PortalAuthorizer{Verifier: verifier},
		realtime.KindCheckout: &realtime.CheckoutAuthorizer{Links: e.links, Lifetime: 35 * time.Minute},
	}
	cfg := realtime.DefaultConfig()
	gw := &realtime.Gateway{
		Authorizers: authorizers, Hub: realtime.NewHub(reg, realtime.DefaultLimits(), metrics),
		Log: repositories.NewRealtimeRepository(testDB, testCfg), Drain: &drain.Tracker{}, Config: cfg,
	}
	app := fiber.New()
	realtime.RegisterGateway(app.Group("/v1.0"), gw)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	e.url = "ws://" + ln.Addr().String() + "/v1.0/ws"
	return e
}

// connect opens one socket per probe and starts collecting its frames.
func (e *isolationEnv) connect(t *testing.T, invoice1, invoice2 string) {
	t.Helper()
	tenant := func(o *billing.Organization, live bool) string { return realtime.TenantScope(o.ID, live) }
	e.probes = []*probe{
		{name: "console A live", auth: &wire.Auth{Token: "tok-memberA", Space: "org:" + e.acctA, Mode: "live"},
			owns: []string{tenant(e.orgA, true), realtime.SpaceScope(e.acctA + "#live")}},
		{name: "console A test", auth: &wire.Auth{Token: "tok-memberA", Space: "org:" + e.acctA, Mode: "test"},
			owns: []string{tenant(e.orgA, false), realtime.SpaceScope(e.acctA + "#test")}},
		{name: "console B live", auth: &wire.Auth{Token: "tok-memberB", Space: "org:" + e.acctB, Mode: "live"},
			owns: []string{tenant(e.orgB, true), realtime.SpaceScope(e.acctB + "#live")}},
		{name: "portal U1", auth: &wire.Auth{Token: "tok-u1"}, owns: []string{realtime.UserScope("u1")}},
		{name: "portal U2", auth: &wire.Auth{Token: "tok-u2"}, owns: []string{realtime.UserScope("u2")}},
		{name: "checkout 1", auth: &wire.Auth{PayLink: e.links.Sign(e.orgA.ID, true, invoice1)},
			owns: []string{realtime.InvoiceScope(e.orgA.ID, true, invoice1)}},
		{name: "checkout 2", auth: &wire.Auth{PayLink: e.links.Sign(e.orgB.ID, true, invoice2)},
			owns: []string{realtime.InvoiceScope(e.orgB.ID, true, invoice2)}},
	}
	for _, p := range e.probes {
		conn, _, err := (&fws.Dialer{HandshakeTimeout: 2 * time.Second}).Dial(e.url, http.Header{})
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		p.conn = conn
		t.Cleanup(func() { conn.Close() })
		b, _ := proto.Marshal(&wire.ClientFrame{Body: &wire.ClientFrame_Auth{Auth: p.auth}})
		if err := conn.WriteMessage(fws.BinaryMessage, b); err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{})
		go func() {
			var once sync.Once
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					return
				}
				p.record(raw)
				var f wire.ServerFrame
				if proto.Unmarshal(raw, &f) == nil && f.GetReady() != nil {
					once.Do(func() { close(ready) })
				}
			}
		}()
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s never got Ready (frames: %d)", p.name, len(p.frames))
		}
	}
}

// settle waits until no probe has received anything new for a moment.
func (e *isolationEnv) settle() {
	total := func() int {
		n := 0
		for _, p := range e.probes {
			p.mu.Lock()
			n += len(p.frames)
			p.mu.Unlock()
		}
		return n
	}
	last, stable := -1, 0
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && stable < 4; time.Sleep(50 * time.Millisecond) {
		if n := total(); n == last {
			stable++
		} else {
			last, stable = n, 0
		}
	}
}

// payInvoice creates, issues and pays an invoice for the customer; returns its id
// and the scopes that may hear it, worked out by hand from the draw.
func (e *isolationEnv) payInvoice(t *testing.T, org *billing.Organization, customer *billing.Customer, at time.Duration) (string, []string) {
	t.Helper()
	ctx := ctxT(t)
	inv := &billing.Invoice{
		ID: id.NewWithPrefix(id.PrefixInvoice), OrganizationID: org.ID, Livemode: org.Livemode, CustomerID: customer.ID,
		Status: billing.InvoiceDraft, Period: marchPeriod(), Currency: billing.CurrencyBRL, Total: 4990, Subtotal: 4990,
	}
	items := []billing.InvoiceItem{{Description: "DF-e", Period: marchPeriod(), Quantity: 1, UnitAmount: 4990, Amount: 4990}}
	if err := e.invoices.Create(ctx, inv, items, "gen_"+inv.ID, now().Add(at)); err != nil {
		t.Fatal(err)
	}
	due := brcal.New(2026, time.March, 10)
	if _, err := e.invoices.Finalize(ctx, inv, due, due, billing.CauseScheduler, "test", "r1", now().Add(at+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.invoices.Transition(ctx, inv, billing.InvoicePaid, billing.CauseManualPayment, "test", "r2", now().Add(at+2*time.Second)); err != nil {
		t.Fatal(err)
	}
	scopes := []string{realtime.TenantScope(org.ID, org.Livemode), realtime.InvoiceScope(org.ID, org.Livemode, inv.ID)}
	if customer.UserID != "" {
		scopes = append(scopes, realtime.UserScope(customer.UserID))
	}
	return inv.ID, scopes
}

// postBill creates a bill in a Finanças space; returns its id and its one scope.
func (e *isolationEnv) postBill(t *testing.T, label string, at time.Duration) (string, []string) {
	t.Helper()
	sp := e.spaces[label]
	d := brcal.MustParse("2026-10-05")
	b, err := e.bills.Create(ctxT(t), sp, finance.Bill{Direction: finance.Payable, Amount: 300, AccountID: "byok-inter-abc",
		CategoryID: "cat-aluguel", Description: "Luz", Competence: d, Due: d, Origin: finance.OriginManual},
		repositories.PostMeta{Actor: "u"}, now().Add(at))
	if err != nil {
		t.Fatal(err)
	}
	return b.ID, []string{realtime.SpaceScope(sp.PK())}
}

// assertDelivered checks every probe received the entity iff it owns one of its scopes.
func (e *isolationEnv) assertDelivered(t *testing.T, label, entityID string, scopes []string) {
	t.Helper()
	for _, p := range e.probes {
		if got, want := p.entityIDs(t)[entityID], p.hears(scopes); got != want {
			t.Errorf("%s: %s heard=%v, want %v (scopes %v)", label, p.name, got, want, scopes)
		}
	}
}

// No frame, on any socket, may contain an id that belongs to a tenant, user or
// invoice the socket does not own.
func (e *isolationEnv) assertNoForeignIDs(t *testing.T, owned map[string][]string, foreign func(p *probe) []string) {
	t.Helper()
	for _, p := range e.probes {
		p.mu.Lock()
		all := bytes.Join(p.frames, nil)
		p.mu.Unlock()
		for _, id := range foreign(p) {
			if bytes.Contains(all, []byte(id)) {
				t.Errorf("%s received a frame containing %q, which it does not own", p.name, id)
			}
		}
	}
}

func TestEverySocketHearsExactlyWhatItOwnsAcrossTenantsModesUsersAndLinks(t *testing.T) {
	e := newIsolationEnv(t)

	// The two invoices the checkouts are bound to must exist before the links do.
	// They are created (draft) without realtime noise, then connected, then paid.
	preCreate := func(org *billing.Organization, c *billing.Customer) *billing.Invoice {
		inv := &billing.Invoice{ID: id.NewWithPrefix(id.PrefixInvoice), OrganizationID: org.ID, Livemode: org.Livemode, CustomerID: c.ID,
			Status: billing.InvoiceDraft, Period: marchPeriod(), Currency: billing.CurrencyBRL, Total: 4990, Subtotal: 4990}
		items := []billing.InvoiceItem{{Description: "DF-e", Period: marchPeriod(), Quantity: 1, UnitAmount: 4990, Amount: 4990}}
		if err := repositories.NewInvoiceRepository(testDB, testCfg).Create(ctxT(t), inv, items, "gen_"+inv.ID, now()); err != nil {
			t.Fatal(err)
		}
		return inv
	}
	inv1 := preCreate(e.orgA, e.customers["A-live-u1"])
	inv2 := preCreate(e.orgB, e.customers["B-live-u2"])
	e.connect(t, inv1.ID, inv2.ID)

	pay := func(inv *billing.Invoice, org *billing.Organization, c *billing.Customer, at time.Duration) []string {
		due := brcal.New(2026, time.March, 10)
		if _, err := e.invoices.Finalize(ctxT(t), inv, due, due, billing.CauseScheduler, "test", "r1", now().Add(at)); err != nil {
			t.Fatal(err)
		}
		if _, err := e.invoices.Transition(ctxT(t), inv, billing.InvoicePaid, billing.CauseManualPayment, "test", "r2", now().Add(at+time.Second)); err != nil {
			t.Fatal(err)
		}
		scopes := []string{realtime.TenantScope(org.ID, org.Livemode), realtime.InvoiceScope(org.ID, org.Livemode, inv.ID)}
		if c.UserID != "" {
			scopes = append(scopes, realtime.UserScope(c.UserID))
		}
		return scopes
	}
	scopes1 := pay(inv1, e.orgA, e.customers["A-live-u1"], time.Second)
	scopes2 := pay(inv2, e.orgB, e.customers["B-live-u2"], 5*time.Second)
	testID, testScopes := e.payInvoice(t, e.orgAtest, e.customers["A-test"], 10*time.Second)
	billID, billScopes := e.postBill(t, "A-live", 15*time.Second)
	e.settle()

	e.assertDelivered(t, "invoice 1 (tenant A live, customer U1)", inv1.ID, scopes1)
	e.assertDelivered(t, "invoice 2 (tenant B live, customer U2)", inv2.ID, scopes2)
	e.assertDelivered(t, "invoice in tenant A test", testID, testScopes)
	e.assertDelivered(t, "a Finanças bill in space A", billID, billScopes)

	// And no frame carries another owner's ids: nothing of tenant B reaches anything
	// that belongs to tenant A, and the other way round.
	e.assertNoForeignIDs(t, nil, func(p *probe) []string {
		switch p.name {
		case "console A live", "console A test", "portal U1", "checkout 1":
			return []string{inv2.ID, e.orgB.ID, e.acctB, e.customers["B-live-u2"].ID, "u2"}
		case "console B live", "portal U2", "checkout 2":
			return []string{inv1.ID, e.orgA.ID, e.acctA, e.customers["A-live-u1"].ID, "u1", billID, testID}
		}
		return nil
	})
}

// 200 random (principal, mutation) draws from a seeded generator: the delivered
// set must equal the authorized set every time. The seed is logged on failure.
func TestRandomMutationsAreDeliveredToExactlyTheAuthorizedSockets(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed = %d", seed)
	rng := rand.New(rand.NewSource(seed))
	e := newIsolationEnv(t)
	e.connect(t, "in_unused_checkout_1", "in_unused_checkout_2")

	type draw struct {
		label    string
		entityID string
		scopes   []string
	}
	invoiceCases := []struct {
		label    string
		org      *billing.Organization
		customer string
	}{
		{"A live / user 1", e.orgA, "A-live-u1"}, {"A live / API customer", e.orgA, "A-live-api"},
		{"A test", e.orgAtest, "A-test"}, {"B live / user 2", e.orgB, "B-live-u2"}, {"B live / API customer", e.orgB, "B-live-api"},
	}
	billSpaces := []string{"A-live", "A-test", "B-live"}

	var draws []draw
	for i := 0; i < 200; i++ {
		at := time.Duration(i) * 10 * time.Second
		if rng.Intn(3) == 0 {
			label := billSpaces[rng.Intn(len(billSpaces))]
			entityID, scopes := e.postBill(t, label, at)
			draws = append(draws, draw{"bill in " + label, entityID, scopes})
			continue
		}
		c := invoiceCases[rng.Intn(len(invoiceCases))]
		entityID, scopes := e.payInvoice(t, c.org, e.customers[c.customer], at)
		draws = append(draws, draw{"invoice " + c.label, entityID, scopes})
	}
	e.settle()

	// Every probe's expectation is the sorted list of what its scopes cover.
	for _, p := range e.probes {
		var want, got []string
		heard := p.entityIDs(t)
		for _, d := range draws {
			if p.hears(d.scopes) {
				want = append(want, d.entityID)
			}
		}
		for entityID := range heard {
			got = append(got, entityID)
		}
		sort.Strings(want)
		sort.Strings(got)
		if fmt.Sprint(want) != fmt.Sprint(got) {
			t.Errorf("%s (seed %d): heard %d entities, authorized for %d\n  missing or extra: %s", p.name, seed, len(got), len(want), diff(want, got))
		}
	}
}

func diff(want, got []string) string {
	w, g := map[string]bool{}, map[string]bool{}
	for _, s := range want {
		w[s] = true
	}
	for _, s := range got {
		g[s] = true
	}
	var out []string
	for s := range w {
		if !g[s] {
			out = append(out, "missing "+s)
		}
	}
	for s := range g {
		if !w[s] {
			out = append(out, "EXTRA "+s)
		}
	}
	sort.Strings(out)
	if len(out) > 6 {
		out = append(out[:6], "...")
	}
	return fmt.Sprint(out)
}
