package middleware

import (
	"context"
	"time"

	"gopkg.aoctech.app/api-commons/ttlcache"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// gateCacheMax bounds what one process remembers per gate: one entry per tenant
// seen recently.
const gateCacheMax = 5000

// CachedTenantRows remembers a tenant row for ttl, so the suspension gate on every
// M2M write is not a GetItem each. Only a successful read is remembered (an error
// is not an answer), and a caller gets its own copy: the middleware hands the row
// to the handler. A change made elsewhere is seen when the entry expires. ttl <= 0
// returns inner unchanged.
func CachedTenantRows(inner TenantRows, ttl time.Duration) TenantRows {
	if ttl <= 0 || inner == nil {
		return inner
	}
	return &cachedTenantRows{inner: inner, c: ttlcache.New[billing.Organization](ttl, gateCacheMax)}
}

type cachedTenantRows struct {
	inner TenantRows
	c     *ttlcache.Cache[billing.Organization]
}

func (r *cachedTenantRows) Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error) {
	key := organizationID + "|" + modeKey(livemode)
	if org, ok := r.c.Get(key); ok {
		return &org, nil
	}
	org, err := r.inner.Get(ctx, organizationID, livemode)
	if err != nil {
		return nil, err
	}
	r.c.Set(key, *org)
	return org, nil
}

// CachedClosingFlags remembers whether a tenant is being closed for ttl. Only a
// successful read is remembered. A nil inner (the feature off) stays nil, which is
// what RefuseClosingWrites tests for. ttl <= 0 returns inner unchanged.
func CachedClosingFlags(inner ClosingFlags, ttl time.Duration) ClosingFlags {
	if ttl <= 0 || inner == nil {
		return inner
	}
	return &cachedClosingFlags{inner: inner, c: ttlcache.New[bool](ttl, gateCacheMax)}
}

type cachedClosingFlags struct {
	inner ClosingFlags
	c     *ttlcache.Cache[bool]
}

func (f *cachedClosingFlags) ReadOnly(ctx context.Context, tenantOrgID string) (bool, error) {
	if v, ok := f.c.Get(tenantOrgID); ok {
		return v, nil
	}
	v, err := f.inner.ReadOnly(ctx, tenantOrgID)
	if err != nil {
		return false, err
	}
	f.c.Set(tenantOrgID, v)
	return v, nil
}

func modeKey(livemode bool) string {
	if livemode {
		return "live"
	}
	return "test"
}
