package billing

import "time"

// PayoutState is where a payout stands (not to be confused with Organization.PayoutStatus, the charge gate). Only the bank's answer moves it past
// processing: a transport error, a timeout or a status nobody recognises leaves
// it processing, never failed.
type PayoutState string

const (
	PayoutProcessing PayoutState = "processing"
	PayoutDone       PayoutState = "done"
	PayoutFailed     PayoutState = "failed"
)

// The events a payout emits when the bank has answered. Both name the payout; the
// consumer (the wallet) reads it for the outcome.
const (
	EventPayoutDone   EventType = "payout.done"
	EventPayoutFailed EventType = "payout.failed"
)

// Failure reasons a payout can carry.
const (
	PayoutKeyNotFound = "key_not_found" // the bank has no Pix key for that CPF
	PayoutRefused     = "refused"       // the bank refused the request; no money moved
)

// Payout is money leaving the tenant's bank account to a person's Pix key (CPF
// only): CTech's own tenant paying a user's withdrawal. It is reserved BEFORE
// the bank is called, so a retry or a crash can never pay twice.
type Payout struct {
	ID             string `dynamodbav:"id"              json:"id"`
	OrganizationID string `dynamodbav:"organization_id" json:"-"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`

	// OwnerKey routes the payout's events to the service that asked for it.
	OwnerKey string `dynamodbav:"owner_key,omitempty" json:"-"`

	// Reference is the caller's own name for the payout (the wallet's withdrawal
	// id): unique per tenant, the same reference with the same CPF and amount is
	// the same payout. RequestHash is what "the same" was.
	Reference   string `dynamodbav:"reference"    json:"reference"`
	RequestHash string `dynamodbav:"request_hash" json:"-"`

	// Ref is the UUID sent to the bank as its idempotency id; ProviderRef is the
	// bank's own id for the payment (what a lookup uses), empty until the bank
	// has taken the request; PaymentID is the end-to-end id once there is one.
	Ref         string `dynamodbav:"ref"                    json:"-"`
	ProviderRef string `dynamodbav:"provider_ref,omitempty" json:"-"`
	PaymentID   string `dynamodbav:"payment_id,omitempty"   json:"payment_id,omitempty"`

	Amount  Cents  `dynamodbav:"amount"            json:"amount"`
	CPF     string `dynamodbav:"cpf"               json:"-"`
	Message string `dynamodbav:"message,omitempty" json:"message,omitempty"`

	Status        PayoutState `dynamodbav:"status"                   json:"status"`
	FailureReason string      `dynamodbav:"failure_reason,omitempty" json:"failure_reason,omitempty"`

	CreatedAt time.Time `dynamodbav:"opened_at" json:"created_at"`
	UpdatedAt time.Time `dynamodbav:"updated_at_p" json:"updated_at"`
}

// Final reports a payout the bank has answered for.
func (p *Payout) Final() bool { return p.Status == PayoutDone || p.Status == PayoutFailed }
