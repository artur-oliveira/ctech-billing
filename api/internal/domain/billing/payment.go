package billing

import (
	"strconv"
	"time"
)

// PaymentAttemptStatus is one attempt at collecting an invoice. An invoice may
// have many; folding the attempt into the invoice is the modelling error that
// makes dunning and diagnosis impossible (assessment § 5.2).
type PaymentAttemptStatus string

const (
	AttemptPending   PaymentAttemptStatus = "PENDING"
	AttemptSucceeded PaymentAttemptStatus = "SUCCEEDED"
	AttemptFailed    PaymentAttemptStatus = "FAILED"
	// AttemptAbandoned means reconciliation could not find the charge in wallet
	// after the settlement window. It is a sign of an integration bug, not of a
	// customer who did not pay, and it must raise an alarm.
	AttemptAbandoned PaymentAttemptStatus = "ABANDONED"
)

// PaymentAttempt events.
const (
	EventPaymentAttempted EventType = "payment.attempted"
	EventPaymentSucceeded EventType = "payment.succeeded"
	EventPaymentFailed    EventType = "payment.failed"
	EventPaymentAbandoned EventType = "payment.abandoned"
)

// PaymentMethod is how an attempt tries to collect.
type PaymentMethod string

const (
	// MethodPIX opens a PIX charge in wallet. The MVP rail (ADR 0004).
	MethodPIX PaymentMethod = "pix"
	// MethodWalletBalance debits an existing wallet balance. Tried first as an
	// optimization when the customer has one; never the only route, because it
	// fails for every new customer.
	MethodWalletBalance PaymentMethod = "wallet_balance"
	// MethodManual records a receipt that happened outside the system. It exists
	// because it will happen on day one, and without it the operator either lies
	// in the system or leaves an eternal open invoice (assessment § 13).
	MethodManual PaymentMethod = "manual"
)

var paymentAttemptTransitions = map[edge[PaymentAttemptStatus]][]rule{
	{AttemptPending, AttemptSucceeded}: {{
		event:  EventPaymentSucceeded,
		causes: paymentCauses,
	}},
	// FAILED is reachable from reconciliation as well as from the rail. The two
	// are the same finding by different routes: wallet says the charge expired
	// unpaid, and whether that arrived as a notify-back or was discovered by
	// polling does not change what happened to the customer's money.
	{AttemptPending, AttemptFailed}: {{
		event:  EventPaymentFailed,
		causes: []Cause{CausePaymentFailed, CauseWalletWebhook, CauseReconciliation},
	}},
	// ABANDONED is reconciliation's alone, and it means something narrower than
	// FAILED: wallet does not know this charge at all. Nobody declined to pay —
	// billing recorded a charge id that wallet cannot account for, which is an
	// integration fault and must never be reported as a customer who did not pay.
	{AttemptPending, AttemptAbandoned}: {{
		event:  EventPaymentAbandoned,
		causes: []Cause{CauseReconciliation},
	}},
}

