package billing

import "strings"

// Customer is the invoice's addressee.
//
// The spec originally forbade PII entirely (ARCHITECTURE.md § 9) and the product
// requires it — an invoice needs a name, and a due-date notification needs an
// email. The resolution (assessment § 8) is to store the **minimum needed to
// invoice and to notify**, and nothing else:
//
//   - phone is not stored: there is no SMS/WhatsApp notification in the MVP, so
//     it would be PII held for a feature that does not exist.
//   - address is stored only when the organization emits NFS-e for the customer.
//   - card data, PIX keys and bank accounts are **never** stored. Those belong to
//     wallet and the PSP; billing references opaque ids.
type Customer struct {
	ID             string `dynamodbav:"id"              json:"id"`
	OrganizationID string `dynamodbav:"organization_id" json:"organization_id"`
	Livemode       bool   `dynamodbav:"livemode"        json:"livemode"`

	// ExternalRef links the customer to the caller's own identifier, so a product
	// does not have to store a mapping table of its own.
	ExternalRef string `dynamodbav:"external_ref,omitempty" json:"external_ref,omitempty"`

	// UserID is this customer's ctech-account subject, when the customer is a
	// person with a CTech account. It is what lets them sign in to the portal and
	// see their own invoices (ADR 0012).
	//
	// It is a **reference**, not new personal data — billing stores the subject
	// and nothing about the person behind it. It is supplied by the merchant on
	// create and is never inferred from the email address: an address changes, an
	// address is mistyped, and matching on one would hand a stranger somebody
	// else's invoices.
	UserID string `dynamodbav:"user_id,omitempty" json:"user_id,omitempty"`

	Name  string `dynamodbav:"name"  json:"name"`
	Email string `dynamodbav:"email" json:"email"`

	// TaxID is a CPF or CNPJ, required for NFS-e. It is readable here and
	// encrypted in the row: CustomerRepository seals it through internal/crypto on
	// the way in and opens it on the way out, so DynamoDB's own at-rest encryption
	// is not the only thing between a stored CPF and anything holding table
	// access. It is never returned in full in a listing either — not even in the
	// console, where revealing it is a separate, audited action.
	TaxID string `dynamodbav:"tax_id,omitempty" json:"-"`

	Locale   string `dynamodbav:"locale,omitempty"   json:"locale,omitempty"`
	Timezone string `dynamodbav:"timezone,omitempty" json:"timezone,omitempty"`
	Currency string `dynamodbav:"currency,omitempty" json:"currency,omitempty"`

	// Anonymized marks a customer whose identifying content was erased on a
	// deletion request. The record itself stays: an issued invoice is a document
	// and cannot vanish because someone asked to be forgotten.
	Anonymized bool `dynamodbav:"anonymized" json:"anonymized"`

	Metadata Metadata `dynamodbav:"metadata,omitempty" json:"metadata,omitempty"`
}

// MaskedTaxID renders the tax id with only its last digits visible, which is what
// every list and every default view shows. Revealing the full value is a separate
// action that writes an audit entry — reading a tax id is audited, not only
// writing one (assessment § 8).
//
// Non-digit characters are dropped first, so a value stored as "123.456.789-09"
// and one stored as "12345678909" mask identically.
func MaskedTaxID(taxID string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, taxID)
	const visible = 4
	if len(digits) <= visible {
		return strings.Repeat("•", len(digits))
	}
	return strings.Repeat("•", len(digits)-visible) + digits[len(digits)-visible:]
}

// Anonymize erases the identifying content of the customer in place, keeping the
// record so that its invoices still have an addressee id.
//
// It clears Metadata too. That is not tidiness: metadata is free-form and is
// propagated in every webhook, so it is exactly where undeclared PII ends up
// (ADR 0008). Leaving it behind would make the erasure a formality.
func (c *Customer) Anonymize() {
	c.Name = "[anonimizado]"
	c.Email = ""
	c.TaxID = ""
	c.ExternalRef = ""
	c.Metadata = nil
	c.Anonymized = true
}
