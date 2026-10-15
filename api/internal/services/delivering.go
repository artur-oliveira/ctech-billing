package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"syscall"
	"time"

	"gopkg.aoctech.app/api-commons/observability"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Deliverer moves events out to the endpoints that asked for them (ADR 0016).
//
// Two passes, deliberately separate:
//
//   - **Fan-out** matches an event to endpoints. It is the pass that reads
//     configuration, and keeping it out of the write path is why an invoice can
//     be paid while every webhook endpoint in the tenant is misconfigured.
//   - **Delivery** makes one HTTP attempt per (event, endpoint) and either
//     finishes it or schedules the next try.
//
// Both are driven by cmd/deliver, both are cross-tenant, and neither has an HTTP
// surface — the same discipline as cmd/sweep (ADR 0002).
type Deliverer struct {
	hooks  *repositories.WebhookRepository
	client *http.Client
	// merchant delivers to endpoints a merchant registered in the console. Its
	// dialer refuses any non-public address after DNS resolution, so neither a
	// hostname pointed at 10.x nor a DNS answer that changes after the create
	// check reaches CTech's network; it follows no redirect (a 3xx is a failed
	// attempt) and uses no proxy.
	merchant *http.Client
}

var errPrivateAddress = errors.New("webhook host resolves to a non-public address")

func newMerchantClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: refusePrivate}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = dialer.DialContext
	return &http.Client{
		Timeout:   deliveryTimeout,
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// refusePrivate runs after resolution, on the address actually dialled.
func refusePrivate(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("webhook dial %q: %w", address, err)
	}
	if !billing.PublicAddr(ap.Addr()) {
		return errPrivateAddress
	}
	return nil
}

// deliveryTimeout is per attempt. Generous enough for a consumer that writes to
// its own database before answering, short enough that one hung endpoint cannot
// hold the pass open behind everything else waiting.
const deliveryTimeout = 10 * time.Second

// disabledRecheck is how far down the queue a disabled provisioned endpoint's
// delivery moves: re-enabling it sends what it missed within the hour.
const disabledRecheck = time.Hour

func NewDeliverer(hooks *repositories.WebhookRepository) *Deliverer {
	return &Deliverer{
		hooks:    hooks,
		client:   &http.Client{Timeout: deliveryTimeout},
		merchant: newMerchantClient(),
	}
}

// SetHTTPClient replaces both clients. Tests only: they deliver to httptest
// servers on 127.0.0.1, which the merchant client would refuse by design.
func (d *Deliverer) SetHTTPClient(c *http.Client) { d.client, d.merchant = c, c }

// clientFor picks the guarded client for anything a merchant registered.
func (d *Deliverer) clientFor(e *billing.WebhookEndpoint) *http.Client {
	if e.Provisioned() {
		return d.client
	}
	return d.merchant
}

// PassResult is what one run of either pass did.
type PassResult struct {
	Examined int
	Handled  int
	Deferred int
	Failed   int
	Errors   []string
}

func (r *PassResult) fail(format string, args ...any) {
	r.Failed++
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
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
		n, err := d.hooks.FanOut(ctx, ev, wanted, now)
		if err != nil {
			res.fail("fanning out %s: %v", ev.ID, err)
			continue
		}
		res.Handled += n
	}
	return res
}

