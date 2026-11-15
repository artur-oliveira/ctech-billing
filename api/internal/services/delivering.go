package services

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"gopkg.aoctech.app/api-commons/webhooks"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Header prefix and agent of billing's deliveries. The prefix is part of the
// contract with every receiver (wallet, ctech-dfe verify X-Billing-Signature), so
// it stays when the engine moves to go-common.
const (
	webhookHeaderPrefix = "X-Billing-"
	webhookUserAgent    = "ctech-billing-webhooks/1"
)

// Deliverer moves events out to the endpoints that asked for them (ADR 0016).
//
// Two passes, deliberately separate:
//
//   - **Fan-out** matches an event to endpoints. It is the pass that reads
//     configuration, and keeping it out of the write path is why an invoice can
//     be paid while every webhook endpoint in the tenant is misconfigured. It
//     stays here: it is billing's routing (owner key, event filter).
//   - **Delivery** makes one HTTP attempt per (event, endpoint) and either
//     finishes it or schedules the next try. That is the shared engine,
//     go-common/webhooks, over webhookStore.
//
// Both are driven by cmd/deliver, both are cross-tenant, and neither has an HTTP
// surface; the same discipline as cmd/sweep (ADR 0002).
type Deliverer struct {
	hooks *repositories.WebhookRepository
	// merchant delivers to endpoints a merchant registered in the console (the
	// engine's guarded client: no non-public address, no redirect, no proxy);
	// plain to provisioned ones, which sit on CTech's private network.
	merchant, plain *http.Client
	workers         int
	// engines is the delivery engine per mode (test, live), built once so the
	// clients' connection pools are kept.
	engines [2]*webhooks.Deliverer
}

func NewDeliverer(hooks *repositories.WebhookRepository) *Deliverer {
	d := &Deliverer{
		hooks:    hooks,
		merchant: webhooks.NewGuardedClient(webhooks.DeliveryTimeout),
		plain:    &http.Client{Timeout: webhooks.DeliveryTimeout},
		workers:  1,
	}
	d.build()
	return d
}

func (d *Deliverer) build() {
	for i := range d.engines {
		d.engines[i] = webhooks.NewDeliverer(&webhookStore{repo: d.hooks, livemode: i == 1}, webhooks.Config{
			HeaderPrefix: webhookHeaderPrefix, UserAgent: webhookUserAgent,
			Workers: d.workers, Client: d.merchant, TrustedClient: d.plain,
		})
	}
}

func (d *Deliverer) engine(livemode bool) *webhooks.Deliverer {
	if livemode {
		return d.engines[1]
	}
	return d.engines[0]
}

// WithConcurrency sets how many endpoints a pass delivers to at once. The
// default is 1: serial. The deliveries of one endpoint are always made one at a
// time, in order, so a consumer is never hit by its own backlog in parallel; what
// the pool adds is that a slow or dead endpoint no longer holds up every other
// tenant's deliveries behind it.
func (d *Deliverer) WithConcurrency(workers int) *Deliverer {
	d.workers = max(workers, 1)
	d.build()
	return d
}

// SetHTTPClient replaces both clients. Tests only: they deliver to httptest
// servers on 127.0.0.1, which the guarded client would refuse by design.
func (d *Deliverer) SetHTTPClient(c *http.Client) {
	d.merchant, d.plain = c, c
	d.build()
}

// PassResult is what one run of either pass did.
type PassResult struct {
	Examined int
	Handled  int
	Deferred int
	Failed   int
	Errors   []string
	// fresh are the deliveries a fan-out pass created, to be attempted without
	// going through the delivery queue.
	fresh []repositories.DueDelivery
}

func (r *PassResult) fail(format string, args ...any) {
	r.Failed++
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func (r *PassResult) add(o webhooks.Result) {
	r.Examined += o.Examined
	r.Handled += o.Handled
	r.Deferred += o.Deferred
	r.Failed += o.Failed
	r.Errors = append(r.Errors, o.Errors...)
}

// FanOut matches queued events to endpoints.
//
// Endpoints are read once per (organization, mode) per pass rather than once per
// event. A tenant that just had forty invoices finalized by the sweep is the
// normal case, and forty identical reads of the same three endpoints is the
// difference between a job that costs nothing and one that shows up on a bill.
func (d *Deliverer) FanOut(ctx context.Context, livemode bool, limit int, now time.Time) PassResult {
	var res PassResult

	events, err := d.hooks.PendingEvents(ctx, livemode, limit)
	if err != nil {
		res.fail("reading the fan-out queue: %v", err)
		return res
	}
	res.Examined = len(events)

	type tenant struct {
		org      string
		livemode bool
	}
	cache := map[tenant][]billing.WebhookEndpoint{}

	for i := range events {
		ev := &events[i]
		key := tenant{org: ev.OrganizationID, livemode: ev.Livemode}
		endpoints, cached := cache[key]
		if !cached {
			endpoints, err = d.hooks.ListEndpoints(ctx, ev.OrganizationID, ev.Livemode)
			if err != nil {
				res.fail("reading endpoints of %s: %v", ev.OrganizationID, err)
				continue
			}
			cache[key] = endpoints
		}

		wanted := make([]billing.WebhookEndpoint, 0, len(endpoints))
		for _, e := range endpoints {
			if e.Wants(ev) {
				wanted = append(wanted, e)
			}
		}

		// An event nobody wants still leaves the queue. "No endpoint is
		// registered for this" is an answer, and leaving it pending would build a
		// backlog that grows forever in a tenant with no webhooks at all —
		// which is every tenant until somebody registers one.
		made, err := d.hooks.FanOutRows(ctx, ev, wanted, now)
		if err != nil {
			res.fail("fanning out %s: %v", ev.ID, err)
			continue
		}
		res.Handled += len(made)
		for _, m := range made {
			res.fresh = append(res.fresh, repositories.DueDelivery{Delivery: m, Event: *ev})
		}
	}
	return res
}

// FanOutAndDeliver is both passes with the second one fed by the first.
//
// The delivery queue is an index, and an index shows a row a moment after it is
// written. Run back to back, the delivery pass would not see what the fan-out
// pass had just created, and the delivery would wait for the next run of the job:
// a minute, for an event somebody is watching a screen for. So the deliveries the
// fan-out made are attempted directly, and the queue is then read for the backlog
// (anything it also shows of those is skipped: they were just tried).
func (d *Deliverer) FanOutAndDeliver(ctx context.Context, livemode bool, limit int, now time.Time) (fan, del PassResult) {
	fan = d.FanOut(ctx, livemode, limit, now)
	engine := d.engine(livemode)
	tried := make(map[string]bool, len(fan.fresh))
	fresh := make([]webhooks.Due, 0, len(fan.fresh))
	for _, row := range fan.fresh {
		due, err := toDue(row)
		if err != nil {
			del.fail("%v", err)
			continue
		}
		fresh = append(fresh, due)
		tried[webhooks.Key(due.Delivery)] = true
	}
	del.add(engine.DeliverFresh(ctx, fresh, now))
	del.add(engine.DeliverExcept(ctx, limit, now, tried))
	return fan, del
}

// Deliver attempts the deliveries that are due.
func (d *Deliverer) Deliver(ctx context.Context, livemode bool, limit int, now time.Time) PassResult {
	var res PassResult
	res.add(d.engine(livemode).Deliver(ctx, limit, now))
	return res
}
