package billing

import (
	"errors"
	"fmt"
	"slices"
)

// State transitions, for every entity, go through one function per entity
// (assessment § 6): it validates the move, names the cause, and returns the
// events to emit. There is no `status = X` anywhere else in the codebase.
//
// That rule is the difference between a billing system whose history can be
// explained and one where an invoice is PAID and nobody can say why.

// EventType is the name of a domain event. The pure domain returns event *types*;
// turning them into stored Event records — with an id, a version, a timestamp and
// a payload — is the persistence layer's job, because those need a clock and an
// id source this package deliberately does not have.
type EventType string

// Cause is why a transition happened. It is not decoration: several edges are
// legal only for some causes, and that is the only thing standing between "the
// bank confirmed this payment" and "an operator clicked a button".
type Cause string

const (
	// CauseScheduler is the daily invoice-generation/renewal sweep.
	CauseScheduler Cause = "scheduler"
	// CauseManual is an operator action in the console that changes state
	// directly. It is never sufficient to mark money as received.
	CauseManual Cause = "manual"
	// CauseManualPayment is an operator recording a receipt that happened outside
	// the system (a TED, a settlement). It requires its own permission and creates
	// a PaymentAttempt of method "manual" naming who recorded it — it must never
	// disguise itself as an automatic payment (assessment § 6.4).
	CauseManualPayment Cause = "manual_payment"
	// CauseWalletWebhook is a signed notification from ctech-wallet.
	CauseWalletWebhook Cause = "wallet_webhook"
	// CauseReconciliation is the job that polls wallet for charges whose webhook
	// never arrived. Webhooks are never the only signal (ARCHITECTURE.md § 3).
	CauseReconciliation Cause = "reconciliation"
	// CausePaymentFailed is a failed collection attempt.
	CausePaymentFailed Cause = "payment_failed"
	// CauseDunningExhausted is the retry policy giving up.
	CauseDunningExhausted Cause = "dunning_exhausted"
	// CauseTrialEnded is the end of a trial period.
	CauseTrialEnded Cause = "trial_ended"
	// CauseInvoicePaid is an invoice reaching PAID, propagating to a subscription.
	CauseInvoicePaid Cause = "invoice_paid"
	// CauseActivationExpired is an INCOMPLETE subscription whose first payment
	// never arrived within the activation window.
	CauseActivationExpired Cause = "activation_expired"
	// CauseRenewal is a subscription rolling into its next period.
	CauseRenewal Cause = "renewal"
	// CauseScheduleCancel is cancel_at_period_end being set or cleared.
	CauseScheduleCancel Cause = "schedule_cancel"
	// CauseExpired is a TTL elapsing (a checkout session).
	CauseExpired Cause = "expired"
	// CauseCustomer is an action taken by the consumer in the portal.
	CauseCustomer Cause = "customer"
	// CauseNothingDue closes an invoice whose total is zero — a free plan's
	// period, or one entirely covered by a discount.
	//
	// It is its own cause and not a member of paymentCauses precisely because no
	// money moved. An accountant reading the audit trail must be able to tell the
	// two apart, and the state machine must be able to refuse this one on an
	// invoice that actually owes something (see Invoice.Transition).
	CauseNothingDue Cause = "nothing_due"
)

var (
	// ErrInvalidTransition reports a move the state machine does not have an edge
	// for — including any move out of a terminal state.
	ErrInvalidTransition = errors.New("invalid state transition")
	// ErrCauseNotAllowed reports a legal edge attempted for a cause that may not
	// take it. This is the error behind "an operator cannot mark an
	// UNCOLLECTIBLE invoice as paid by hand".
	ErrCauseNotAllowed = errors.New("transition not allowed for this cause")
)

// rule is one legal edge: the event it emits and, optionally, the exhaustive set
// of causes permitted to take it. An empty causes slice means any cause.
type rule struct {
	event  EventType
	causes []Cause
}

type edge[S ~string] struct{ from, to S }

// apply looks up (from, to) in the table and checks the cause. Returning the
// event rather than mutating anything keeps the table itself pure and lets each
// entity decide what else the transition changes.
func apply[S ~string](table map[edge[S]][]rule, from, to S, cause Cause) (EventType, error) {
	rules, ok := table[edge[S]{from, to}]
	if !ok {
		return "", fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	for _, r := range rules {
		if len(r.causes) == 0 || slices.Contains(r.causes, cause) {
			return r.event, nil
		}
	}
	return "", fmt.Errorf("%w: %s -> %s by %q", ErrCauseNotAllowed, from, to, cause)
}
