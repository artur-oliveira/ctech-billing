package provision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Repos is what applying a plan needs. It is an explicit struct rather than the
// whole repository set so that this package cannot grow into a second write path
// for entities it has no business touching — an invoice, a payment, an audit row.
type Repos struct {
	Organizations *repositories.OrganizationRepository
	Credentials   *repositories.CredentialRepository
	Catalog       *repositories.CatalogRepository
	Webhooks      *repositories.WebhookRepository
}

// Result reports what one Apply did, per row.
type Result struct {
	Created []string
	Skipped []string
}

func (r *Result) created(kind, id string) { r.Created = append(r.Created, kind+" "+id) }
func (r *Result) skipped(kind, id string) { r.Skipped = append(r.Skipped, kind+" "+id) }

// Apply writes the plan into one mode, skipping what is already there.
//
// **Create-or-skip, never update.** A price is immutable by design (ADR 0001's
// model), and the same discipline is applied to everything else here for a
// different reason: a seed that updates is a seed that can silently rewrite a
// live tenant when somebody re-runs it against the wrong environment. Changing
// an existing tenant is an operator action with an audit trail, not a file
// re-applied.
//
// It is therefore safe to run twice, which is what makes it usable as the first
// half of a deploy runbook rather than a one-shot somebody has to remember not
// to repeat.
func Apply(ctx context.Context, repos Repos, plan *Plan, livemode bool, now time.Time) (*Result, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	orgID := plan.Organization.ID
	res := &Result{}

	// The organization first, and everything else only if it exists: a credential
	// pointing at an organization that is not there resolves to a tenant with no
	// payout gate and no owner, which every later read would treat as merely empty.
	switch existing, err := repos.Organizations.Get(ctx, orgID, livemode); {
	case err == nil:
		// The payout gate is the one field worth refusing to diverge on quietly. A
		// plan that says `enabled` against a tenant sitting at `not_configured` is
		// either a mistake or an attempt to open the gate through a file, and both
		// deserve to stop here.
		if plan.Organization.PayoutStatus != "" && existing.PayoutStatus != plan.Organization.PayoutStatus {
			return nil, fmt.Errorf(
				"organization %s already exists with payout_status %q, plan says %q — move the gate deliberately, not through a seed",
				orgID, existing.PayoutStatus, plan.Organization.PayoutStatus)
		}
		res.skipped("organization", orgID)
	case errors.Is(err, repositories.ErrNotFound):
		org := &billing.Organization{
			ID:           orgID,
			DisplayName:  plan.Organization.DisplayName,
			Livemode:     livemode,
			PayoutStatus: plan.Organization.PayoutStatus,
			OwnerUserID:  plan.Organization.OwnerUserID,
		}
		if err := repos.Organizations.Create(ctx, org, now); err != nil {
			return nil, fmt.Errorf("creating organization %s: %w", orgID, err)
		}
		res.created("organization", orgID)
	default:
		return nil, fmt.Errorf("reading organization %s: %w", orgID, err)
	}

	for _, c := range plan.Credentials {
		// Resolve is the global lookup, so this also catches a client id already
		// admitted to a *different* tenant — which is the failure worth catching
		// loudly. Two tenants claiming one client id is a request that resolves to
		// whichever row the index returns first.
		switch existing, err := repos.Credentials.Resolve(ctx, c.ClientID); {
		case err == nil:
			if existing.OrganizationID != orgID {
				return nil, fmt.Errorf(
					"credential %s is already admitted to organization %s, not %s",
					c.ClientID, existing.OrganizationID, orgID)
			}
			res.skipped("credential", c.ClientID)
			continue
		case !errors.Is(err, repositories.ErrNotFound):
			// An inactive credential fails Validate inside Resolve rather than
			// returning ErrNotFound. Recreating it would silently re-enable a
			// credential somebody deliberately switched off.
			if errors.Is(err, billing.ErrCredentialInactive) {
				res.skipped("credential", c.ClientID+" (inactive)")
				continue
			}
			return nil, fmt.Errorf("resolving credential %s: %w", c.ClientID, err)
		}

		cred := &billing.APICredential{
			ClientID:       c.ClientID,
			OrganizationID: orgID,
			Livemode:       livemode,
			Description:    c.Description,
			Active:         true,
		}
		if err := repos.Credentials.Create(ctx, cred, now); err != nil {
			return nil, fmt.Errorf("creating credential %s: %w", c.ClientID, err)
		}
		res.created("credential", c.ClientID)
	}

	for _, p := range plan.Products {
		switch _, err := repos.Catalog.GetProduct(ctx, orgID, livemode, p.ID); {
		case err == nil:
			res.skipped("product", p.ID)
			continue
		case !errors.Is(err, repositories.ErrNotFound):
			return nil, fmt.Errorf("reading product %s: %w", p.ID, err)
		}
		if err := repos.Catalog.CreateProduct(ctx, p.entity(orgID, livemode), now); err != nil {
			return nil, fmt.Errorf("creating product %s: %w", p.ID, err)
		}
		res.created("product", p.ID)
	}

	for _, p := range plan.Prices {
		switch _, err := repos.Catalog.GetPrice(ctx, orgID, livemode, p.ID); {
		case err == nil:
			res.skipped("price", p.ID)
			continue
		case !errors.Is(err, repositories.ErrNotFound):
			return nil, fmt.Errorf("reading price %s: %w", p.ID, err)
		}
		if err := repos.Catalog.CreatePrice(ctx, p.entity(orgID, livemode), now); err != nil {
			return nil, fmt.Errorf("creating price %s: %w", p.ID, err)
		}
		res.created("price", p.ID)
	}

	for _, e := range plan.Endpoints {
		switch _, err := repos.Webhooks.GetEndpoint(ctx, orgID, livemode, e.ID); {
		case err == nil:
			res.skipped("webhook endpoint", e.ID)
			continue
		case !errors.Is(err, repositories.ErrNotFound):
			return nil, fmt.Errorf("reading webhook endpoint %s: %w", e.ID, err)
		}
		// The secret comes from the environment, never the file. A missing one is
		// a hard failure rather than a generated value: an endpoint whose secret
		// this process invented is an endpoint whose consumer cannot verify a
		// single delivery, and they would not find out until the first event.
		secret := os.Getenv(e.SecretEnv)
		if secret == "" {
			return nil, fmt.Errorf("webhook endpoint %s: %s is not set in the environment", e.ID, e.SecretEnv)
		}
		if err := repos.Webhooks.CreateEndpoint(ctx, e.entity(orgID, livemode, secret), now); err != nil {
			return nil, fmt.Errorf("creating webhook endpoint %s: %w", e.ID, err)
		}
		res.created("webhook endpoint", e.ID)
	}

	return res, nil
}
