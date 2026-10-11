package services

import (
	"context"
	"errors"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type tenantReader interface {
	Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error)
}

// suspensionCache answers "is this tenant suspended" once per tenant per run
// (spec § 4.3: scheduled jobs skip a suspended live tenant). Test mode is never
// suspended and is never read.
type suspensionCache struct {
	orgs tenantReader
	seen map[string]bool
}

func newSuspensionCache(orgs *repositories.OrganizationRepository) *suspensionCache {
	c := &suspensionCache{seen: map[string]bool{}}
	if orgs != nil { // a nil pointer must not become a non-nil interface
		c.orgs = orgs
	}
	return c
}

func (c *suspensionCache) skip(ctx context.Context, organizationID string, livemode bool) (bool, error) {
	if !livemode || c.orgs == nil {
		return false, nil
	}
	if v, ok := c.seen[organizationID]; ok {
		return v, nil
	}
	org, err := c.orgs.Get(ctx, organizationID, true)
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		c.seen[organizationID] = false
		return false, nil
	case err != nil:
		return false, err
	}
	c.seen[organizationID] = org.Suspended()
	return c.seen[organizationID], nil
}
