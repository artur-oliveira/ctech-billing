package billing

import "errors"

// MerchantOwnerKey is CTech Billing's own owner in tenant zero's catalogue
// (ADR 0016): an organization's subscription to a product with this owner is
// what makes it a merchant in live mode (spec § 4, D3).
const MerchantOwnerKey = "billing"

// ApplicationStatus is where an organization's request to bill through CTech
// Billing stands (spec § 4).
type ApplicationStatus string

const (
	ApplicationPending  ApplicationStatus = "pending"
	ApplicationApproved ApplicationStatus = "approved"
	ApplicationRejected ApplicationStatus = "rejected"
)

func (s ApplicationStatus) Valid() bool {
	switch s {
	case ApplicationPending, ApplicationApproved, ApplicationRejected:
		return true
	}
	return false
}

// MonthlyVolume is the applicant's own estimate, one of a fixed list whose
// labels live in the console's i18n. Optional: "" is valid.
type MonthlyVolume string

const (
	VolumeUpTo10k   MonthlyVolume = "up_to_10k"
	Volume10kTo50k  MonthlyVolume = "10k_to_50k"
	Volume50kTo200k MonthlyVolume = "50k_to_200k"
	VolumeOver200k  MonthlyVolume = "over_200k"
)

func (v MonthlyVolume) Valid() bool {
	switch v {
	case "", VolumeUpTo10k, Volume10kTo50k, Volume50kTo200k, VolumeOver200k:
		return true
	}
	return false
}

// ErrApplicationNotPending is a decision on an application already decided the
// other way (approving a rejected one, rejecting an approved one).
var ErrApplicationNotPending = errors.New("application is not pending")

// ErrAlreadyMerchant is an application from an organization that already has a
// live tenant: there is nothing left to ask for.
var ErrAlreadyMerchant = errors.New("organization already bills through CTech Billing in live mode")

// Application is one request, stored in tenant zero's live partition. It is
// history: a rejected one stays when the organization applies again.
//
// The organization and company names are copies taken when it was made, so the
// review screen needs no ctech-account call per row; they are display data.
type Application struct {
	ID                    string `dynamodbav:"id"                      json:"id"`
	AccountOrganizationID string `dynamodbav:"account_organization_id" json:"account_organization_id"`
	OrganizationName      string `dynamodbav:"organization_name"       json:"organization_name"`
	// TenantID is the merchant tenant's id, the same in both modes.
	TenantID         string `dynamodbav:"tenant_id"          json:"tenant_id"`
	CompanyID        string `dynamodbav:"company_id"         json:"company_id"`
	CompanyLegalName string `dynamodbav:"company_legal_name" json:"company_legal_name"`
	// CompanyTaxID is readable here and sealed in the row (repositories), like
	// a customer's. Never serialized from the domain struct.
	CompanyTaxID          string        `dynamodbav:"company_tax_id"                    json:"-"`
	Description           string        `dynamodbav:"description"                       json:"description"`
	Website               string        `dynamodbav:"website,omitempty"                 json:"website,omitempty"`
	ExpectedMonthlyVolume MonthlyVolume `dynamodbav:"expected_monthly_volume,omitempty" json:"expected_monthly_volume,omitempty"`
	ContactEmail          string        `dynamodbav:"contact_email"                     json:"contact_email"`
	// AppliedBy is the ctech-account subject who applied.
	AppliedBy       string            `dynamodbav:"applied_by"                 json:"applied_by"`
	AppliedAt       string            `dynamodbav:"applied_at"                 json:"applied_at"`
	Status          ApplicationStatus `dynamodbav:"status"                     json:"status"`
	DecidedBy       string            `dynamodbav:"decided_by,omitempty"       json:"decided_by,omitempty"`
	DecidedAt       string            `dynamodbav:"decided_at,omitempty"       json:"decided_at,omitempty"`
	RejectionReason string            `dynamodbav:"rejection_reason,omitempty" json:"rejection_reason,omitempty"`
	// SubscriptionID is the prod_billing subscription approval created or reused.
	SubscriptionID string `dynamodbav:"subscription_id,omitempty" json:"subscription_id,omitempty"`
}
