package billing

import (
	"errors"
	"fmt"
)

// PayoutRail is how a tenant collects (spec 2026-10-11 § 7.1): the wallet
// rail of ADR 0004, or BYOK, the organization's own bank account.
type PayoutRail string

const (
	RailWallet PayoutRail = "wallet"
	RailBYOK   PayoutRail = "byok"
)

var (
	ErrByokNotConnected       = errors.New("byok: no active connection for the collecting provider")
	ErrByokCertificateExpired = errors.New("byok: the connection's certificate expired")
	ErrByokScopesMissing      = errors.New("byok: the connection lacks the collection scopes")
	ErrByokQuotaExceeded      = errors.New("byok: the plan's charge quota is used up")

	// Plan limits (BYOK part C, spec § 12.5), returned inside a *LimitError.
	ErrSubscriptionLimitReached   = errors.New("billing: the plan's active subscriptions are at the quota")
	ErrBankConnectionLimitReached = errors.New("byok: the plan's bank connections are at the quota")
)

// LimitError is a plan limit reached, with the quota the console shows.
type LimitError struct {
	Err   error
	Quota int64
}

func (e *LimitError) Error() string { return fmt.Sprintf("%v (quota %d)", e.Err, e.Quota) }
func (e *LimitError) Unwrap() error { return e.Err }

// ConnectionFacts is what the gate needs from the BYOK connection.
type ConnectionFacts struct{ Found, Active, Expired, Collection bool }

// ChargeGate is every fact AuthorizeCharge decides on, built in one place
// (services.ChargeGates.For) so no collection path can leave one out. There is
// no legal-clearance fact: the gate was withdrawn (spec amendment 2026-10-11).
type ChargeGate struct {
	Connection    ConnectionFacts
	Entitled      bool // live: an entitling prod_billing subscription (R4)
	QuotaExceeded bool // live: plan with overage "refuse" past quota_charges_month (part C)
}

// Rail reads "" as the wallet, so every tenant from before BYOK is unchanged.
func (o *Organization) Rail() PayoutRail {
	if o.PayoutRail == "" {
		return RailWallet
	}
	return o.PayoutRail
}

// AuthorizeCharge is the **single** gate deciding whether an organization may
// open a charge. Every path that collects money calls this one function.
//
// Keeping it in one place is the whole design: a gate spread across handlers is
// a gate with a hole in it, and the hole is found by a merchant, not by a test.
// The block is server-side authorization — hiding a button in the console is not
// blocking anything. The wallet rail ignores g.
func (o *Organization) AuthorizeCharge(g ChargeGate) error {
	if o.PayoutStatus != PayoutEnabled {
		return fmt.Errorf("%w: payout_status is %q", ErrPayoutNotEnabled, o.PayoutStatus)
	}
	if o.Rail() == RailWallet {
		return nil
	}
	switch {
	case !g.Connection.Found:
		return ErrByokNotConnected
	case g.Connection.Expired:
		return ErrByokCertificateExpired
	case !g.Connection.Active:
		return ErrByokNotConnected
	case !g.Connection.Collection:
		return ErrByokScopesMissing
	}
	if !o.Livemode {
		return nil // test mode reaches the sandbox only (spec § 9)
	}
	switch {
	case o.Suspended():
		return ErrTenantSuspended
	case !g.Entitled:
		return fmt.Errorf("%w: no entitling prod_billing subscription", ErrPayoutNotEnabled)
	case g.QuotaExceeded:
		return ErrByokQuotaExceeded
	}
	return nil
}
