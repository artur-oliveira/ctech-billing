package services

import (
	"context"
	"errors"
	"sync"

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
	mu   sync.Mutex // the sweep's workers share one cache
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
	c.mu.Lock()
	v, ok := c.seen[organizationID]
	c.mu.Unlock()
	if ok {
		return v, nil
	}
	// The read is outside the lock: two workers asking about the same tenant at the
	// same moment both read it, which costs one extra read and cannot disagree.
	org, err := c.orgs.Get(ctx, organizationID, true)
	suspended := false
	switch {
	case errors.Is(err, repositories.ErrNotFound):
	case err != nil:
		return false, err
	default:
		suspended = org.Suspended()
	}
	c.mu.Lock()
	c.seen[organizationID] = suspended
	c.mu.Unlock()
	return suspended, nil
}
