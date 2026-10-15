//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Final review C1: deliveries queued for an endpoint that got disabled must not
// sit at the head of the shared queue. A merchant's dead endpoint used to keep
// its pending rows there forever, and once a batch's worth of them piled up no
// other tenant's delivery was attempted again.
func TestADisabledEndpointNeverBlocksTheQueue(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)

	dead := newOrg(t, true)
	e := merchantEndpoint(dead)
	if err := repo.AddEndpoint(ctx, e, 5, "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	// One ping per PingInterval, so the pings are spread out and every pass
	// below runs after the last of them.
	step := repositories.PingInterval + time.Second
	later := now().Add(61 * step)
	for i := 0; i < 60; i++ {
		at := now().Add(time.Duration(i) * step)
		if _, err := repo.EmitPing(ctx, e, at); err != nil {
			t.Fatal(err)
		}
	}
	d := newDeliverer(t, newConsumer(t, http.StatusOK))
	for {
		if res := d.FanOut(context.Background(), true, 100, later); res.Examined < 100 {
			break
		}
	}
	full, err := repo.GetEndpoint(ctx, dead.ID, true, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	full.FailureRun = billing.MaxFailureRun - 1
	if err := repo.RecordEndpointHealth(ctx, full, false, "HTTP 500", later); err != nil {
		t.Fatal(err)
	}

	alive := newOrg(t, true)
	c := newConsumer(t, http.StatusOK)
	target := registerEndpoint(t, alive, c, "")
	ev, err := repo.EmitPing(ctx, target, later)
	if err != nil {
		t.Fatal(err)
	}
	d = newDeliverer(t, c)
	for {
		if res := d.FanOut(context.Background(), true, 100, later); res.Examined < 100 {
			break
		}
	}
	// A few bounded passes, as cmd/deliver makes one a minute.
	for pass := 0; pass < 4; pass++ {
		d.Deliver(context.Background(), true, 50, later)
		for _, got := range c.deliveries() {
			if got.payload.ID == ev.ID {
				return
			}
		}
	}
	t.Fatal("the live endpoint's delivery never left the queue behind the disabled endpoint's rows")
}

// An endpoint remembers how its last delivery went, so the console can say
// whether it answers: "responding" after a success, the status code after a
// failure, and nothing before the first attempt.
func TestEndpointRemembersHowItsLastDeliveryWent(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)

	deliver := func(c *consumer) *billing.WebhookEndpoint {
		ep := registerEndpoint(t, org, c, "")
		if got, _ := repo.GetEndpoint(ctx, org.ID, org.Livemode, ep.ID); got.LastDeliveryAt != "" {
			t.Fatalf("before any delivery: %+v", got)
		}
		if _, err := repo.EmitPing(ctx, ep, now()); err != nil {
			t.Fatal(err)
		}
		d := newDeliverer(t, c)
		later := now().Add(time.Hour)
		for pass := 0; pass < 4; pass++ {
			d.FanOut(context.Background(), true, 100, later)
			d.Deliver(context.Background(), true, 50, later)
		}
		got, err := repo.GetEndpoint(ctx, org.ID, org.Livemode, ep.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := deliver(newConsumer(t, http.StatusOK)); !got.LastDeliveryOK || got.LastDeliveryAt == "" || got.LastDeliveryDetail != "" {
		t.Fatalf("after a 200: %+v", got)
	}
	if got := deliver(newConsumer(t, http.StatusNotFound)); got.LastDeliveryOK || got.LastDeliveryDetail != "HTTP 404" {
		t.Fatalf("after a 404: ok=%v detail=%q", got.LastDeliveryOK, got.LastDeliveryDetail)
	}
}

// The delivery pass must not depend on the queue index showing what the fan-out
// pass wrote a moment earlier: FanOutAndDeliver attempts what it just fanned out,
// once, and the backlog read does not try it again.
func TestWhatTheFanOutJustCreatedIsDeliveredInTheSameRunExactlyOnce(t *testing.T) {
	e := newAPI(t)
	cons := newConsumer(t, http.StatusOK)
	registerEndpoint(t, e.org, cons, "")
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-same-run", `{"customer_id":"`+cust+`","description":"x","amount_cents":5000,"reference":"same-run-1"}`)
	var made oneOffInvoice
	r.decode(t, &made)

	d := newDeliverer(t, cons)
	const batch = 100
	for {
		fan, _ := d.FanOutAndDeliver(context.Background(), e.org.Livemode, batch, time.Now())
		if fan.Examined < batch {
			break
		}
	}
	got := 0
	for _, dl := range cons.deliveries() {
		if dl.payload.Data.ID == made.ID {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("the invoice's event reached the endpoint %d times in one run", got)
	}
	page, err := repositories.NewWebhookRepository(testDB, testCfg).EventsOf(ctxT(t), e.org.ID, e.org.Livemode, "invoice", made.ID, nil, 5)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("%v %+v", err, page)
	}
	dels, _ := repositories.NewWebhookRepository(testDB, testCfg).ListDeliveries(ctxT(t), e.org.ID, e.org.Livemode, page.Items[0].ID)
	if len(dels) != 1 || dels[0].Status != billing.DeliveryDelivered || dels[0].Attempts != 1 {
		t.Fatalf("deliveries %+v", dels)
	}
}
