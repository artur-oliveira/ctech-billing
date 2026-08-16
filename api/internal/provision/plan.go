// Package provision applies a declared tenant to an empty deployment.
//
// It exists because nothing else can create the first row. Every write path in
// this service resolves its tenant from a credential (ADR 0003), and a
// credential is itself a row — so a fresh deployment has no way in. That is not
// an oversight in the API design, it is the consequence of a good one: there is
// deliberately no self-service merchant onboarding and no route that accepts an
// `organization_id`, so admission is manual by construction (ADR 0007).
//
// The plan is a **file**, not a sequence of flags. A tenant is a thing with a
// shape — an organization, the integrations allowed to act for it, and a
// catalogue — and the shape is worth reviewing in a pull request rather than
// reconstructing from shell history. The same file seeds test and live, so the
// sandbox a merchant integrates against is the catalogue they will be billed
// against.
package provision

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// Plan is one tenant, mode-independent.
//
// There is no `livemode` anywhere in it, deliberately: the mode is an argument
// to Apply, not a property of the document. A file that named its own mode would
// be two files that drift, and the drift is discovered when a merchant's
// sandbox stops matching what they are charged.
type Plan struct {
	Organization Organization `json:"organization"`
	Credentials  []Credential `json:"credentials,omitempty"`
	Products     []Product    `json:"products,omitempty"`
	Prices       []Price      `json:"prices,omitempty"`
	Endpoints    []Endpoint   `json:"webhook_endpoints,omitempty"`
}

// Endpoint is one outbound webhook destination (ADR 0016).
//
// The secret is **not** in the plan. A signing secret committed to a repository
// is a signing secret anybody with read access can forge with, and this file is
// meant to be reviewed in a pull request. It comes from the environment instead,
// named per endpoint, so the file says which endpoints exist and the deployment
// says what signs them.
type Endpoint struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	// OwnerKey restricts this endpoint to one service's products. Empty means
	// every product in the tenant, which is what an ordinary merchant wants and
	// what tenant zero must never use.
	OwnerKey string `json:"owner_key,omitempty"`
	// Events filters by type. Empty means all of them.
	Events []billing.EventType `json:"events,omitempty"`
	// SecretEnv names the environment variable holding this endpoint's signing
	// secret, e.g. WEBHOOK_SECRET_DFE.
	SecretEnv string `json:"secret_env"`
}

// Organization is the tenant record (ADR 0007). It carries no CNPJ, no address
// and no roles, because the entity does not have them.
type Organization struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	// OwnerUserID is the ctech-account subject who reaches this tenant's console.
	// Optional: a tenant with no owner is API-only, which is the correct shape
	// for one that only ever receives M2M calls.
	OwnerUserID string `json:"owner_user_id,omitempty"`
	// PayoutStatus is the charge gate (ADR 0005). Absent means `not_configured`,
	// which is the only safe default — an organization that could collect money
	// the moment somebody committed a JSON file is a gate that does not exist.
	PayoutStatus billing.PayoutStatus `json:"payout_status,omitempty"`
}

// Credential admits one ctech-account OAuth client to act for this tenant.
//
// Billing stores the client id and nothing else about the client: no secret, no
// scopes, no rotation state. Those live in ctech-account, which is where they
// are implemented and where they belong.
type Credential struct {
	ClientID    string `json:"client_id"`
	Description string `json:"description,omitempty"`
}

// Product is a thing sold. OwnerKey is what routes its events (ADR 0016) and is
// empty for an ordinary merchant, who owns everything in their own catalogue.
type Product struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	OwnerKey string           `json:"owner_key,omitempty"`
	Active   *bool            `json:"active,omitempty"`
	Metadata billing.Metadata `json:"metadata,omitempty"`
}

// Price is what a product costs. It is immutable once written, which is why
// Apply never updates one — see apply.go.
type Price struct {
	ID         string                `json:"id"`
	ProductID  string                `json:"product_id"`
	Type       billing.PriceType     `json:"type"`
	Currency   string                `json:"currency,omitempty"`
	UnitAmount billing.Cents         `json:"unit_amount"`
	Recurrence billing.Recurrence    `json:"recurrence"`
	Timing     billing.BillingTiming `json:"billing_timing"`
	Metadata   billing.Metadata      `json:"metadata,omitempty"`
}

