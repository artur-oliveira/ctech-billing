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
	PrefixApplication     = "apl_"
	PrefixWebhookEndpoint = "whe_"
)

// New returns a bare ULID string. Uses crypto/rand entropy; safe for concurrent
// use.
func New() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// NewAt returns a bare ULID whose timestamp is t's. The realtime change log uses
// it so an id's embedded time is the transaction's own clock (and a test's
// pinned one), which is what the replay window is computed from.
func NewAt(t time.Time) string {
	return ulid.MustNew(ulid.Timestamp(t), rand.Reader).String()
}

// Time is the instant a ULID was created, to the millisecond. It reports false
// for anything that is not a ULID.
func Time(s string) (time.Time, bool) {
	u, err := ulid.ParseStrict(s)
	if err != nil {
		return time.Time{}, false
	}
	return ulid.Time(u.Time()), true
}

// FloorAt is the smallest ULID of t's millisecond: every id created at or after
// t sorts after it, which makes it a lower bound for a range query.
func FloorAt(t time.Time) string {
	var u ulid.ULID
	if err := u.SetTime(ulid.Timestamp(t)); err != nil {
		return ""
	}
	return u.String()
}

// NewWithPrefix returns a prefixed id, e.g. NewWithPrefix(PrefixInvoice).
func NewWithPrefix(prefix string) string { return prefix + New() }
