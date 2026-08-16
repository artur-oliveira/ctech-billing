// Package id generates the identifiers billing stores.
//
// ULIDs, not UUIDs: they are lexicographically sortable by creation time, which
// is what lets an audit trail and an event log use the id itself as a
// time-ordered sort key with no second timestamp field to keep consistent.
package id

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// Prefixes make an id self-describing in a log line, a support ticket and a
// webhook payload. Reading "in_01J..." tells you what it is without a lookup;
// reading a bare ULID does not.
const (
	PrefixOrganization    = "org_"
	PrefixCustomer        = "cus_"
	PrefixProduct         = "prod_"
	PrefixPrice           = "price_"
	PrefixSubscription    = "sub_"
	PrefixSubscriptionItm = "si_"
	PrefixInvoice         = "in_"
	PrefixCreditNote      = "cn_"
	PrefixPaymentAttempt  = "pa_"
	PrefixCheckoutSession = "cs_"
	PrefixUsageRecord     = "ur_"
	PrefixEvent           = "evt_"
	PrefixAuditLog        = "log_"
)

// New returns a bare ULID string. Uses crypto/rand entropy; safe for concurrent
// use.
func New() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// NewWithPrefix returns a prefixed id, e.g. NewWithPrefix(PrefixInvoice).
func NewWithPrefix(prefix string) string { return prefix + New() }
