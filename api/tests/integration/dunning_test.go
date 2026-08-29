//go:build integration

package integration

import (
	"context"
	"strings"
	"sync"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// mailbox records what would have been sent. Dunning's tests must not need SES
// credentials — a job that can only be tested against the real thing is a job
// whose escalation path nobody exercises until it cancels a real subscription.
type mailbox struct {
	mu   sync.Mutex
	sent []email.Reminder
}

func (m *mailbox) SendInvoiceReminder(_ context.Context, r email.Reminder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, r)
	return nil
}

func (m *mailbox) all() []email.Reminder {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]email.Reminder(nil), m.sent...)
}

// to filters by recipient. The dunning queue is cross-tenant by design — the
// partition key carries no organization — so a run picks up whatever else the
// suite has scheduled for the same date. Asserting on this customer's mail is
// the only assertion that means anything; counting the run's totals would be a
// test that passes alone and fails beside its neighbours.
func (m *mailbox) to(address string) []email.Reminder {
	out := []email.Reminder{}
	for _, r := range m.all() {
		if r.To == address {
			out = append(out, r)
		}
	}
	return out
}

func newDunner(t *testing.T, box *mailbox) *services.Dunner {
	t.Helper()
	return services.NewDunner(
		repositories.NewInvoiceRepository(testDB, testCfg),
		repositories.NewSubscriptionRepository(testDB, testCfg),
		repositories.NewCustomerRepository(testDB, testCfg),
		services.NewPayLink(strings.Repeat("k", 40), "https://billing.aoctech.app"),
		box,
	)
}

// dunnableInvoice is an overdue bill belonging to an **ongoing** subscriber:
// somebody who had the service and is about to lose it, which is what the
// escalation half of the policy is about.
//
// The activation stands in for the earlier periods this subscriber already paid
// for. Without it the fixture is a subscription on its very first invoice —
// INCOMPLETE, never activated — which is a different story with a different
// ending, covered by TestDunningNeverRestrictsAServiceNobodyHad.
func dunnableInvoice(t *testing.T, org *billing.Organization) (*billing.Invoice, *billing.Subscription, string) {
	t.Helper()
	inv, sub, address := unactivatedDunnableInvoice(t, org)
	activateSubscription(t, org, sub.ID)
	sub.Status = billing.SubscriptionActive
	return inv, sub, address
}