// PaymentAttempt records one collection attempt against an invoice.
//
// SUCCEEDED is terminal and is only reachable with a charge confirmed at the
// source — never by a UI action. Recording an out-of-band receipt is a different
// thing: an attempt with MethodManual and RecordedBy naming who did it. It needs
// its own permission and must never disguise itself as an automatic payment.
type PaymentAttempt struct {
	ID             string               `dynamodbav:"id"              json:"id"`
	OrganizationID string               `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool                 `dynamodbav:"livemode"        json:"livemode"`
	InvoiceID      string               `dynamodbav:"invoice_id"      json:"invoice_id"`
	AttemptNumber  int                  `dynamodbav:"attempt_number"  json:"attempt_number"`
	Status         PaymentAttemptStatus `dynamodbav:"status"          json:"status"`
	Method         PaymentMethod        `dynamodbav:"method"          json:"method"`
	Amount         Cents                `dynamodbav:"amount"          json:"amount"`

	// WalletChargeID is the charge id returned by ctech-wallet. It is the only
	// evidence that makes SUCCEEDED legitimate, and it is the key the wallet
	// webhook arrives with.
	WalletChargeID string `dynamodbav:"wallet_charge_id,omitempty" json:"wallet_charge_id,omitempty"`
	FailureReason  string `dynamodbav:"failure_reason,omitempty"   json:"failure_reason,omitempty"`
	// RecordedBy is the operator who recorded a MethodManual receipt. Empty for
	// every automatic attempt.
	RecordedBy string `dynamodbav:"recorded_by,omitempty" json:"recorded_by,omitempty"`
}

// IdempotencyKey is the key sent to wallet when opening this attempt's charge.
//
// It includes the attempt number so a genuine retry after a failure opens a new
// charge, while a retried HTTP request for the *same* attempt is deduplicated by
// wallet. Keying on the invoice alone would make the second dunning attempt a
// silent no-op returning the first attempt's failure.
func (a *PaymentAttempt) IdempotencyKey() string {
	return a.InvoiceID + ":" + strconv.Itoa(a.AttemptNumber)
}

// Transition moves the attempt to `to`, returning the events to emit.
func (a *PaymentAttempt) Transition(to PaymentAttemptStatus, cause Cause) ([]EventType, error) {
	event, err := apply(paymentAttemptTransitions, a.Status, to, cause)
	if err != nil {
		return nil, err
	}
	a.Status = to
	return []EventType{event}, nil
}

// CheckoutSessionStatus is the lifecycle of a hosted payment page.
type CheckoutSessionStatus string

const (
	CheckoutOpen      CheckoutSessionStatus = "OPEN"
	CheckoutCompleted CheckoutSessionStatus = "COMPLETED"
	CheckoutExpired   CheckoutSessionStatus = "EXPIRED"
	CheckoutCanceled  CheckoutSessionStatus = "CANCELED"
)

// CheckoutSession events.
const (
	EventCheckoutCreated   EventType = "checkout.session.created"
	EventCheckoutCompleted EventType = "checkout.session.completed"
	EventCheckoutExpired   EventType = "checkout.session.expired"
	EventCheckoutCanceled  EventType = "checkout.session.canceled"
)

// DefaultCheckoutTTL is how long a PIX checkout session stays open. Long enough
// for a customer to switch to their bank app and back, short enough that an
// abandoned QR code stops being scannable.
const DefaultCheckoutTTL = 30 * time.Minute

var checkoutTransitions = map[edge[CheckoutSessionStatus]][]rule{
	{CheckoutOpen, CheckoutCompleted}: {{
		event:  EventCheckoutCompleted,
		causes: paymentCauses,
	}},
	{CheckoutOpen, CheckoutExpired}: {{
		event:  EventCheckoutExpired,
		causes: []Cause{CauseExpired, CauseScheduler},
	}},
	{CheckoutOpen, CheckoutCanceled}: {{
		event:  EventCheckoutCanceled,
		causes: []Cause{CauseManual, CauseCustomer},
	}},
}

// CheckoutSession is the stateful hosted page that turns "pay this invoice" into
// a resource with a QR code, a copy-paste string and an expiry — rather than a
// magic link with no lifecycle.
//
// Critical invariant: **the session is not the source of truth for the payment —
// the invoice is.** An expiring session must never undo an invoice that is
// already paid. That race is real, not theoretical: the PIX confirmation can
// land in the same second the TTL elapses.
type CheckoutSession struct {
	ID             string                `dynamodbav:"id"              json:"id"`
	OrganizationID string                `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool                  `dynamodbav:"livemode"        json:"livemode"`
	InvoiceID      string                `dynamodbav:"invoice_id"      json:"invoice_id"`
	Status         CheckoutSessionStatus `dynamodbav:"status"          json:"status"`
	ExpiresAt      time.Time             `dynamodbav:"expires_at"      json:"expires_at"`
	SuccessURL     string                `dynamodbav:"success_url,omitempty" json:"success_url,omitempty"`
	CancelURL      string                `dynamodbav:"cancel_url,omitempty"  json:"cancel_url,omitempty"`

	// PaymentAttemptID is the attempt this page is showing. One session, one
	// attempt: a retry after a failure is a different charge with a different QR
	// code, so it is a different session rather than an update to this one.
	PaymentAttemptID string `dynamodbav:"payment_attempt_id,omitempty" json:"payment_attempt_id,omitempty"`
	// PixCode is the copy-and-paste EMV string the rail returned.
	//
	// It is stored because wallet returns it once, when the charge is opened, and
	// does not keep it — so without this a customer who reloads the page loses
	// the only thing on it. The QR **image** is deliberately not stored: the
	// image is a rendering of exactly this string, and a few kilobytes of base64
	// per session buys nothing a renderer on the page does not already do.
	PixCode string `dynamodbav:"pix_code,omitempty" json:"pix_code,omitempty"`

	Metadata Metadata `dynamodbav:"metadata,omitempty" json:"metadata,omitempty"`
}

// IsUsable reports whether this session can still be shown as a way to pay.
//
// Expiry is **derived here, on read**, and never written by a sweep. That is the
// invariant above made operational: a job that writes EXPIRED races the PIX
// confirmation, and losing that race marks a paid invoice's session dead. Nobody
// needs the row to say EXPIRED — they need the page to stop offering a QR code
// that the rail will refuse.
func (c *CheckoutSession) IsUsable(now time.Time) bool {
	return c.Status == CheckoutOpen && now.Before(c.ExpiresAt) && c.PixCode != ""
}

// Transition moves the session to `to`, returning the events to emit.
func (c *CheckoutSession) Transition(to CheckoutSessionStatus, cause Cause) ([]EventType, error) {
	event, err := apply(checkoutTransitions, c.Status, to, cause)
	if err != nil {
		return nil, err
	}
	c.Status = to
	return []EventType{event}, nil
}
