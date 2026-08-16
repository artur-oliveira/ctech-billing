package billing

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Outbound webhooks: how a consuming product learns that something happened
// here without polling for it (ADR 0016).
//
// The hard question this design answers is **which** endpoint an event belongs
// to. In tenant zero — CTech billing its own customers, ADR 0012 — every CTech
// service is a client of one organization, so an endpoint registered per
// organization would send ctech-dfe every invoice ctech-poker issued. Routing is
// therefore per **product owner**, which survives the thing that breaks every
// other candidate: a subscription an operator creates by hand in the console has
// no calling client to route by, but it still has a price, and a price still has
// a product.

// ErrInvalidEndpoint reports an endpoint that cannot be delivered to.
var ErrInvalidEndpoint = errors.New("invalid webhook endpoint")

// EndpointStatus is whether an endpoint is delivered to.
type EndpointStatus string

const (
	// EndpointActive is delivered to.
	EndpointActive EndpointStatus = "active"
	// EndpointDisabled is not. Set by an operator, or by the delivery job after
	// a run of failures long enough that the endpoint is plainly gone.
	EndpointDisabled EndpointStatus = "disabled"
)

// MaxFailureRun is how many consecutive failures disable an endpoint.
//
// It exists because an endpoint that has been answering 404 for a week is not a
// transient failure, and retrying it forever turns the delivery job's backlog
// into a permanent queue of things that will never succeed — which is how the
// one delivery that *would* have worked waits behind them.
const MaxFailureRun = 12

// WebhookEndpoint is one destination.
//
// It is its own entity rather than fields on APICredential, which was the
// obvious place and the wrong one. A credential is a **reference** to a client
// in ctech-account and stores nothing about it, so rotating the OAuth client
// would silently take the endpoint with it. An endpoint also outlives, and
// multiplies against, credentials: one consumer may want two of them, and a
// subscription created in the console has no credential at all.
type WebhookEndpoint struct {
	ID             string `dynamodbav:"id"              json:"id"`
	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`

	// URL must be https. A billing event says which invoice moved, and that is
	// enough to be worth not sending in the clear.
	URL string `dynamodbav:"url" json:"url"`

	// Secret signs the delivery. Sealed at rest by the repository, like a tax id,
	// and never returned by any read path that a browser can reach.
	Secret string `dynamodbav:"secret" json:"-"`

	// Events filters by type. Empty means every type, which is what a consumer
	// that just wants to stay in sync actually wants.
	Events []EventType `dynamodbav:"events,omitempty" json:"events,omitempty"`

	// OwnerKey filters by the product's owner. Empty means every product in the
	// organization — correct for an ordinary merchant, who owns their whole
	// catalogue, and wrong only for tenant zero, which is why the field exists.
	OwnerKey string `dynamodbav:"owner_key,omitempty" json:"owner_key,omitempty"`

	Status EndpointStatus `dynamodbav:"status" json:"status"`
	// FailureRun counts consecutive failed deliveries and resets on any success.
	// Total failures would disable a busy endpoint that fails 1% of the time,
	// which is a working endpoint.
	FailureRun int    `dynamodbav:"failure_run" json:"failure_run"`
	DisabledAt string `dynamodbav:"disabled_at,omitempty" json:"disabled_at,omitempty"`
}

// Validate reports whether the endpoint can be delivered to.
func (e *WebhookEndpoint) Validate() error {
	if e.ID == "" || e.OrganizationID == "" {
		return fmt.Errorf("%w: needs an id and an organization", ErrInvalidEndpoint)
	}
	parsed, err := url.Parse(e.URL)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: %q is not a URL", ErrInvalidEndpoint, e.URL)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("%w: %q is not https", ErrInvalidEndpoint, e.URL)
	}
	// A signature nobody can verify is a signature nobody checks. Refusing here
	// means an unsigned delivery is not a state this system can reach.
	if len(e.Secret) < 32 {
		return fmt.Errorf("%w: secret must be at least 32 characters", ErrInvalidEndpoint)
	}
	for _, t := range e.Events {
		if !strings.Contains(string(t), ".") {
			return fmt.Errorf("%w: %q is not an event type", ErrInvalidEndpoint, t)
		}
	}
	switch e.Status {
	case "", EndpointActive, EndpointDisabled:
	default:
		return fmt.Errorf("%w: unknown status %q", ErrInvalidEndpoint, e.Status)
	}
	return nil
}

// Wants reports whether this endpoint should receive an event.
//
// Both filters are "empty means everything", and the asymmetry is the point:
// the common case — a merchant who owns their catalogue and wants to stay in
// sync — is configured by registering a URL and nothing else. Only tenant zero,
// which is the only place the problem exists, carries the filter.
func (e *WebhookEndpoint) Wants(ev *Event) bool {
	if e.Status != EndpointActive {
		return false
	}
	if e.OwnerKey != "" && e.OwnerKey != ev.OwnerKey {
		return false
	}
	if len(e.Events) > 0 && !slices.Contains(e.Events, ev.Type) {
		return false
	}
	return true
}

// Event is one thing that happened, recorded in the same transaction as the
// change it describes.
//
// It is written **before** any endpoint is known. Resolving destinations needs
// reads — the tenant's endpoints — and doing them inside a status-change
// transaction would put three lookups on the path of every invoice that moves.
// The delivery job resolves them instead, which costs a second pass and buys a
// write path that cannot be slowed down or failed by a webhook configuration.
type Event struct {
	ID             string `dynamodbav:"id"              json:"id"`
	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`

	Type EventType `dynamodbav:"type" json:"type"`

	// Entity and EntityID say what moved. They are the whole of the payload's
	// `data` — see Payload.
	Entity   string `dynamodbav:"entity"    json:"entity"`
	EntityID string `dynamodbav:"entity_id" json:"entity_id"`

	// SubscriptionID is set when the subject is, or belongs to, a subscription.
	// It is in the payload because it is the id a consumer keyed its own records
	// on, and absent for a one-off invoice.
	SubscriptionID string `dynamodbav:"subscription_id,omitempty" json:"subscription_id,omitempty"`

	// OwnerKey is the routing key, copied from the subscription at emission.
	// Copied rather than derived at delivery time because deriving it means
	// subscription → items → prices → products, which is three reads to answer a
	// question whose answer cannot change: a subscription does not move between
	// services.
	OwnerKey string `dynamodbav:"owner_key,omitempty" json:"-"`

	OccurredAt string `dynamodbav:"occurred_at" json:"occurred_at"`
}