// Deliver attempts the deliveries that are due.
//
// The queue is ordered by due time, so the pass stops at the first row that is
// not due yet rather than filtering — everything after it is further in the
// future by construction.
func (d *Deliverer) Deliver(ctx context.Context, livemode bool, limit int, now time.Time) PassResult {
	var res PassResult

	due, err := d.hooks.DueDeliveries(ctx, livemode, limit)
	if err != nil {
		res.fail("reading the delivery queue: %v", err)
		return res
	}

	for i := range due {
		item := &due[i]
		if !isDue(item.Delivery.NextAttemptAt, now) {
			res.Deferred = len(due) - i
			break
		}
		res.Examined++

		endpoint, err := d.hooks.GetEndpoint(ctx, item.Delivery.OrganizationID, item.Delivery.Livemode, item.Delivery.EndpointID)
		if err != nil {
			// The endpoint was deleted under a queued delivery. Not retryable, and
			// not an error worth alarming on: there is nowhere to send it.
			if markErr := d.hooks.MarkAttemptFailed(ctx, &item.Delivery, 0, "endpoint no longer exists", now); markErr != nil {
				res.fail("closing orphaned delivery %s: %v", item.Delivery.EventID, markErr)
			}
			continue
		}
		// A disabled endpoint's rows must leave the head of the queue: the queue
		// is shared by every tenant, and rows left in place would be the whole
		// of every later batch. A provisioned endpoint (an operator may
		// re-enable it) keeps what it missed, an hour further down; a console
		// endpoint cannot be re-enabled (delete and add again), so its rows close.
		if endpoint.Status != billing.EndpointActive {
			var markErr error
			if endpoint.Provisioned() {
				markErr = d.hooks.DeferDelivery(ctx, &item.Delivery, now.Add(disabledRecheck), now)
			} else {
				markErr = d.hooks.CloseDelivery(ctx, &item.Delivery, "endpoint disabled", now)
			}
			if markErr != nil {
				res.fail("moving delivery %s of a disabled endpoint: %v", item.Delivery.EventID, markErr)
			}
			res.Deferred++
			continue
		}

		began := time.Now()
		status, answer, err := d.post(ctx, endpoint, &item.Event, now)
		attempt := billing.DeliveryAttempt{
			Number: item.Delivery.Attempts + 1, StatusCode: status, DurationMs: time.Since(began).Milliseconds(), Response: answer,
		}
		if err != nil {
			attempt.Error = err.Error()
		}
		// Kept before the delivery row moves on, and idempotent on the number, so a
		// crash between the two repeats nothing. A failure to keep it is logged and
		// never stops the delivery: the history is evidence, not the work.
		if recErr := d.hooks.RecordAttempt(ctx, &item.Delivery, attempt, now); recErr != nil {
			slog.Warn("webhook attempt not recorded", "event", item.Event.ID, "endpoint", endpoint.ID, "error", recErr)
		}
		switch {
		case err == nil:
			if err := d.hooks.MarkDelivered(ctx, &item.Delivery, status, now); err != nil {
				res.fail("recording delivery of %s: %v", item.Event.ID, err)
				continue
			}
			res.Handled++
		default:
			if err := d.hooks.MarkAttemptFailed(ctx, &item.Delivery, status, err.Error(), now); err != nil {
				res.fail("recording failed attempt for %s: %v", item.Event.ID, err)
				continue
			}
			slog.Warn("webhook delivery failed",
				"event", item.Event.ID,
				"endpoint", endpoint.ID,
				"attempt", item.Delivery.Attempts+1,
				"status", status,
				"error", err)
		}
		// Endpoint health is updated on every attempt, success or not, so a run of
		// failures eventually disables a destination that is plainly gone and a
		// single success clears the count.
		if err := d.hooks.RecordEndpointHealth(ctx, endpoint, err == nil, failureDetail(status, err), now); err != nil {
			res.fail("recording endpoint health for %s: %v", endpoint.ID, err)
		}
	}
	return res
}

// post makes one signed attempt. It returns the status code alongside the error
// so a failure can record what the endpoint actually said.
func (d *Deliverer) post(ctx context.Context, e *billing.WebhookEndpoint, ev *billing.Event, now time.Time) (int, string, error) {
	body, err := json.Marshal(ev.Payload())
	if err != nil {
		return 0, "", fmt.Errorf("rendering payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("building request: %w", err)
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ctech-billing-webhooks/1")
	// The event id is a header as well as a payload field so a consumer can
	// deduplicate before parsing anything. Deliveries are at-least-once: a
	// response that times out after the endpoint committed is indistinguishable
	// from one that never arrived, and this service will retry it.
	req.Header.Set("X-Billing-Event-Id", ev.ID)
	req.Header.Set("X-Billing-Event-Type", string(ev.Type))
	req.Header.Set("X-Billing-Timestamp", timestamp)
	req.Header.Set("X-Billing-Signature", "v1="+Sign(e.Secret, timestamp, body))

	resp, err := d.clientFor(e).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			observability.Warn(ctx, "webhook response body close failed", closeErr, "event_id", ev.ID)
		}
	}()
	// Drained so the connection can be reused. An undrained body is a new TCP
	// handshake per delivery, which for a job that retries is a lot of them.
	// The first bytes of the answer are kept with the attempt (a 401 from the
	// consumer and an HTML page from a gateway in front of it are different
	// problems), the rest is drained.
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("draining endpoint response: %w", err)
	}
	snippet := string(answer)
	if len(snippet) > 512 {
		snippet = snippet[:512]
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, snippet, fmt.Errorf("endpoint answered %d", resp.StatusCode)
	}
	return resp.StatusCode, snippet, nil
}

// Sign renders the delivery signature: HMAC-SHA256 over "timestamp.body".
//
// The timestamp is inside the signed material, not merely a header beside it.
// Signing the body alone produces a value that stays valid forever, so anybody
// who captures one delivery can replay it at will; binding the time lets a
// consumer refuse anything older than their own tolerance.
//
// It is exported because a consumer in this family verifies with the same
// function, and two implementations of one signature scheme is how a rollout
// discovers that trailing whitespace matters.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func isDue(at string, now time.Time) bool {
	if at == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		// An unparseable due time is a corrupt row, and holding it back forever
		// would hide it. Attempting it surfaces the problem where it can be seen.
		return true
	}
	return !t.After(now)
}

// failureDetail is what the console shows for a failed attempt: the status the
// endpoint answered with, or the transport error when it never answered.
func failureDetail(status int, err error) string {
	switch {
	case err == nil:
		return ""
	case status > 0:
		return fmt.Sprintf("HTTP %d", status)
	}
	return err.Error()
}
