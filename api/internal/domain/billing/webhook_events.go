package billing

import "slices"

// webhookEventTypes is what a merchant may subscribe an endpoint to (spec
// § 5.2): every event the domain emits. Pinned to the source by
// TestTheWebhookEventListIsEveryEventConstant.
var webhookEventTypes = func() []EventType {
	list := []EventType{
		EventInvoiceCreated, EventInvoiceFinalized, EventInvoicePaid, EventInvoicePaymentFailed,
		EventInvoiceUncollectible, EventInvoiceVoided,
		EventSubscriptionCreated, EventSubscriptionActivated, EventSubscriptionPastDue,
		EventSubscriptionRecovered, EventSubscriptionRenewed, EventSubscriptionPaused,
		EventSubscriptionResumed, EventSubscriptionCanceled, EventSubscriptionUpdated,
		EventPaymentAttempted, EventPaymentSucceeded, EventPaymentFailed, EventPaymentAbandoned,
		EventCheckoutCreated, EventCheckoutCompleted, EventCheckoutExpired, EventCheckoutCanceled,
		EventCreditNoteCreated,
		EventPayoutDone, EventPayoutFailed,
	}
	slices.Sort(list)
	return list
}()

// WebhookEventTypes returns the subscribable events, sorted. A copy.
func WebhookEventTypes() []EventType { return slices.Clone(webhookEventTypes) }

// IsWebhookEventType reports a subscribable event.
func IsWebhookEventType(t EventType) bool {
	_, ok := slices.BinarySearch(webhookEventTypes, t)
	return ok
}
