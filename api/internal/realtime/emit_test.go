package realtime

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type fakeCustomers struct {
	mu    sync.Mutex
	users map[string]string
	calls int
	err   error
}

func (f *fakeCustomers) Get(_ context.Context, _ string, _ bool, id string) (*billing.Customer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &billing.Customer{ID: id, UserID: f.users[id]}, nil
}

func invoice(live bool) *billing.Invoice {
	return &billing.Invoice{ID: "in_1", OrganizationID: "org1", Livemode: live, CustomerID: "cus_1", Status: billing.InvoicePaid}
}

func TestAnInvoiceChangeGoesToTheTenantTheCheckoutAndTheCustomersUser(t *testing.T) {
	e := NewEmitter(&fakeCustomers{users: map[string]string{"cus_1": "user-1"}})
	c := e.ForInvoice(context.Background(), invoice(true), billing.EventInvoicePaid)
	want := []string{TenantScope("org1", true), InvoiceScope("org1", true, "in_1"), UserScope("user-1")}
	if !slices.Equal(c.Scopes, want) {
		t.Fatalf("scopes = %v, want %v", c.Scopes, want)
	}
	if c.Type != "invoice.paid" || c.Entity != "invoice" || c.EntityID != "in_1" || c.Status != "PAID" {
		t.Fatalf("change = %+v", c)
	}
}

func TestACustomerWithNoUserHasNoUserScope(t *testing.T) {
	e := NewEmitter(&fakeCustomers{users: map[string]string{}})
	c := e.ForInvoice(context.Background(), invoice(true), billing.EventInvoicePaid)
	want := []string{TenantScope("org1", true), InvoiceScope("org1", true, "in_1")}
	if !slices.Equal(c.Scopes, want) {
		t.Fatalf("scopes = %v, want %v", c.Scopes, want)
	}
}

func TestATestModeInvoiceHearsTestKeysOnly(t *testing.T) {
	e := NewEmitter(&fakeCustomers{users: map[string]string{"cus_1": "user-1"}})
	for _, s := range e.ForInvoice(context.Background(), invoice(false), billing.EventInvoicePaid).Scopes {
		if s == TenantScope("org1", true) || s == InvoiceScope("org1", true, "in_1") {
			t.Fatalf("a test invoice was sent to the live scope %s", s)
		}
	}
	if got := e.ForInvoice(context.Background(), invoice(false), billing.EventInvoicePaid).Scopes[0]; got != TenantScope("org1", false) {
		t.Fatalf("first scope = %s", got)
	}
}

func TestAnUnreadableCustomerDoesNotStopTheChange(t *testing.T) {
	e := NewEmitter(&fakeCustomers{err: errors.New("dynamo down")})
	c := e.ForInvoice(context.Background(), invoice(true), billing.EventInvoicePaid)
	if c == nil || len(c.Scopes) != 2 {
		t.Fatalf("change = %+v: the console and checkout must still be told", c)
	}
}

func TestTheCustomersUserIsCachedForAMinute(t *testing.T) {
	f := &fakeCustomers{users: map[string]string{"cus_1": "user-1"}}
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e := NewEmitter(f).WithClock(func() time.Time { return clock })
	for i := 0; i < 5; i++ {
		e.ForInvoice(context.Background(), invoice(true), billing.EventInvoicePaid)
	}
	if f.calls != 1 {
		t.Fatalf("reads = %d, want 1 within the minute", f.calls)
	}
	clock = clock.Add(61 * time.Second)
	e.ForInvoice(context.Background(), invoice(true), billing.EventInvoicePaid)
	if f.calls != 2 {
		t.Fatalf("reads = %d, want a fresh one after the minute", f.calls)
	}
}

func TestAPaymentAndACheckoutSessionHearTheTenantAndTheInvoiceOnly(t *testing.T) {
	e := NewEmitter(&fakeCustomers{users: map[string]string{"cus_1": "user-1"}})
	pay := e.ForAttempt(context.Background(), &billing.PaymentAttempt{ID: "pa_1", OrganizationID: "org1", Livemode: true, InvoiceID: "in_1", Status: billing.AttemptFailed}, billing.EventPaymentFailed)
	cs := e.ForCheckout(context.Background(), &billing.CheckoutSession{ID: "cs_1", OrganizationID: "org1", Livemode: true, InvoiceID: "in_1", Status: billing.CheckoutCompleted}, billing.EventCheckoutCompleted)
	want := []string{TenantScope("org1", true), InvoiceScope("org1", true, "in_1")}
	for name, c := range map[string]*repositories.Change{"payment": pay, "checkout": cs} {
		if !slices.Equal(c.Scopes, want) {
			t.Errorf("%s scopes = %v, want %v", name, c.Scopes, want)
		}
	}
}

