package billing

import "gopkg.aoctech.app/billing/api/internal/domain/brcal"

// SubscriptionStatus is the lifecycle of a recurring agreement.
//
// Two deliberate differences from the original spec (assessment § 6.2):
//
//   - INCOMPLETE exists. It is a subscription whose *first* payment never
//     happened. That is not PAST_DUE: the service was never granted, so there is
//     nothing to revoke and expiry is silent. Without it, the UI claims an
//     entitlement on day one that the customer does not have.
//   - EXPIRED does not exist. It is CANCELED with a cancel_reason. Two terminal
//     states that behave identically only fragment every query and report.
type SubscriptionStatus string

const (
	SubscriptionIncomplete SubscriptionStatus = "INCOMPLETE"
	SubscriptionTrialing   SubscriptionStatus = "TRIALING"
	SubscriptionActive     SubscriptionStatus = "ACTIVE"
	SubscriptionPastDue    SubscriptionStatus = "PAST_DUE"
	SubscriptionPaused     SubscriptionStatus = "PAUSED"
	SubscriptionCanceled   SubscriptionStatus = "CANCELED"
)

// Subscription events.
const (
	EventSubscriptionCreated   EventType = "subscription.created"
	EventSubscriptionActivated EventType = "subscription.activated"
	EventSubscriptionPastDue   EventType = "subscription.past_due"
	EventSubscriptionRecovered EventType = "subscription.recovered"
	EventSubscriptionRenewed   EventType = "subscription.renewed"
	EventSubscriptionPaused    EventType = "subscription.paused"
	EventSubscriptionResumed   EventType = "subscription.resumed"
	EventSubscriptionCanceled  EventType = "subscription.canceled"
	EventSubscriptionUpdated   EventType = "subscription.updated"
)

var subscriptionTransitions = map[edge[SubscriptionStatus]][]rule{
	{SubscriptionIncomplete, SubscriptionActive}: {{
		event:  EventSubscriptionActivated,
		causes: []Cause{CauseInvoicePaid},
	}},
	{SubscriptionIncomplete, SubscriptionCanceled}: {{
		event:  EventSubscriptionCanceled,
		causes: []Cause{CauseActivationExpired, CauseManual, CauseCustomer},
	}},
	{SubscriptionTrialing, SubscriptionActive}: {{
		event:  EventSubscriptionActivated,
		causes: []Cause{CauseTrialEnded, CauseInvoicePaid},
	}},
	{SubscriptionTrialing, SubscriptionPastDue}: {{
		event:  EventSubscriptionPastDue,
		causes: []Cause{CauseTrialEnded, CausePaymentFailed},
	}},
	{SubscriptionTrialing, SubscriptionPaused}: {{
		event:  EventSubscriptionPaused,
		causes: []Cause{CauseManual},
	}},
	{SubscriptionTrialing, SubscriptionCanceled}: {{
		event:  EventSubscriptionCanceled,
		causes: []Cause{CauseManual, CauseCustomer},
	}},
	// Two distinct self-edges on ACTIVE. Renewal rolls the period; scheduling a
	// cancellation for the period end changes nothing about the state — which is
	// exactly why cancel_at_period_end is a flag and not a status. Treating it as
	// a status is what produces the combinatorial state explosion.
	//
	// The scheduling edge accepts three causes because "why is this ending" has
	// three different answers: an integration scheduled it, an operator did, or
	// the customer did it themselves in the portal. CauseScheduleCancel alone
	// only restates the change, and a trail that cannot separate an operator's
	// cancellation from the customer's own is the trail somebody needs six months
	// later, during the argument about it.
	{SubscriptionActive, SubscriptionActive}: {
		{event: EventSubscriptionRenewed, causes: []Cause{CauseRenewal}},
		{event: EventSubscriptionUpdated, causes: []Cause{CauseScheduleCancel, CauseManual, CauseCustomer}},
	},
	{SubscriptionActive, SubscriptionPastDue}: {{
		event:  EventSubscriptionPastDue,
		causes: []Cause{CausePaymentFailed, CauseScheduler},
	}},
	{SubscriptionActive, SubscriptionPaused}: {{
		event:  EventSubscriptionPaused,
		causes: []Cause{CauseManual},
	}},
	{SubscriptionActive, SubscriptionCanceled}: {{
		event:  EventSubscriptionCanceled,
		causes: []Cause{CauseManual, CauseCustomer, CauseScheduler},
	}},
	{SubscriptionPastDue, SubscriptionActive}: {{
		event:  EventSubscriptionRecovered,
		causes: []Cause{CauseInvoicePaid},
	}},
	{SubscriptionPastDue, SubscriptionCanceled}: {{
		event:  EventSubscriptionCanceled,
		causes: []Cause{CauseDunningExhausted, CauseManual, CauseCustomer},
	}},
	{SubscriptionPaused, SubscriptionActive}: {{
		event:  EventSubscriptionResumed,
		causes: []Cause{CauseManual},
	}},
	// Not in the § 6.2 table, added here on purpose: without it a paused
	// subscription can never be ended, and the operator's only way out is to
	// resume it just to cancel it — which generates a spurious resume event and a
	// billing period nobody wanted.
	{SubscriptionPaused, SubscriptionCanceled}: {{
		event:  EventSubscriptionCanceled,
		causes: []Cause{CauseManual, CauseCustomer},
	}},
}

