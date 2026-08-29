// Package repositories is the DynamoDB persistence layer.
//
// Rules, from ADR 0002 and ADR 0003:
//   - Every partition key **begins with** {organization_id}#{livemode}. A query
//     without a tenant is not expressible, so forgetting a tenant filter stops
//     being a class of bug.
//   - GetItem > Query > never Scan on a tenant read path. If an access pattern
//     only resolves with a Scan, an index is missing.
//   - Every write sets the TTL attribute (ADR 0009). In DynamoDB the TTL is
//     written at creation, so an item created without it keeps no retention
//     forever.
package repositories

import (
	"fmt"
	"strings"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Logical table names. The physical name is {prefix}_{table}, and the company
// standard makes the prefix `{env}_billing`, so the deployed tables are
// `prod_billing_invoices`, `prod_billing_subscriptions`, and so on — the same
// shape as ctech-dfe's `prod_dfe_nfes`.
//
// One table per entity, with **one exception that is the whole point**: an
// entity's children live in their parent's table, under their parent's
// partition. An invoice, its lines, its payment attempts and its checkout
// sessions are one aggregate — the invoice screen reads all of them, and they
// are written in one transaction — so they are one table, addressed by sort-key
// prefix. Subscription items are the same case. Splitting an aggregate across
// tables would buy nothing and cost the prefix Query that makes it one read.
//
// These names are the keys of schema.json, and TestEveryTableNameHasASchema
// pins that.
const (
	TableOrganizations = "organizations"
	TableCredentials   = "credentials"
	TableCustomers     = "customers"
	TableProducts      = "products"
	TablePrices        = "prices"
	TableSubscriptions = "subscriptions"
	// TableInvoices holds the invoice aggregate: the invoice, its lines, its
	// payment attempts, its checkout sessions, the per-year numbering counter it
	// is finalized against, and the generation marker that makes the daily sweep
	// re-runnable. All of them are written with the invoice, in the invoice's own
	// transaction.
	TableInvoices = "invoices"
	TableUsage    = "usage"
	TableAudit    = "audit"
	// TableWebhooks holds the outbound delivery machinery: the endpoints a tenant
	// registered, the events emitted for it, and one delivery row per
	// (event, endpoint). Three row types in one table because they are read
	// together — fanning an event out means reading the tenant's endpoints, and
	// retrying means reading a delivery beside the event it carries.
	TableWebhooks    = "webhooks"
	TableIdempotency = "idempotency"
)

// Index names.
//
// The same three names recur across tables that need them — `invoices` has all
// three, `products` has none. Reusing the name rather than qualifying it per
// table is what lets one shared query helper name an index without knowing
// which table it is reading.
const (
	// IndexPeriod serves tenant listings and the pre-declared period metrics:
	// partition period_pk = {org}#{mode}#{ENTITY}, sort period_sk =
	// year#month#day#seq, queried by prefix.
	IndexPeriod = "period-index"
	// IndexSchedule serves the system sweeps. Sparse: the key attributes exist
	// only while the item is actionable.
	IndexSchedule = "schedule-index"
	// IndexLookup serves reference lookups by an external identifier. Sparse.
	IndexLookup = "lookup-index"
)

// Entity discriminates rows in a period index. It is part of the partition
// attribute, so a period query always names exactly what it is counting.
//
// It survives the split into per-entity tables because the `invoices` table
// holds three indexed row types — the invoice, its attempts and its sessions —
// and "invoices issued in March" must not also count the attempts to pay them.
type Entity string

const (
	EntityInvoice        Entity = "INVOICE"
	EntitySubscription   Entity = "SUBSCRIPTION"
	EntityCustomer       Entity = "CUSTOMER"
	EntityPaymentAttempt Entity = "PAYMENT_ATTEMPT"
	EntityCheckout       Entity = "CHECKOUT"
	EntityCreditNote     Entity = "CREDIT_NOTE"
	EntityAudit          Entity = "AUDIT"
)

// Sort-key prefixes. One constant per row type, so the layout lives in this file
// and nowhere else.
//
// Most tables now hold one row type, where the prefix is redundant with the
// table name. It is kept anyway: it is what makes a raw row readable in the
// console without knowing which table it came from, and dropping it would mean
// a migration to add it back the first time a table gains a second row type.
const (
	skOrganization = "ORG"
	skCustomer     = "CUSTOMER#"
	skProduct      = "PRODUCT#"
	skPrice        = "PRICE#"
	skSubscription = "SUB#"
	skInvoice      = "INVOICE#"
	skCheckout     = "CHECKOUT#"
	skCreditNote   = "CREDIT#"
	skAudit        = "AUDIT#"
	skCounter      = "COUNTER#"
	skCustomerUser = "CUSTOMER_USER#"
	skCredential   = "CREDENTIAL#"
	skIdempotency  = "IDEMPOTENCY#"
	skEndpoint     = "ENDPOINT#"
	skEvent        = "EVENT#"
	skDelivery     = "DELIVERY#"
)

// Mode renders the livemode flag as it appears in a key.
func Mode(livemode bool) string {
	if livemode {
		return "live"
	}
	return "test"
}

// TenantPK is the partition every tenant row lives under.
func TenantPK(organizationID string, livemode bool) string {
	return organizationID + "#" + Mode(livemode)
}

// OrganizationSK is the sort key of the organization row itself.
func OrganizationSK() string { return skOrganization }

// CustomerSK, ProductSK and friends build a row's sort key.
func CustomerSK(customerID string) string         { return skCustomer + customerID }
func ProductSK(productID string) string           { return skProduct + productID }
func PriceSK(priceID string) string               { return skPrice + priceID }
func SubscriptionSK(subscriptionID string) string { return skSubscription + subscriptionID }
func InvoiceSK(invoiceID string) string           { return skInvoice + invoiceID }
func AuditSK(auditID string) string               { return skAudit + auditID }

// SubscriptionItemSK nests the item under its subscription, so reading a
// subscription and all its items is one Query with begins_with, not a second
// round trip per item.
func SubscriptionItemSK(subscriptionID, itemID string) string {
	return skSubscription + subscriptionID + "#ITEM#" + itemID
}

// PaymentAttemptSK nests the attempt under its invoice for the same reason: the
// invoice detail screen needs every attempt, and it is the only place they are
// ever read from.
func PaymentAttemptSK(invoiceID string, attemptNumber int) string {
	return fmt.Sprintf("%s%s#ATTEMPT#%04d", skInvoice, invoiceID, attemptNumber)
}

// CheckoutSK nests the session under its invoice too.
//
// A session is only ever reached from the invoice it pays — the checkout page
// asks "is there still a usable session for this invoice?", never "fetch session
// cs_x". Nesting makes that a prefix Query inside a partition the caller was
// already entitled to read, so a session needs no index and no addressable id of
// its own.
func CheckoutSK(invoiceID, sessionID string) string {
	return skInvoice + invoiceID + "#" + skCheckout + sessionID
}

// CreditNoteSK nests a credit note under the invoice it corrects.
//
// Same reason as the payment attempt: a credit note is only ever read as part of
// an invoice — "what has been credited against this?" — and never fetched by id
// on its own. Nesting answers that with one prefix Query inside a partition the
// caller already holds, and costs no index.
func CreditNoteSK(invoiceID, creditNoteID string) string {
	return skInvoice + invoiceID + "#" + skCreditNote + creditNoteID
}

// InvoiceItemSK nests a line under its invoice, ordered by position.
func InvoiceItemSK(invoiceID string, line int) string {
	return fmt.Sprintf("%s%s#LINE#%04d", skInvoice, invoiceID, line)
}

// InvoiceCounterSK is the per-organization, per-year sequential invoice number.
// Numbering is per year because that is how it is read on a Brazilian invoice,
// and because a single ever-growing counter makes the first invoice of a new
// year unreadable.
func InvoiceCounterSK(year int) string {
	return fmt.Sprintf("%sINVOICE#%04d", skCounter, year)
}

// UsagePK is the sub-partition for usage records.
//
// It still starts with the tenant prefix, so no cross-tenant read is
// expressible; the extra segments exist because usage is the one entity with
// unbounded write volume, and period close must read exactly one partition
// rather than sweeping the tenant's whole partition (ADR 0002).
func UsagePK(organizationID string, livemode bool, subscriptionItemID string, periodStart brcal.Date) string {
	return TenantPK(organizationID, livemode) + "#USAGE#" + subscriptionItemID + "#" + periodStart.String()
}

// Schedule-index job names. One constant per job that exists, and the list is
// short on purpose: a job name with no writer and no reader describes a sweep
// nobody runs, which reads to the next person as a feature that is merely
// broken.
const (
	// JobSubscriptionDue finds subscriptions whose next invoice is due.
	// Written by SubscriptionRepository, read by cmd/sweep.
	JobSubscriptionDue = "SUB_DUE"

	// JobChargeReconcile finds payment attempts still waiting on a wallet charge.
	// Written by PaymentRepository, read by cmd/reconcile.
	//
	// Deliberately not JobInvoiceSettlement, which answers a different question on
	// a different clock. "This bill is late" is a daily fact about a customer;
	// "this charge has no answer yet" is an hourly fact about an integration, and
	// the second one is what catches a notify-back that was never delivered. An
	// invoice paid at 15:00 whose webhook was lost must not wait for tomorrow's
	// dunning pass to be marked paid.
	JobChargeReconcile = "CHARGE_RECONCILE"

	// JobInvoiceSettlement finds open invoices past their due date. It is armed on
	// finalize and disarmed the moment the invoice stops being collectable, so the
	// partition is the list of bills nobody has paid.
	//
	// **Armed with no reader today.** It is dunning's input, and dunning is not
	// built (PLAN.md Phase 3). The rows are written now rather than backfilled
	// later because arming an invoice retroactively means finding the invoices
	// that should have been armed, which is the query the index exists to avoid.
	// Whoever builds dunning reads this partition and writes nothing new.
	JobInvoiceSettlement = "SETTLEMENT"

	// JobWebhookFanout finds events that have not been matched to endpoints yet.
	// Written inside the transaction of the change the event describes, read by
	// cmd/deliver.
	JobWebhookFanout = "WEBHOOK_FANOUT"

	// JobWebhookDelivery finds deliveries that are due — a first attempt, or one
	// whose backoff has elapsed. Also read by cmd/deliver.
	JobWebhookDelivery = "WEBHOOK_DELIVERY"
)

// SchedulePK is the partition a daily sweep reads.
//
// This is the **only** key in the system that does not start with a tenant, and
// that is deliberate: a sweep is inherently cross-tenant. It must never be
// reachable from a request-scoped path — the mode is in the key so that a test
// sweep can never touch live rows.
func SchedulePK(livemode bool, job string, due brcal.Date) string {
	return Mode(livemode) + "#" + job + "#" + due.String()
}

// EndpointSK, EventSK and DeliverySK address the three webhook row types.
//
// A delivery nests under its event, so "everything queued for this event" is a
// prefix Query, and the pair (event, endpoint) is unique by construction rather
// than by a condition somebody has to remember to write.
func EndpointSK(endpointID string) string { return skEndpoint + endpointID }
func EventSK(eventID string) string       { return skEvent + eventID }
func DeliverySK(eventID, endpointID string) string {
	return skEvent + eventID + "#" + skDelivery + endpointID
}

// WebhookQueuePK is the partition the delivery job reads.
//
// Unlike every other schedule partition it carries **no date**. The others
// answer "what is due today", where today is a civil fact about billing; this
// one answers "what is due now", where now moves in seconds and a retry
// scheduled at 23:59 for 00:05 would otherwise land in a partition the job has
// already passed. One partition per mode per job, with the due time as the sort
// key, makes "what is due" the natural order of the partition — the job reads
// ascending and stops at the first row that is not due yet.
//
// It is sparse and small by construction: a row enters when work is outstanding
// and leaves the moment it is done, so the partition is the backlog rather than
// the history. That is also what keeps a single partition from being a hot one.
func WebhookQueuePK(livemode bool, job string) string {
	return Mode(livemode) + "#" + job
}

// WebhookQueueSK orders the queue by when the work is due, ties broken by id so
// two rows due in the same millisecond are still distinct keys.
func WebhookQueueSK(due time.Time, rowID string) string {
	return due.UTC().Format(time.RFC3339Nano) + "#" + rowID
}

// Lookup-index key builders.

// LookupWalletChargePK finds the payment attempt a wallet webhook refers to.
// It is not tenant-scoped because the webhook arrives from wallet knowing only
// the charge id; the row it finds carries the tenant, which every subsequent
// read uses.
func LookupWalletChargePK(livemode bool, walletChargeID string) string {
	return Mode(livemode) + "#CHARGE#" + walletChargeID
}

// LookupCustomerRefPK finds a customer by the caller's own identifier. Tenant
// scoped, because two organizations may legitimately use the same external ref.
func LookupCustomerRefPK(organizationID string, livemode bool, externalRef string) string {
	return TenantPK(organizationID, livemode) + "#CUSTOMER_REF#" + externalRef
}

// LookupOrganizationOwnerPK resolves a console user to the organization they
// own, which is the one read that must happen before the tenant is known
// (ADR 0011). It is the user-session counterpart of LookupCredentialPK: an
// integration arrives with a client id, a person arrives with a subject.
//
// The mode is in the key rather than filtered afterwards because the live and
// test organizations are two rows with the same owner, and a lookup that returns
// both and then picks one is a lookup that can pick wrong.
func LookupOrganizationOwnerPK(livemode bool, userID string) string {
	return Mode(livemode) + "#OWNER#" + userID
}

// CustomerUserSK addresses the pointer row that maps a signed-in person to their
// customer record inside one organization (ADR 0012).
//
// It is a sort key rather than an index entry because the portal already knows
// which organization it serves — tenant zero, from configuration — so the
// question is "who is this user *here*", and that is a GetItem inside a partition
// the caller was always entitled to read. An index would be machinery for a
// question nobody asks.
func CustomerUserSK(userID string) string { return skCustomerUser + userID }

// CredentialSK and IdempotencySK are tenant-scoped rows.
func CredentialSK(clientID string) string { return skCredential + clientID }
func IdempotencySK(key string) string     { return skIdempotency + key }

// LookupCredentialPK resolves an OAuth client id to its tenant.
//
// It is not tenant-scoped, and it cannot be: the tenant is the answer to this
// lookup, so it cannot also be part of the question. It is the one read in the
// request path that precedes knowing the tenant — everything after it is scoped
// by what this returns, never by anything the caller sent.
func LookupCredentialPK(clientID string) string { return "CLIENT#" + clientID }

// LookupInvoiceGenerationPK is the idempotency key of invoice generation
// ({subscription_item_id}:{period_start}), made addressable so the scheduler can
// check "did I already create this invoice?" with a GetItem rather than a scan.
func LookupInvoiceGenerationPK(organizationID string, livemode bool, generationKey string) string {
	return TenantPK(organizationID, livemode) + "#GEN#" + generationKey
}

// LookupSubscriptionInvoicesPK partitions a subscription's invoices so that
// "the last N invoices of this subscription" is a Query rather than a tenant
// listing filtered down to one subscription_id.
//
// It rides the lookup index instead of getting an index of its own. The sort
// key there is the table's own `sk` — "INVOICE#" plus the invoice's ULID — so
// the ordering the subscription screen wants is already the ordering the index
// has, newest last, and reading it backwards costs nothing. A fourth GSI would
// have bought the same query for a backfill on a live table and a second copy
// of every invoice row.
//
// Sparse, like every other lookup partition: a one-off invoice has no
// subscription and writes no attribute, so it never enters the index.
func LookupSubscriptionInvoicesPK(organizationID string, livemode bool, subscriptionID string) string {
	return TenantPK(organizationID, livemode) + "#SUBINV#" + subscriptionID
}

// PeriodPK is the period index's partition: tenant plus entity type, so a
// period query always names exactly what it is counting.
//
// It is one derived attribute rather than a two-attribute composite partition
// key (which is what ctech-dfe's index uses). The reason is reuse: the shared
// helper `api-commons dynamo.QueryComposite` expresses a single partition
// attribute plus an ordered sort key, which covers every access pattern billing
// declared. Using a two-attribute partition would mean hand-rolling the
// KeyConditionExpression here — the duplication the cross-stack audit warns
// about — for no query billing needs.
func PeriodPK(organizationID string, livemode bool, entity Entity) string {
	return TenantPK(organizationID, livemode) + "#" + string(entity)
}

// PeriodAttrs are the period-index key attributes.
//
// The sort key is **one concatenated, zero-padded string** —
// "2026#03#05#in_01J..." — not four separate key attributes.
//
// Both express the same thing, because a multi-attribute key can only ever be
// constrained left to right anyway: "everything in 2026" is begins_with
// "2026#", "everything in March 2026" is begins_with "2026#03#".
//
// This is a choice, not a constraint. The multi-attribute form is expressible in
// Terraform — provider 6.x replaced the GSI's hash_key/range_key arguments with
// repeatable key_schema blocks, an unbounded list that exists for exactly that.
// The concatenated form is kept because it is one attribute rather than four and
// needs only the simplest shared query helper (a prefix Query) instead of the
// composite-key builder.
//
// ctech-dfe's equivalent index does use multi-attribute keys. Both are valid;
// this one is smaller where it is read.
//
// Padding is what makes it sort: "03" before "12"; "3" would sort after. Nothing
// else may build this string by hand — this is the only constructor, and queries
// render their prefixes through PeriodPrefix so a read can never pad differently
// from a write.
type PeriodAttrs struct {
	PeriodPK string `dynamodbav:"period_pk"`
	PeriodSK string `dynamodbav:"period_sk"`
}

// NewPeriodAttrs builds the index attributes for a row.
//
// The date must be the civil date in America/Sao_Paulo (brcal guarantees this).
// A UTC-derived date misfiles every row created between 21:00 and midnight local
// time into the following day, and every month boundary into the wrong month.
//
// seq is the row's own ULID-based id: it breaks ties within a day and gives
// stable pagination, and it is already time-ordered.
func NewPeriodAttrs(organizationID string, livemode bool, entity Entity, d brcal.Date, seq string) PeriodAttrs {
	return PeriodAttrs{
		PeriodPK: PeriodPK(organizationID, livemode, entity),
		PeriodSK: fmt.Sprintf("%04d#%02d#%02d#%s", d.Year, int(d.Month), d.Day, seq),
	}
}

// PeriodPrefix renders the sort-key prefix for a query, using the same padding
// as NewPeriodAttrs.
//
// Pass month = 0 for a whole year, day = 0 for a whole month. Anything else
// would be a second place that decides how a date is rendered, and two such
// places always disagree eventually.
func PeriodPrefix(year, month, day int) string {
	switch {
	case month <= 0:
		return fmt.Sprintf("%04d#", year)
	case day <= 0:
		return fmt.Sprintf("%04d#%02d#", year, month)
	default:
		return fmt.Sprintf("%04d#%02d#%02d#", year, month, day)
	}
}

// TenantOf splits a partition key back into its organization and mode. It exists
// for the webhook path, which finds a row by charge id and then needs the tenant
// that row belongs to.
func TenantOf(pk string) (organizationID string, livemode bool, ok bool) {
	org, mode, found := strings.Cut(pk, "#")
	if !found || org == "" {
		return "", false, false
	}
	// A sub-partitioned key (usage) has more segments; the first two are still
	// organization and mode.
	mode, _, _ = strings.Cut(mode, "#")
	switch mode {
	case "live":
		return org, true, true
	case "test":
		return org, false, true
	default:
		return "", false, false
	}
}