// Parse reads a plan and rejects one that cannot be applied.
//
// Unknown fields are an error. A plan is hand-written, and the two mistakes it
// is actually exposed to are a typo in a field name and a field from a newer
// schema — both of which a lenient decoder turns into a tenant that is silently
// missing something.
func Parse(r io.Reader) (*Plan, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var p Plan
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parsing plan: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks the plan is internally consistent before anything is written.
//
// It runs entirely before the first write, and that ordering is the point: a
// plan half-applied because its fourth price is malformed leaves an operator
// guessing which rows exist.
func (p *Plan) Validate() error {
	if strings.TrimSpace(p.Organization.ID) == "" {
		return fmt.Errorf("organization.id is required")
	}
	if strings.TrimSpace(p.Organization.DisplayName) == "" {
		return fmt.Errorf("organization.display_name is required")
	}
	switch p.Organization.PayoutStatus {
	case "", billing.PayoutNotConfigured, billing.PayoutPendingCustody, billing.PayoutEnabled:
	default:
		return fmt.Errorf("organization.payout_status %q is not a known status", p.Organization.PayoutStatus)
	}

	seen := map[string]string{}
	claim := func(kind, id string) error {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%s: id is required", kind)
		}
		if prev, dup := seen[id]; dup {
			return fmt.Errorf("%s %q is declared twice (already used by %s)", kind, id, prev)
		}
		seen[id] = kind
		return nil
	}

	for _, c := range p.Credentials {
		if err := claim("credential", c.ClientID); err != nil {
			return err
		}
	}

	products := map[string]bool{}
	for _, prod := range p.Products {
		if err := claim("product", prod.ID); err != nil {
			return err
		}
		if strings.TrimSpace(prod.Name) == "" {
			return fmt.Errorf("product %q: name is required", prod.ID)
		}
		if err := prod.Metadata.Validate(); err != nil {
			return fmt.Errorf("product %q: %w", prod.ID, err)
		}
		products[prod.ID] = true
	}

	for _, e := range p.Endpoints {
		if err := claim("webhook endpoint", e.ID); err != nil {
			return err
		}
		if strings.TrimSpace(e.SecretEnv) == "" {
			return fmt.Errorf("webhook endpoint %q: secret_env is required", e.ID)
		}
		// Validated with a placeholder secret: the real one is not known until
		// Apply reads the environment, and everything else about the endpoint —
		// the scheme, the host, the event names — is worth rejecting now rather
		// than after half the plan is written.
		if err := e.entity("validation", false, strings.Repeat("x", 32)).Validate(); err != nil {
			return fmt.Errorf("webhook endpoint %q: %w", e.ID, err)
		}
	}

	for _, pr := range p.Prices {
		if err := claim("price", pr.ID); err != nil {
			return err
		}
		// The product has to be in this same plan. Pointing at one that already
		// exists in the table would work, and it would also silently work when the
		// id is a typo for a product in another tenant.
		if !products[pr.ProductID] {
			return fmt.Errorf("price %q references product %q, which this plan does not declare", pr.ID, pr.ProductID)
		}
		// Validate through the domain rather than re-checking fields here. The
		// rules that make a price billable — metered implies arrears, currency,
		// non-negative amount — belong in one place, and this is not it.
		if err := pr.entity("validation", false).Validate(); err != nil {
			return fmt.Errorf("price %q: %w", pr.ID, err)
		}
	}
	return nil
}

// entity renders the domain value this declaration describes.
func (p Price) entity(organizationID string, livemode bool) *billing.Price {
	currency := p.Currency
	if currency == "" {
		currency = billing.CurrencyBRL
	}
	return &billing.Price{
		ID:             p.ID,
		OrganizationID: organizationID,
		Livemode:       livemode,
		ProductID:      p.ProductID,
		Type:           p.Type,
		Currency:       currency,
		UnitAmount:     p.UnitAmount,
		Recurrence:     p.Recurrence,
		Timing:         p.Timing,
		Metadata:       p.Metadata,
	}
}

func (p Product) entity(organizationID string, livemode bool) *billing.Product {
	// Absent means active. A product declared in a plan and inert on arrival is
	// the less useful default, and `"active": false` says so explicitly when it
	// is what somebody means.
	active := true
	if p.Active != nil {
		active = *p.Active
	}
	return &billing.Product{
		ID:             p.ID,
		OrganizationID: organizationID,
		Livemode:       livemode,
		Name:           p.Name,
		OwnerKey:       p.OwnerKey,
		Active:         active,
		Metadata:       p.Metadata,
	}
}

func (e Endpoint) entity(organizationID string, livemode bool, secret string) *billing.WebhookEndpoint {
	return &billing.WebhookEndpoint{
		ID:             e.ID,
		OrganizationID: organizationID,
		Livemode:       livemode,
		URL:            e.URL,
		Secret:         secret,
		Events:         e.Events,
		OwnerKey:       e.OwnerKey,
		Status:         billing.EndpointActive,
	}
}