// Subscription is the recurrence itself. Cancellation is terminal: reactivating
// means creating a new subscription, which keeps the history of what was agreed
// when honest.
type Subscription struct {
	ID             string             `dynamodbav:"id"              json:"id"`
	OrganizationID string             `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool               `dynamodbav:"livemode"        json:"livemode"`
	CustomerID     string             `dynamodbav:"customer_id"     json:"customer_id"`
	Status         SubscriptionStatus `dynamodbav:"status"          json:"status"`

	Recurrence Recurrence    `dynamodbav:"recurrence"     json:"recurrence"`
	Timing     BillingTiming `dynamodbav:"billing_timing" json:"billing_timing"`

	// Anchor is the date every period is derived from. Periods are computed as
	// anchor + n recurrences, never by chaining from the previous period, so an
	// end-of-month clamp can never permanently move the customer's billing day.
	Anchor brcal.Date `dynamodbav:"anchor" json:"anchor"`
	// PeriodIndex is which period the subscription is currently in, 0-based.
	PeriodIndex int `dynamodbav:"period_index" json:"period_index"`

	// NetDays is how many days after the period boundary the invoice falls due,
	// before the business-day roll-forward.
	NetDays int `dynamodbav:"net_days" json:"net_days"`

	// Since is when this subscription was created, RFC 3339 in UTC. It is the
	// row's own created_at rather than a second stored copy — `dynamodbav:"-"`,
	// so it is never written — and the repository fills it on every read.
	//
	// It exists because "cliente desde" is a question every billing screen is
	// asked and none of the period fields answer: current_period_start moves
	// every month, and the customer record's own age is when somebody was
	// registered, not when they started paying.
	Since string `dynamodbav:"-" json:"since,omitempty"`

	// TrialEnd is set while the subscription is TRIALING.
	TrialEnd brcal.Date `dynamodbav:"trial_end,omitempty" json:"trial_end,omitempty"`

	// CancelAtPeriodEnd schedules cancellation without changing the status. The
	// scheduler performs the actual transition when the period closes.
	CancelAtPeriodEnd bool   `dynamodbav:"cancel_at_period_end" json:"cancel_at_period_end"`
	CancelReason      string `dynamodbav:"cancel_reason,omitempty" json:"cancel_reason,omitempty"`

	// OwnerKey names the service whose product this subscription is for, copied
	// from that product when the subscription is created. It routes every event
	// this subscription and its invoices emit (ADR 0016).
	//
	// Copied rather than derived on each event: reaching it live means
	// subscription → items → prices → products, three reads to answer a question
	// whose answer cannot change, because a subscription does not move between
	// services. The cost of the copy is that a future item change must recompute
	// it — and item changes (upgrade, downgrade) do not exist yet, so the place
	// to recompute it is the code that introduces them.
	OwnerKey string `dynamodbav:"owner_key,omitempty" json:"owner_key,omitempty"`

	Metadata Metadata `dynamodbav:"metadata,omitempty" json:"metadata,omitempty"`
}

// CurrentPeriod returns the period the subscription is in.
func (s *Subscription) CurrentPeriod() Period {
	return s.Recurrence.PeriodAt(s.Anchor, s.PeriodIndex)
}

// NextPeriod returns the period after the current one.
func (s *Subscription) NextPeriod() Period {
	return s.Recurrence.PeriodAt(s.Anchor, s.PeriodIndex+1)
}

// IsEntitled reports whether the subscription currently grants service.
//
// This is the single answer to "can this customer use the product?" that
// § 13 requires billing to expose — otherwise every product reimplements it, and
// they disagree the first time a state is added.
//
// PAST_DUE grants service on purpose: the customer had it and the dunning policy
// has not given up yet. Revocation happens on CANCELED, which is where the
// decision belongs.
func (s *Subscription) IsEntitled() bool {
	switch s.Status {
	case SubscriptionTrialing, SubscriptionActive, SubscriptionPastDue:
		return true
	default:
		return false
	}
}

// Transition moves the subscription to `to`, returning the events to emit.
//
// A renewal (ACTIVE -> ACTIVE by CauseRenewal) also advances the period index —
// that is the whole point of the edge, and doing it here rather than in the
// caller is what keeps "renewed" and "moved to the next period" from ever
// diverging.
func (s *Subscription) Transition(to SubscriptionStatus, cause Cause) ([]EventType, error) {
	event, err := apply(subscriptionTransitions, s.Status, to, cause)
	if err != nil {
		return nil, err
	}
	s.Status = to
	if event == EventSubscriptionRenewed {
		s.PeriodIndex++
	}
	return []EventType{event}, nil
}
