package v1

import "gopkg.aoctech.app/billing/api/internal/domain/billing"

type applicationCompany struct {
	ID        string `json:"id"`
	LegalName string `json:"legal_name"`
	TaxID     string `json:"tax_id"`
}

// applicationResponse is one application on both screens: the applicant's own
// and tenant zero's review. Both readers are entitled to the company document.
type applicationResponse struct {
	ID                    string                    `json:"id"`
	Status                billing.ApplicationStatus `json:"status"`
	OrganizationID        string                    `json:"organization_id"`
	OrganizationName      string                    `json:"organization_name"`
	Company               applicationCompany        `json:"company"`
	Description           string                    `json:"description"`
	Website               string                    `json:"website,omitempty"`
	ExpectedMonthlyVolume billing.MonthlyVolume     `json:"expected_monthly_volume,omitempty"`
	ContactEmail          string                    `json:"contact_email"`
	AppliedBy             string                    `json:"applied_by"`
	AppliedAt             string                    `json:"applied_at"`
	DecidedAt             string                    `json:"decided_at,omitempty"`
	RejectionReason       string                    `json:"rejection_reason,omitempty"`
	// Payout is the approved merchant's live collection rail (BYOK spec
	// § 11.3): what tenant zero's review shows per merchant.
	Payout *applicationPayout `json:"payout,omitempty"`
}

type applicationPayout struct {
	Rail     billing.PayoutRail   `json:"rail"`
	Provider string               `json:"provider,omitempty"`
	Status   billing.PayoutStatus `json:"status"`
}

func newApplicationResponse(a *billing.Application) applicationResponse {
	return applicationResponse{
		ID: a.ID, Status: a.Status, OrganizationID: a.AccountOrganizationID, OrganizationName: a.OrganizationName,
		Company:     applicationCompany{ID: a.CompanyID, LegalName: a.CompanyLegalName, TaxID: a.CompanyTaxID},
		Description: a.Description, Website: a.Website, ExpectedMonthlyVolume: a.ExpectedMonthlyVolume,
		ContactEmail: a.ContactEmail, AppliedBy: a.AppliedBy, AppliedAt: a.AppliedAt,
		DecidedAt: a.DecidedAt, RejectionReason: a.RejectionReason,
	}
}

// companiesResponse is the form's picker: at most 200 companies by legal name,
// and whether ctech-account cut the list (the form then says so).
type companiesResponse struct {
	Data      []companyResponse `json:"data"`
	Truncated bool              `json:"truncated"`
}

type companyResponse struct {
	ID        string `json:"company_id"`
	LegalName string `json:"legal_name"`
	TradeName string `json:"trade_name,omitempty"`
	TaxID     string `json:"tax_id"`
}

type applyRequest struct {
	CompanyID             string `json:"company_id"`
	Description           string `json:"description"`
	Website               string `json:"website"`
	ExpectedMonthlyVolume string `json:"expected_monthly_volume"`
	ContactEmail          string `json:"contact_email"`
}

type rejectRequest struct {
	Reason string `json:"reason"`
}
