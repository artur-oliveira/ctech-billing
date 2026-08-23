package v1

import (
	"bufio"
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/observability"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// Live settlement for the payment screen, over Server-Sent Events.
//
// The screen it serves is P3 with a PIX charge open: the reader has the code in
// their bank app and is waiting to be told it worked. Without this they either
// poll — a thirty-minute PIX window at three-second intervals is six hundred
// requests to learn one fact — or they refresh by hand and are told nothing at
// all when they stop.
//
// SSE and not a WebSocket because the traffic is one-directional and ends after
// one message: the server says "paid" and closes. A socket would buy
// bidirectionality nobody needs, and cost an upgrade path through the edge.

const (
	// How long a stream may live. A PIX charge expires well inside this; the cap
	// exists so a tab left open overnight is not a connection held all night.
	eventsMaxDuration = 35 * time.Minute

	// The re-read interval **when there is no notification bus**: fast while
	// somebody is plainly watching, slower once they have walked away.
	eventsPollFast   = 2 * time.Second
	eventsPollSlow   = 6 * time.Second
	eventsFastWindow = 90 * time.Second

	// The re-read interval when there **is** one. Two orders of magnitude
	// slower, because it is no longer how the answer arrives — it is the net
	// under a fire-and-forget message. Pub/sub has no replay: a subscriber that
	// reconnects a moment late misses the publish, and without this the screen
	// would wait for something that already happened.
	eventsPollBacked = 30 * time.Second

	// Proxies and load balancers close connections that go quiet. A comment
	// frame is legal SSE that no client will parse as an event.
	eventsHeartbeat = 15 * time.Second
)

// invoiceEvents streams the settlement of one invoice.
//
// Two signals, and the pairing is the design. The webhook that settles a charge
// arrives at whichever instance the edge picked, which is not the instance
// holding this connection the moment the ASG runs more than one — so the
// settlement is **published over Valkey** and this handler subscribes.
//
// It also keeps re-reading the invoice, thirty seconds apart. Pub/sub is
// fire-and-forget: nothing replays a message to a subscriber that was a
// millisecond late, and a screen that trusted it alone would occasionally spin
// forever on a payment that already went through. The publish makes the common
// case instant; the re-read makes every case correct.
//
// With no Valkey configured there is no publish, and the re-read speeds back up
// to two seconds — the same behaviour this handler had before, which is what
// makes a single-instance deployment work with no configuration at all.
func (h *portalHandlers) invoiceEvents(c fiber.Ctx) error {
	t := middleware.GetTenant(c)
	customer := middleware.GetCustomer(c)

	inv, err := h.invoices.Get(c.Context(), t.OrganizationID, t.Livemode, c.Params("id"))
	if err != nil {
		return fail(c, err)
	}
	// Same answer as getInvoice for the same reasons: another customer's invoice
	// and a nonexistent one are indistinguishable from outside.
	if inv.CustomerID != customer.ID || inv.Status == billing.InvoiceDraft {
		return problem.NotFound("fatura não encontrada").Send(c)
	}

	// Everything the stream needs is copied out here. The writer below runs after
	// this handler has returned, when the fiber Ctx and its context are no longer
	// valid to touch.
	var (
		orgID     = t.OrganizationID
		livemode  = t.Livemode
		invoiceID = inv.ID
		settled   = isSettled(inv)
	)

	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache")
	c.Set(fiber.HeaderConnection, "keep-alive")
	// Nginx sits in front of this service and buffers responses by default,
	// which for a stream means the client receives nothing until it ends.
	c.Set("X-Accel-Buffering", "no")

	bus := h.bus
	logCtx := observability.WithRequestID(context.Background(), middleware.GetRequestID(c))

	return c.SendStreamWriter(func(w *bufio.Writer) {
		if settled {
			writeEvent(logCtx, w, "paid")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), eventsMaxDuration)
		defer cancel()

		// Subscribed **before** the first re-read below, not after. Between a read
		// that says OPEN and a subscription that starts a moment later there is a
		// window, and a settlement landing inside it is a message nobody receives.
		var settledCh <-chan struct{}
		if bus != nil {
			settledCh = bus.Watch(ctx, invoiceID)
		}

		started := time.Now()
		lastBeat := started

		for {
			interval := eventsPollFast
			switch {
			case bus != nil:
				interval = eventsPollBacked
			case time.Since(started) > eventsFastWindow:
				interval = eventsPollSlow
			}

			select {
			case <-ctx.Done():
				// The cap, not the answer. Say so rather than closing silently, so
				// the client can stop waiting instead of assuming it lost the event.
				writeEvent(logCtx, w, "timeout")
				return
			case <-settledCh:
				// The notification arrived. Fall through to the re-read below rather
				// than reporting "paid" on the strength of the message: the message
				// says something happened, the row says what.
			case <-time.After(interval):
			}

			current, err := h.invoices.Get(ctx, orgID, livemode, invoiceID)
			if err != nil {
				// A failed read is this instance's problem, not an answer about the
				// invoice. Keep waiting; the heartbeat below still proves the
				// connection is alive, and a transient DynamoDB error must not tell
				// somebody mid-payment that their charge went wrong.
				observability.Warn(logCtx, "portal events invoice refresh failed", err, "invoice_id", invoiceID)
				if !beat(logCtx, w, &lastBeat) {
					return
				}
				continue
			}

			if isSettled(current) {
				writeEvent(logCtx, w, "paid")
				return
			}
			// A charge that can no longer be paid ends the wait too: staying open
			// on a voided invoice is a screen that spins until the tab is closed.
			// Same condition portal_dto.go uses for `payable`, so the stream and
			// the payload cannot disagree about whether there is still something
			// to wait for.
			if current.Status != billing.InvoiceOpen {
				writeEvent(logCtx, w, "closed")
				return
			}
			if !beat(logCtx, w, &lastBeat) {
				return
			}
		}
	})
}

func isSettled(inv *billing.Invoice) bool {
	return inv.Status == billing.InvoicePaid || inv.AmountDue() <= 0
}

// beat flushes a comment frame when the connection has been quiet, and reports
// whether the client is still there. A failed flush is the only signal fasthttp
// gives that the reader hung up.
func beat(ctx context.Context, w *bufio.Writer, last *time.Time) bool {
	if time.Since(*last) < eventsHeartbeat {
		return true
	}
	if _, err := w.WriteString(": ping\n\n"); err != nil {
		observability.Warn(ctx, "portal events heartbeat write failed", err)
		return false
	}
	if err := w.Flush(); err != nil {
		observability.Warn(ctx, "portal events heartbeat flush failed", err)
		return false
	}
	*last = time.Now()
	return true
}

func writeEvent(ctx context.Context, w *bufio.Writer, name string) {
	if _, err := w.WriteString("event: " + name + "\ndata: {}\n\n"); err != nil {
		observability.Warn(ctx, "portal event write failed", err, "event", name)
		return
	}
	if err := w.Flush(); err != nil {
		observability.Warn(ctx, "portal event flush failed", err, "event", name)
	}
}
