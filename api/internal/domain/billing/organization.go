package billing

import (
	"errors"
	"fmt"
)

// PayoutStatus is how far an organization has got in being able to receive money
// (ADR 0005).
type PayoutStatus string

const (
	// PayoutNotConfigured is the default. Nothing has been set up.
	PayoutNotConfigured PayoutStatus = "not_configured"
	// PayoutPendingCustody means onboarding is done on billing's side and the
	// organization is waiting on wallet-side custody, which is off by default
	// (ctech-wallet AsaasCustodyEnabled).
	PayoutPendingCustody PayoutStatus = "pending_custody"
	// PayoutEnabled means this organization may open charges.
	PayoutEnabled PayoutStatus = "enabled"
)

// ErrPayoutNotEnabled reports an organization that may not open charges yet. The
// HTTP layer maps it to 409 with the reason, never to a hidden button.
var ErrPayoutNotEnabled = errors.New("organization cannot open charges")

// Organization is the tenant. It is deliberately minimal (ADR 0007): an id, a
// display name, one owner, the mode, and the payout gate.
//
// **No CNPJ, no legal name, no address, no certificate.** The moment billing
// stores a company registry, the boundary with ctech-dfe's fiscal registry blurs
// and there are two sources of truth about the same company — and the second one
// is always the stale one. If a "just one little CNPJ field" request appears,
// that is the start of the duplication, and the answer is no.
//
// It is also not a second RBAC model: no roles, no permissions, no invitations.
// It is designed to be replaced by a reference to ctech-account, not to grow. If
// it starts acquiring configurable roles, it has quietly become the copy of
// ctech-dfe's model that _analysis/cross-stack-duplication.md forbids.
type Organization struct {
	ID           string       `dynamodbav:"id"            json:"id"`
	DisplayName  string       `dynamodbav:"display_name"  json:"display_name"`
	Livemode     bool         `dynamodbav:"livemode"      json:"livemode"`
	PayoutStatus PayoutStatus `dynamodbav:"payout_status" json:"payout_status"`
	// OwnerUserID references ctech-account's user. Billing stores the reference,
	// never a copy of the person.
	OwnerUserID string `dynamodbav:"owner_user_id" json:"owner_user_id"`

	// DunningPolicy is this organization's default schedule for chasing an
	// unpaid invoice. Empty means the built-in one, which is the state every
	// organization starts in — a merchant who has not thought about dunning gets
	// a policy that works rather than none.
	//
	// A product may override it (Product.DunningPolicy). Neither is read while
	// an invoice is being chased: the schedule is copied onto the invoice when
	// it is finalized, so editing this changes what happens to invoices issued
	// afterwards and nothing about the ones already in flight.
	DunningPolicy DunningSchedule `dynamodbav:"dunning_policy,omitempty" json:"dunning_policy,omitempty"`
}

// AuthorizeCharge is the **single** gate deciding whether an organization may
// open a charge. Every path that collects money calls this one function.
//
// Keeping it in one place is the whole design: a gate spread across handlers is
// a gate with a hole in it, and the hole is found by a merchant, not by a test.
// The block is server-side authorization — hiding a button in the console is not
// blocking anything.
func (o *Organization) AuthorizeCharge() error {
	if o.PayoutStatus == PayoutEnabled {
		return nil
	}
	return fmt.Errorf("%w: payout_status is %q", ErrPayoutNotEnabled, o.PayoutStatus)
}