func TestACreditNoteIsHeardWhereItsInvoiceIs(t *testing.T) {
	e := NewEmitter(&fakeCustomers{users: map[string]string{"cus_1": "user-1"}})
	c := e.ForCreditNote(context.Background(), &billing.CreditNote{ID: "cn_1"}, invoice(true))
	if c.Type != "credit_note.created" || c.Entity != "credit_note" || len(c.Scopes) != 3 {
		t.Fatalf("change = %+v", c)
	}
}

func TestASubscriptionChangeGoesToTheTenantAndTheCustomersUser(t *testing.T) {
	e := NewEmitter(&fakeCustomers{users: map[string]string{"cus_1": "user-1"}})
	c := e.ForSubscription(context.Background(), &billing.Subscription{ID: "sub_1", OrganizationID: "org1", Livemode: true, CustomerID: "cus_1", Status: billing.SubscriptionActive}, billing.EventSubscriptionActivated)
	want := []string{TenantScope("org1", true), UserScope("user-1")}
	if !slices.Equal(c.Scopes, want) || c.Status != "ACTIVE" {
		t.Fatalf("change = %+v", c)
	}
}

// A Change is a hint: its type is a webhook name or one of the realtime-only
// names, and its status is a state word, never a name, an amount or a document.
func TestAChangeNeverCarriesAnythingButIdsAndStateWords(t *testing.T) {
	e := NewEmitter(&fakeCustomers{users: map[string]string{"cus_1": "user-1"}})
	statuses := map[string]bool{}
	for _, s := range []billing.InvoiceStatus{billing.InvoiceDraft, billing.InvoiceOpen, billing.InvoicePaid, billing.InvoiceVoid, billing.InvoiceUncollectible} {
		statuses[string(s)] = true
	}
	known := map[string]bool{}
	for _, t := range billing.WebhookEventTypes() {
		known[string(t)] = true
	}
	for _, t := range RealtimeOnlyTypes() {
		known[t] = true
	}
	for _, typ := range []billing.EventType{billing.EventInvoiceCreated, billing.EventInvoiceFinalized, billing.EventInvoicePaid, billing.EventInvoiceVoided} {
		inv := invoice(true)
		c := e.ForInvoice(context.Background(), inv, typ)
		if !known[c.Type] {
			t.Errorf("type %q is neither a webhook name nor a realtime-only one", c.Type)
		}
		if !statuses[c.Status] {
			t.Errorf("status %q is not a state word", c.Status)
		}
	}
}

type recorder struct {
	mu   sync.Mutex
	sent map[string][][]byte
}

func (r *recorder) Broadcast(_ context.Context, key string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent == nil {
		r.sent = map[string][][]byte{}
	}
	r.sent[key] = append(r.sent[key], payload)
}

func TestThePublisherSendsOneFramePerScopeAndItDecodesBack(t *testing.T) {
	rec := &recorder{}
	c := &repositories.Change{
		ID: "01JABCDEFGHJKMNPQRSTVWXYZ0", Type: "invoice.paid", Entity: "invoice", EntityID: "in_1", Status: "PAID",
		Revision: 7, OccurredAt: time.UnixMilli(1_760_000_000_000), Scopes: []string{TenantScope("org1", true), UserScope("u1")},
	}
	NewPublisher(rec, nil).Publish(context.Background(), c)

	if len(rec.sent) != 2 || len(rec.sent[TenantScope("org1", true)]) != 1 || len(rec.sent[UserScope("u1")]) != 1 {
		t.Fatalf("sent = %v", rec.sent)
	}
	var f wire.ServerFrame
	if err := proto.Unmarshal(rec.sent[UserScope("u1")][0], &f); err != nil {
		t.Fatal(err)
	}
	got := f.GetChange()
	if got.GetId() != c.ID || got.GetType() != "invoice.paid" || got.GetEntityId() != "in_1" || got.GetStatus() != "PAID" || got.GetRevision() != 7 || got.GetOccurredAtMs() != 1_760_000_000_000 {
		t.Fatalf("frame = %+v", got)
	}
}
