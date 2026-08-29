//go:build integration

package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// received is one delivery a fake consumer got.
type received struct {
	payload   billing.Payload
	signature string
	timestamp string
	body      []byte
}

// consumer is a fake CTech service listening for its own events.
type consumer struct {
	server *httptest.Server
	secret string

	mu   sync.Mutex
	got  []received
	code int
}

func newConsumer(t *testing.T, code int) *consumer {
	t.Helper()
	c := &consumer{secret: strings.Repeat("s", 40), code: code}
	c.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p billing.Payload
		_ = json.Unmarshal(body, &p)

		c.mu.Lock()
		c.got = append(c.got, received{
			payload:   p,
			signature: r.Header.Get("X-Billing-Signature"),
			timestamp: r.Header.Get("X-Billing-Timestamp"),
			body:      body,
		})
		status := c.code
		c.mu.Unlock()

		w.WriteHeader(status)
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *consumer) deliveries() []received {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]received(nil), c.got...)
}

func (c *consumer) setCode(code int) {
	c.mu.Lock()
	c.code = code
	c.mu.Unlock()
}

func (c *consumer) types() []billing.EventType {
	out := []billing.EventType{}
	for _, d := range c.deliveries() {
		out = append(out, d.payload.Type)
	}
	return out
}

// deliverer wires the job against a client that trusts the test servers'
// self-signed certificates. Plain HTTP is not an option: WebhookEndpoint refuses
// a non-https URL, which is a rule worth keeping rather than relaxing for a test.
func newDeliverer(t *testing.T, cs ...*consumer) *services.Deliverer {
	t.Helper()
	d := services.NewDeliverer(repositories.NewWebhookRepository(testDB, testCfg))
	d.SetHTTPClient(&http.Client{Timeout: 5 * time.Second, Transport: cs[0].server.Client().Transport})
	return d
}

