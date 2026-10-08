package space

import (
	"context"
	"encoding/json"
	"log/slog"

	"gopkg.aoctech.app/api-commons/cache"
)

// MembershipSource is the one question the resolver asks ctech-account.
// A refusal is ("", false, nil) — "not a member" is an answer. Anything that
// stops us from knowing is an error, and the resolver refuses (ADR 0025 rule 7).
type MembershipSource interface {
	Membership(ctx context.Context, organizationID, userID string) (role string, member bool, err error)
}

// MembershipTTLSeconds is how long an answer — positive or negative — is
// trusted. Accepted limit: a removed member may keep reading for this long.
const MembershipTTLSeconds = 60

// Resolver turns (token subject, selector, mode) into a ResolvedSpace. It is
// the only place that does.
type Resolver struct {
	src   MembershipSource
	cache cache.Backend
}

// NewResolver builds a resolver. A nil src is allowed and means "organization
// spaces are unavailable" (fail closed); personal spaces never need it.
func NewResolver(src MembershipSource, c cache.Backend) *Resolver {
	return &Resolver{src: src, cache: c}
}

type cachedMembership struct {
	Member bool   `json:"member"`
	Role   string `json:"role"`
}

// The key carries BOTH ids. Keyed by organization alone, one member's cached
// grant would be handed to the next caller and would look like a cache hit.
func cacheKey(orgID, userID string) string {
	return "billing:space:" + orgID + ":" + userID
}

// Resolve returns the caller's space. sub is the token's subject — the only
// identity input; nothing from the request body or path reaches here.
func (r *Resolver) Resolve(ctx context.Context, sub string, sel Selector, livemode bool) (ResolvedSpace, error) {
	if sel.Personal {
		return personalSpace(sub, livemode)
	}
	if sub == "" {
		return ResolvedSpace{}, ErrInvalidSubject
	}
	// Re-validated here, not only in ParseSelector: a Selector built by hand
	// must not be able to put a key-shaped string into a partition key or a
	// ctech-account URL.
	if !validOrganizationID(sel.OrganizationID) {
		return ResolvedSpace{}, ErrSpaceNotFound
	}

	m, err := r.membership(ctx, sel.OrganizationID, sub)
	if err != nil {
		return ResolvedSpace{}, err
	}
	verbs := VerbsForRole(m.Role)
	if !m.Member || verbs == 0 {
		return ResolvedSpace{}, ErrSpaceNotFound
	}
	return orgSpace(sel.OrganizationID, livemode, verbs), nil
}

func (r *Resolver) membership(ctx context.Context, orgID, userID string) (cachedMembership, error) {
	key := cacheKey(orgID, userID)
	if r.cache != nil {
		if raw, ok, err := r.cache.Get(ctx, key); err == nil && ok {
			var m cachedMembership
			if json.Unmarshal(raw, &m) == nil {
				return m, nil
			}
		}
	}
	if r.src == nil {
		return cachedMembership{}, ErrSpaceUnavailable
	}
	role, member, err := r.src.Membership(ctx, orgID, userID)
	if err != nil {
		// Not cached: the answer would be "we do not know", and caching it turns
		// a blip into a minute of refusals for people who are entitled.
		slog.Warn("space: membership lookup failed", "organization", orgID, "error", err)
		return cachedMembership{}, ErrSpaceUnavailable
	}
	m := cachedMembership{Member: member, Role: role}
	if r.cache != nil {
		if raw, merr := json.Marshal(m); merr == nil {
			// Best effort: a cache that cannot be written costs a round trip,
			// not correctness.
			_ = r.cache.Set(ctx, key, raw, MembershipTTLSeconds)
		}
	}
	return m, nil
}