// Payload is what is actually sent, and it is deliberately thin: an id, a type,
// and which object moved.
//
// **No amounts, no customer, no status.** The consumer reads the entity back
// through the API with its own credential, which is the same posture billing
// takes toward wallet's notify-back — a webhook is a wake-up signal, never
// authority. Two things follow that are worth the smaller payload: a
// misconfigured URL leaks an id rather than a customer's bill, and a consumer
// cannot build logic on a field that was accurate when the event was queued and
// stale by the time it arrived.
type Payload struct {
	ID         string    `json:"id"`
	Type       EventType `json:"type"`
	Livemode   bool      `json:"livemode"`
	OccurredAt string    `json:"occurred_at"`
	Data       struct {
		Object         string `json:"object"`
		ID             string `json:"id"`
		SubscriptionID string `json:"subscription_id,omitempty"`
	} `json:"data"`
}

// Payload renders the event for the wire.
func (e *Event) Payload() Payload {
	var p Payload
	p.ID, p.Type, p.Livemode, p.OccurredAt = e.ID, e.Type, e.Livemode, e.OccurredAt
	p.Data.Object, p.Data.ID, p.Data.SubscriptionID = e.Entity, e.EntityID, e.SubscriptionID
	return p
}

// DeliveryStatus is where one (event, endpoint) pair stands.
type DeliveryStatus string

const (
	// DeliveryPending is queued or waiting on a backoff.
	DeliveryPending DeliveryStatus = "pending"
	// DeliveryDelivered means the endpoint answered 2xx.
	DeliveryDelivered DeliveryStatus = "delivered"
	// DeliveryFailed means the attempts are exhausted. It is terminal, and the
	// row is kept: "we tried and gave up" is an answer somebody will need.
	DeliveryFailed DeliveryStatus = "failed"
)

// MaxDeliveryAttempts is where one event stops being retried.
//
// Eight attempts on the backoff below spans roughly two days, which is the
// window in which a consumer's outage is plausibly fixed. Beyond that the
// consumer has a gap they must reconcile from the API, and pretending otherwise
// by retrying for a week only delays their discovering it.
const MaxDeliveryAttempts = 8

// Delivery is one event's journey to one endpoint.
//
// One row per pair, not per event: two endpoints subscribed to the same event
// fail and succeed independently, and a single row would make one consumer's
// outage look like the other's.
type Delivery struct {
	EventID    string `dynamodbav:"event_id"    json:"event_id"`
	EndpointID string `dynamodbav:"endpoint_id" json:"endpoint_id"`

	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`

	Status   DeliveryStatus `dynamodbav:"status"   json:"status"`
	Attempts int            `dynamodbav:"attempts" json:"attempts"`

	// NextAttemptAt is when the delivery job should pick this up. It is also the
	// sort key of the schedule index, so "what is due" is the natural order of
	// the partition rather than a filter over it.
	NextAttemptAt  string `dynamodbav:"next_attempt_at,omitempty" json:"next_attempt_at,omitempty"`
	LastStatusCode int    `dynamodbav:"last_status_code,omitempty" json:"last_status_code,omitempty"`
	LastError      string `dynamodbav:"last_error,omitempty" json:"last_error,omitempty"`
	DeliveredAt    string `dynamodbav:"delivered_at,omitempty" json:"delivered_at,omitempty"`
}

// Backoff is how long to wait before attempt n (1-based).
//
// Exponential from 30 seconds, capped at 12 hours. The cap is what keeps the
// tail of the schedule readable: without it attempt 8 lands eleven days out,
// long after anybody would have investigated, and the row sits in the index
// looking like work that is about to happen.
func Backoff(attempt int) time.Duration {
	const (
		base = 30 * time.Second
		cap_ = 12 * time.Hour
	)
	if attempt < 1 {
		attempt = 1
	}
	d := base << min(attempt-1, 12)
	return min(d, cap_)
}
