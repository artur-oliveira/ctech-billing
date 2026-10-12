package services

import (
	"context"
	"errors"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// connectionReader is the slice of ByokRepository the gate reads.
type connectionReader interface {
	GetConnection(ctx context.Context, accountOrgID string, livemode bool, provider string) (*byokconn.Connection, error)
}

// ChargeGates builds the one argument of Organization.AuthorizeCharge (spec
// § 7.1). Every collection path and the console's can_charge build it here,
// so no path can forget a fact.
type ChargeGates struct {
	repo     connectionReader
	entitled func(context.Context, *billing.Organization) (bool, error)
	quota    func(context.Context, *billing.Organization) (bool, error)
}

func NewChargeGates(repo connectionReader, entitled func(context.Context, *billing.Organization) (bool, error)) *ChargeGates {
	return &ChargeGates{repo: repo, entitled: entitled}
}

// WithEntitlement sets the live entitlement check (Applications.Entitled).
func (g *ChargeGates) WithEntitlement(f func(context.Context, *billing.Organization) (bool, error)) *ChargeGates {
	g.entitled = f
	return g
}

// WithQuota plugs part C's charge quota in.
func (g *ChargeGates) WithQuota(q func(context.Context, *billing.Organization) (bool, error)) *ChargeGates {
	g.quota = q
	return g
}

// For reads nothing for a wallet-rail tenant. A nil *ChargeGates answers the
// empty gate: the wallet rail passes, the BYOK rail is refused (not connected).
func (g *ChargeGates) For(ctx context.Context, org *billing.Organization) (billing.ChargeGate, error) {
	var out billing.ChargeGate
	if g == nil || org.Rail() != billing.RailBYOK {
		return out, nil
	}
	conn, err := g.repo.GetConnection(ctx, org.AccountOrganizationID, org.Livemode, org.PayoutProvider)
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		return out, nil
	case err != nil:
		return out, err
	}
	out.Connection = billing.ConnectionFacts{Found: conn.State != "", Active: conn.State == byokconn.StateActive,
		Expired: conn.State == byokconn.StateExpired, Collection: conn.Has(byok.CapabilityCollection)}
	if !org.Livemode {
		return out, nil
	}
	if g.entitled != nil {
		if out.Entitled, err = g.entitled(ctx, org); err != nil {
			return out, err
		}
	}
	if g.quota != nil {
		if out.QuotaExceeded, err = g.quota(ctx, org); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Authorize is For followed by AuthorizeCharge: the gate every path calls.
func (g *ChargeGates) Authorize(ctx context.Context, org *billing.Organization) error {
	gate, err := g.For(ctx, org)
	if err != nil {
		return err
	}
	return org.AuthorizeCharge(gate)
}