// unactivatedDunnableInvoice produces a real OPEN invoice through the real path
// — a subscription billed in advance, whose first invoice is created and
// finalized by the subscriber. Hand-writing an invoice row would also pass, and
// would not prove the settlement queue is armed by the code that issues bills.
func unactivatedDunnableInvoice(t *testing.T, org *billing.Organization) (*billing.Invoice, *billing.Subscription, string) {
	t.Helper()
	ctx := ctxT(t)

	// A distinct address per test, so a cross-tenant run's mailbox can be
	// filtered down to the customer this test is actually about.
	address := id.New() + "@example.com"
	customer := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: org.ID, Livemode: org.Livemode,
		Name: "Ana Ribeiro", Email: address,
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, customer, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}

	price := seedProduct(t, org, "prod_"+id.New(), "")
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	catalog := repositories.NewCatalogRepository(testDB, testCfg)
	usage := repositories.NewUsageRepository(testDB, testCfg)

	subscriber := services.NewSubscriber(subs, catalog, services.NewInvoicer(subs, invoices, catalog, usage))
	sub, inv, err := subscriber.Subscribe(ctx, services.SubscribeInput{
		OrganizationID: org.ID, Livemode: org.Livemode,
		CustomerID: customer.ID, Actor: "test",
		Items: []services.SubscribeItem{{PriceID: price.ID, Quantity: 1}},
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil {
		t.Fatal("an advance-billed subscription must produce its first invoice")
	}
	return inv, sub, address
}

// TestDunningWalksThePolicyOneStepPerDay is the whole feature: each scheduled
// day performs exactly one action, and the invoice moves to the next.
func TestDunningWalksThePolicyOneStepPerDay(t *testing.T) {
	org := newOrg(t, true)
	inv, sub, address := dunnableInvoice(t, org)

	box := &mailbox{}
	dunner := newDunner(t, box)
	ctx := context.Background()

	// Step 0 is three days before the due date: the note that can still prevent
	// the invoice being late at all.
	day, _ := inv.Schedule().DunningDate(inv.DueDate, 0)
	if res := dunner.Run(ctx, true, day, now()); len(res.Errors) > 0 {
		t.Fatalf("dunning: %v", res.Errors)
	}
	sent := box.to(address)
	if len(sent) != 1 {
		t.Fatalf("step 0 sent %d reminders, want 1", len(sent))
	}
	if sent[0].Overdue {
		t.Fatalf("the pre-due note must not read as overdue: %+v", sent[0])
	}

	// The same day again must send nothing. This is what makes a re-run safe,
	// and without it a retried job emails the customer twice.
	dunner.Run(ctx, true, day, now())
	if got := len(box.to(address)); got != 1 {
		t.Fatalf("%d emails after a re-run, want 1", got)
	}

	// Escalation: the subscription is gated, the invoice stays payable.
	for step := 1; step <= 4; step++ {
		d, _ := inv.Schedule().DunningDate(inv.DueDate, step)
		dunner.Run(ctx, true, d, now())
	}

	fresh, err := repositories.NewSubscriptionRepository(testDB, testCfg).
		Get(ctxT(t), org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != billing.SubscriptionPastDue {
		t.Fatalf("subscription = %s, want PAST_DUE", fresh.Status)
	}

	freshInv, err := repositories.NewInvoiceRepository(testDB, testCfg).
		Get(ctxT(t), org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Gating the service must not take away the ability to pay for it.
	if freshInv.Status != billing.InvoiceOpen {
		t.Fatalf("invoice = %s, want it still OPEN and payable", freshInv.Status)
	}
}

// TestDunningStopsWhenTheInvoiceIsPaid is the failure that matters most: an
// invoice paid between two steps must never be chased again.
func TestDunningStopsWhenTheInvoiceIsPaid(t *testing.T) {
	org := newOrg(t, true)
	inv, _, address := dunnableInvoice(t, org)

	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	if _, err := invoices.Transition(ctxT(t), inv, billing.InvoicePaid, billing.CauseManualPayment, "operator", "", now()); err != nil {
		t.Fatal(err)
	}

	box := &mailbox{}
	day, _ := inv.Schedule().DunningDate(inv.DueDate, 0)
	newDunner(t, box).Run(context.Background(), true, day, now())

	if got := box.to(address); len(got) != 0 {
		t.Fatalf("a paid invoice was chased: %+v", got)
	}
}

// TestDunningAbandonsAtTheEndOfThePolicy covers the terminal step. It is the one
// that ends a customer relationship, so it is worth pinning that it needs the
// whole schedule to have run rather than arriving early.
func TestDunningAbandonsAtTheEndOfThePolicy(t *testing.T) {
	org := newOrg(t, true)
	inv, sub, _ := dunnableInvoice(t, org)

	dunner := newDunner(t, &mailbox{})
	ctx := context.Background()
	for step := range billing.DefaultDunningPolicy {
		day, ok := inv.Schedule().DunningDate(inv.DueDate, step)
		if !ok {
			t.Fatalf("policy step %d has no date", step)
		}
		dunner.Run(ctx, true, day, now())
	}

	freshInv, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctxT(t), org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if freshInv.Status != billing.InvoiceUncollectible {
		t.Fatalf("invoice = %s, want UNCOLLECTIBLE", freshInv.Status)
	}

	freshSub, err := repositories.NewSubscriptionRepository(testDB, testCfg).Get(ctxT(t), org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if freshSub.Status != billing.SubscriptionCanceled {
		t.Fatalf("subscription = %s, want CANCELED", freshSub.Status)
	}

	// And it is out of the queue: nothing is scheduled for it any more. Read from
	// the row rather than from the run's totals, which are cross-tenant.
	if freshInv.DunningStep < len(billing.DefaultDunningPolicy) {
		t.Fatalf("dunning step = %d, want the policy exhausted (%d)", freshInv.DunningStep, len(billing.DefaultDunningPolicy))
	}
}

// activateSubscription applies a first payment's effect without a wallet.
//
// It is the same edge Collector.activateSubscription walks (CauseInvoicePaid),
// used by tests whose subject is what happens *after* activation and which have
// no payment rail wired. Tests about the payment itself use the real one.
func activateSubscription(t *testing.T, org *billing.Organization, subscriptionID string) {
	t.Helper()
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	sub, err := subs.Get(ctxT(t), org.ID, org.Livemode, subscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status == billing.SubscriptionActive {
		return
	}
	if _, err := subs.Transition(ctxT(t), sub, billing.SubscriptionActive,
		billing.CauseInvoicePaid, "test", "", now()); err != nil {
		t.Fatal(err)
	}
}

// A subscription that never activated walks the same reminder policy — the bill
// is real and owed — and none of the escalation, because there is no service to
// restrict. It ends as CauseActivationExpired, not CauseDunningExhausted: the
// customer never became a subscriber, so dunning did not give up on one.
//
// This is also the test that says there is no separate activation-expiry job.
func TestDunningNeverRestrictsAServiceNobodyHad(t *testing.T) {
	org := newOrg(t, true)
	inv, sub, address := unactivatedDunnableInvoice(t, org)

	box := &mailbox{}
	dunner := newDunner(t, box)
	ctx := context.Background()
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)

	// Every reminder still goes out, including the pre-due note.
	for step := range billing.DefaultDunningPolicy {
		day, ok := inv.Schedule().DunningDate(inv.DueDate, step)
		if !ok {
			t.Fatalf("policy step %d has no date", step)
		}
		if res := dunner.Run(ctx, true, day, now()); len(res.Errors) > 0 {
			t.Fatalf("dunning step %d: %v", step, res.Errors)
		}
		// Through the escalation step the subscription must not move: PAST_DUE
		// would claim something was taken away.
		if step < len(billing.DefaultDunningPolicy)-1 {
			fresh, err := subs.Get(ctxT(t), org.ID, true, sub.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Status != billing.SubscriptionIncomplete {
				t.Fatalf("after step %d the subscription is %s, want it still INCOMPLETE", step, fresh.Status)
			}
		}
	}
	if len(box.to(address)) == 0 {
		t.Fatal("an unpaid first invoice must still be chased — the reminders are what get it paid")
	}

	fresh, err := subs.Get(ctxT(t), org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != billing.SubscriptionCanceled {
		t.Fatalf("subscription = %s, want CANCELED at the end of the policy", fresh.Status)
	}
	freshInv, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctxT(t), org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if freshInv.Status != billing.InvoiceUncollectible {
		t.Fatalf("invoice = %s, want UNCOLLECTIBLE", freshInv.Status)
	}

	trail, err := repositories.NewAuditRepository(testDB, testCfg).
		ListForEntity(ctxT(t), org.ID, true, sub.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range trail {
		if entry.Cause == billing.CauseDunningExhausted {
			t.Fatalf("a subscription that never activated was ended as if it had: %+v", entry)
		}
	}
	var expired bool
	for _, entry := range trail {
		if entry.Cause == billing.CauseActivationExpired {
			expired = true
		}
	}
	if !expired {
		t.Fatalf("nothing records that activation never happened: %+v", trail)
	}
}