func seedProduct(t *testing.T, org *billing.Organization, productID, ownerKey string) *billing.Price {
	t.Helper()
	catalog := repositories.NewCatalogRepository(testDB, testCfg)
	ctx := ctxT(t)

	product := &billing.Product{
		ID: productID, OrganizationID: org.ID, Livemode: org.Livemode,
		Name: productID, Active: true, OwnerKey: ownerKey,
	}
	if err := catalog.CreateProduct(ctx, product, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	price := &billing.Price{
		ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: org.ID, Livemode: org.Livemode,
		ProductID: product.ID, Type: billing.PriceFixed, Currency: billing.CurrencyBRL,
		UnitAmount: 9900,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance,
	}
	if err := catalog.CreatePrice(ctx, price, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	return price
}

func registerEndpoint(t *testing.T, org *billing.Organization, c *consumer, ownerKey string, events ...billing.EventType) *billing.WebhookEndpoint {
	t.Helper()
	e := &billing.WebhookEndpoint{
		ID: "whe_" + id.New(), OrganizationID: org.ID, Livemode: org.Livemode,
		URL: c.server.URL, Secret: c.secret, OwnerKey: ownerKey, Events: events,
	}
	if err := repositories.NewWebhookRepository(testDB, testCfg).CreateEndpoint(ctxT(t), e, now()); err != nil {
		t.Fatal(err)
	}
	return e
}

func subscribe(t *testing.T, org *billing.Organization, price *billing.Price) *billing.Subscription {
	t.Helper()
	ctx := ctxT(t)
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	catalog := repositories.NewCatalogRepository(testDB, testCfg)
	usage := repositories.NewUsageRepository(testDB, testCfg)

	subscriber := services.NewSubscriber(subs, catalog, services.NewInvoicer(subs, invoices, catalog, usage))
	sub, _, err := subscriber.Subscribe(ctx, services.SubscribeInput{
		OrganizationID: org.ID,
		Livemode:       org.Livemode,
		CustomerID:     id.NewWithPrefix(id.PrefixCustomer),
		Items:          []services.SubscribeItem{{PriceID: price.ID, Quantity: 1}},
		Actor:          "test",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// runDelivery drains both queues rather than making one pass of each.
//
// The queues are cross-tenant — an event is pending regardless of whose it is —
// so with the whole suite running, this test's two events can sit behind a
// hundred belonging to other tests. One bounded pass would then deliver nothing
// here and the test would fail for a reason that has nothing to do with routing.
// Draining is also what the real job does across its every-minute passes.
func runDelivery(t *testing.T, d *services.Deliverer, org *billing.Organization, at time.Time) {
	t.Helper()
	ctx := context.Background()
	const batch = 100
	for {
		res := d.FanOut(ctx, org.Livemode, batch, at)
		if len(res.Errors) > 0 {
			t.Fatalf("fan-out: %v", res.Errors)
		}
		if res.Examined < batch {
			break
		}
	}
	for {
		res := d.Deliver(ctx, org.Livemode, batch, at)
		if len(res.Errors) > 0 {
			t.Fatalf("deliver: %v", res.Errors)
		}
		if res.Examined < batch {
			break
		}
	}
}

// TestEventsReachOnlyTheirOwnService is the claim the whole design exists for.
//
// Tenant zero is **one** organization holding every CTech service's
// subscriptions, so an endpoint registered per organization would send ctech-dfe
// every invoice ctech-poker issued. Routing is per product owner; this asserts
// that with two services in one tenant, which is the only configuration where
// getting it wrong is visible.
func TestEventsReachOnlyTheirOwnService(t *testing.T) {
	org := newOrg(t, true)

	dfe, poker := newConsumer(t, http.StatusOK), newConsumer(t, http.StatusOK)
	registerEndpoint(t, org, dfe, "dfe")
	registerEndpoint(t, org, poker, "poker")

	dfePrice := seedProduct(t, org, "prod_dfe_"+id.New(), "dfe")
	pokerPrice := seedProduct(t, org, "prod_poker_"+id.New(), "poker")

	dfeSub := subscribe(t, org, dfePrice)
	pokerSub := subscribe(t, org, pokerPrice)

	runDelivery(t, newDeliverer(t, dfe, poker), org, now())

	assertOnlyOwn := func(name string, c *consumer, ownSub, otherSub *billing.Subscription) {
		t.Helper()
		got := c.deliveries()
		if len(got) == 0 {
			t.Fatalf("%s received nothing", name)
		}
		for _, d := range got {
			switch d.payload.Data.SubscriptionID {
			case ownSub.ID:
			case otherSub.ID:
				t.Fatalf("%s received an event for the other service's subscription %s", name, otherSub.ID)
			default:
				// An invoice event carries its subscription id too, so anything
				// else is a routing failure rather than a shape this test forgot.
				t.Fatalf("%s received an event for an unknown subscription %q", name, d.payload.Data.SubscriptionID)
			}
		}
	}
	assertOnlyOwn("dfe", dfe, dfeSub, pokerSub)
	assertOnlyOwn("poker", poker, pokerSub, dfeSub)
}

// TestDeliveryIsSignedOverTimestampAndBody pins the wire contract. A consumer in
// this family verifies with services.Sign, and a change here that does not fail
// this test is a change that silently stops every consumer from verifying.
func TestDeliveryIsSignedOverTimestampAndBody(t *testing.T) {
	org := newOrg(t, true)
	c := newConsumer(t, http.StatusOK)
	registerEndpoint(t, org, c, "")

	price := seedProduct(t, org, "prod_"+id.New(), "")
	subscribe(t, org, price)
	runDelivery(t, newDeliverer(t, c), org, now())

	got := c.deliveries()
	if len(got) == 0 {
		t.Fatal("nothing delivered")
	}
	d := got[0]

	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(d.timestamp))
	mac.Write([]byte("."))
	mac.Write(d.body)
	want := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if d.signature != want {
		t.Fatalf("signature = %q, want %q", d.signature, want)
	}

	// The payload is deliberately thin: an id and a type. Anything richer is
	// customer data sent to a URL somebody typed into a config file.
	if d.payload.Data.ID == "" || d.payload.Type == "" {
		t.Fatalf("payload is missing its subject: %+v", d.payload)
	}
	if strings.Contains(string(d.body), "amount") || strings.Contains(string(d.body), "customer") {
		t.Fatalf("payload carries data it should not: %s", d.body)
	}
}

// TestAnEndpointFilterOnEventTypeIsHonoured covers the second filter, which a
// consumer uses to avoid being woken for things it does not act on.
func TestAnEndpointFilterOnEventTypeIsHonoured(t *testing.T) {
	org := newOrg(t, true)
	c := newConsumer(t, http.StatusOK)
	registerEndpoint(t, org, c, "", billing.EventInvoicePaid)

	price := seedProduct(t, org, "prod_"+id.New(), "")
	subscribe(t, org, price)
	runDelivery(t, newDeliverer(t, c), org, now())

	if got := c.types(); len(got) != 0 {
		t.Fatalf("an endpoint subscribed to invoice.paid received %v", got)
	}
}

// TestAFailedDeliveryIsRetriedWithBackoff proves the queue is a queue: a
// consumer that is down does not lose the event, and it is not hammered either.
func TestAFailedDeliveryIsRetriedWithBackoff(t *testing.T) {
	org := newOrg(t, true)
	c := newConsumer(t, http.StatusInternalServerError)
	registerEndpoint(t, org, c, "")

	price := seedProduct(t, org, "prod_"+id.New(), "")
	subscribe(t, org, price)

	d := newDeliverer(t, c)
	start := now()
	runDelivery(t, d, org, start)

	first := len(c.deliveries())
	if first == 0 {
		t.Fatal("nothing was attempted")
	}

	// A pass a second later must not retry: the backoff put it 30 seconds out.
	// Without this the job would spin on a dead endpoint, and everything behind
	// it in the queue would wait.
	runDelivery(t, d, org, start.Add(time.Second))
	if got := len(c.deliveries()); got != first {
		t.Fatalf("retried inside the backoff window: %d attempts, want %d", got, first)
	}

	// Past the backoff, and now answering, it lands.
	c.setCode(http.StatusOK)
	runDelivery(t, d, org, start.Add(2*time.Minute))
	if got := len(c.deliveries()); got <= first {
		t.Fatalf("not retried after the backoff elapsed: %d attempts", got)
	}

	hooks := repositories.NewWebhookRepository(testDB, testCfg)
	events, err := hooks.PendingEvents(ctxT(t), true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("%d events are still queued for fan-out after delivery", len(events))
	}
}
