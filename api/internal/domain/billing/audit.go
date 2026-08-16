package billing

import "time"

// Audit actions that are not state transitions. Transitions are audited from the
// event they emit; these are the things that have no event and would otherwise
// leave no trace.
const (
	// AuditTaxIDRevealed records an operator revealing a masked tax id. Reading
	// PII is audited, not only writing it (assessment § 8) — without this, a
	// data-subject request cannot be answered honestly.
	AuditTaxIDRevealed = "customer.tax_id_revealed"
	// AuditMetadataChanged records a metadata edit like any other field change.
	AuditMetadataChanged = "metadata.changed"
	// AuditPayoutStatusChanged records the per-merchant charge gate being moved.
	AuditPayoutStatusChanged = "organization.payout_status_changed"
	// AuditManualPaymentRecorded records an out-of-band receipt, naming who
	// recorded it. It must never be indistinguishable from an automatic payment.
	AuditManualPaymentRecorded = "invoice.manual_payment_recorded"
)

// AuditLog is an append-only record of who did what.
//
// It is separate from application logs on purpose: logs rotate and expire, and
// the question "who voided this invoice, and when" has to be answerable years
// later. It is also written in Phase 1 rather than Phase 4 as the original plan
// had it, because audit cannot be applied retroactively — history that was not
// recorded cannot be reconstructed.
type AuditLog struct {
	ID             string `dynamodbav:"id"              json:"id"`
	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`

	// EntityType and EntityID say what was acted on ("invoice", "in_123").
	EntityType string `dynamodbav:"entity_type" json:"entity_type"`
	EntityID   string `dynamodbav:"entity_id"   json:"entity_id"`

	// Action is a transition event type or one of the audit constants above.
	Action string `dynamodbav:"action" json:"action"`
	// Cause is why, when the action came from a state transition.
	Cause Cause `dynamodbav:"cause,omitempty" json:"cause,omitempty"`

	// Actor is who. A user id, an M2M client id, or the name of a job — never
	// empty, because "the system did it" is not an answer during an incident.
	Actor string `dynamodbav:"actor" json:"actor"`

	// Before and After carry the change for actions that mutate a field, and are
	// empty for the ones that do not.
	Before string `dynamodbav:"before,omitempty" json:"before,omitempty"`
	After  string `dynamodbav:"after,omitempty"  json:"after,omitempty"`

	// RequestID ties the entry to the request that caused it, end to end. Support
	// is impossible without it and it costs one line of middleware
	// (assessment § 13).
	RequestID string `dynamodbav:"request_id,omitempty" json:"request_id,omitempty"`
	IP        string `dynamodbav:"ip,omitempty"         json:"ip,omitempty"`

	CreatedAt time.Time `dynamodbav:"created_at" json:"created_at"`
}
