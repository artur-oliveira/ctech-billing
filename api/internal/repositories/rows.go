package repositories

import (
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// A row is a domain entity plus the attributes DynamoDB needs and the domain
// must not know about: the composite key, the index attributes and the TTL.
//
// The domain struct is embedded rather than duplicated field by field. Two
// copies of an entity's shape drift the first time someone adds a field to one
// of them, and the compiler says nothing.

// keys carries what every row has.
type keys struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`
	// TTL is the DynamoDB time-to-live attribute, absent for records that are
	// kept (ADR 0009). It is written on creation because it cannot be applied
	// retroactively.
	TTL *int64 `dynamodbav:"ttl,omitempty"`

	CreatedAt string `dynamodbav:"created_at"`
	UpdatedAt string `dynamodbav:"updated_at"`
}

func newKeys(pk, sk string, r Retention, now time.Time) keys {
	stamp := now.UTC().Format(time.RFC3339Nano)
	return keys{PK: pk, SK: sk, TTL: r.ExpiresAt(now), CreatedAt: stamp, UpdatedAt: stamp}
}

type organizationRow struct {
	keys
	billing.Organization
	// LookupPK resolves the organization from its owner's user id, for the
	// console session (ADR 0011). Sparse: an organization provisioned without an
	// owner is not reachable from a browser, which is the safe direction.
	LookupPK string `dynamodbav:"lookup_pk,omitempty"`
}

type customerRow struct {
	keys
	PeriodAttrs
	billing.Customer
	// LookupPK resolves the customer from the caller's own identifier. Sparse:
	// absent when the caller supplied no external ref, so the index only holds
	// rows that are actually addressable that way.
	LookupPK string `dynamodbav:"lookup_pk,omitempty"`
}

// customerUserRow points a ctech-account subject at the customer record that is
// them, inside one organization (ADR 0012). It is written in the same
// transaction as the customer and conditionally, so two customers in one
// organization cannot claim the same account.
type customerUserRow struct {
	keys
	UserID     string `dynamodbav:"user_id"`
	CustomerID string `dynamodbav:"customer_id"`
}

type productRow struct {
	keys
	billing.Product
}

type priceRow struct {
	keys
	billing.Price
}

type subscriptionRow struct {
	keys
	PeriodAttrs
	billing.Subscription
	// ScheduleKeys are set only while the subscription is due to be swept. A
	// canceled or paused subscription carries neither, so the sweep index stays
	// the size of the work to do rather than the size of the history.
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

// entity is the subscription as the rest of the service sees it, with the one
// attribute the domain cannot carry itself filled in. Every read path goes
// through it, because a Since that is populated on the detail and empty in the
// list is worse than one that is never populated at all.
func (row subscriptionRow) entity() billing.Subscription {
	sub := row.Subscription
	sub.Since = row.CreatedAt
	return sub
}

type subscriptionItemRow struct {
	keys
	billing.SubscriptionItem
}

type invoiceRow struct {
	keys
	PeriodAttrs
	billing.Invoice
	// GenerationKey is the {subscription_item_id}:{period_start} idempotency key.
	// Stored on the row so a human debugging a duplicate can see which key
	// produced it, and mirrored into a lookup row for the scheduler's pre-check.
	GenerationKey string `dynamodbav:"generation_key,omitempty"`
	SchedulePK    string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK    string `dynamodbav:"schedule_sk,omitempty"`
	// LookupPK groups the invoice under its subscription. Sparse: absent on a
	// one-off invoice, which is why the subscription screen's query can never
	// return one. Written once at Create — an invoice's subscription does not
	// change, so nothing else has to maintain it.
	LookupPK string `dynamodbav:"lookup_pk,omitempty"`
}

type invoiceItemRow struct {
	keys
	billing.InvoiceItem
	InvoiceID string `dynamodbav:"invoice_id"`
	Line      int    `dynamodbav:"line"`
}

type paymentAttemptRow struct {
	keys
	PeriodAttrs
	billing.PaymentAttempt
	// LookupPK resolves the attempt from the wallet charge id a webhook arrives
	// with. Sparse: absent until wallet has returned a charge id.
	LookupPK string `dynamodbav:"lookup_pk,omitempty"`
	// ScheduleKeys put a PENDING attempt in front of the reconciliation job and
	// take it back out the moment it reaches a terminal status. The sweep's
	// partition is therefore the set of charges with no answer yet, not the
	// history of every charge ever opened.
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

// checkoutRow carries no schedule keys, and that is deliberate: session expiry
// is derived on read (billing.CheckoutSession.IsUsable), so there is no sweep to
// index for. A row that never needs finding by a job does not belong in the
// job index.
type checkoutRow struct {
	keys
	PeriodAttrs
	billing.CheckoutSession
}

type usageRow struct {
	keys
	billing.UsageRecord
}

type auditRow struct {
	keys
	PeriodAttrs
	billing.AuditLog
}

// generationRow is a marker written in the same transaction as the invoice it
// belongs to. Its only job is to make "has this period already been invoiced?"
// a GetItem instead of a query with a filter.
type generationRow struct {
	keys
	InvoiceID     string `dynamodbav:"invoice_id"`
	GenerationKey string `dynamodbav:"generation_key"`
}

type endpointRow struct {
	keys
	billing.WebhookEndpoint
}

// eventRow is the outbox entry, written inside the transaction of the change it
// describes. Its schedule keys put it in front of the fan-out pass and are
// removed the moment that pass has matched it to endpoints — so the partition is
// the set of events nobody has routed yet, not the history of every event.
type eventRow struct {
	keys
	billing.Event
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

// deliveryRow is one event's journey to one endpoint. Its schedule keys carry
// the *due time* rather than a due date, because a backoff is measured in
// minutes and a day-granular partition cannot express "retry at 00:05".
type deliveryRow struct {
	keys
	billing.Delivery
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

// scheduleKeys returns the sweep-index attributes for a due date, or empty
// strings when the row should not be swept.
func scheduleKeys(livemode bool, job string, due brcal.Date, id string, active bool) (pk, sk string) {
	if !active {
		return "", ""
	}
	return SchedulePK(livemode, job, due), id
}
