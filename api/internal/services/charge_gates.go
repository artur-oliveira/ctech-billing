package services

import (
	"context"
	"errors"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// connectionReader is the slice of ByokRepository the gate reads.
type connectionReader interface {
	GetConnection(ctx context.Context, accountOrgID string, livemode bool, provider string) (*byokconn.Connection, error)
	ChargesThisMonth(ctx context.Context, tenantID, month string) (int64, error)
}

// planReader resolves a tenant's plan (BillingPlans).
type planReader interface {
	For(ctx context.Context, org *billing.Organization) (PlanLimits, bool, error)
}

// ChargeGates builds the one argument of Organization.AuthorizeCharge (spec
// § 7.1). Every collection path and the console's can_charge build it here,
// so no path can forget a fact.
type ChargeGates struct {
	repo  connectionReader
	plans planReader
	now   func() time.Time
}

func NewChargeGates(repo connectionReader, plans planReader) *ChargeGates {
	return &ChargeGates{repo: repo, plans: plans, now: time.Now}
}

// For reads nothing for a wallet-rail tenant. A nil *ChargeGates answers the
// empty gate: the wallet rail passes, the BYOK rail is refused (not connected).
func (g *ChargeGates) For(ctx context.Context, org *billing.Organization) (billing.ChargeGate, error) {
	gate, _, err := g.facts(ctx, org)
	return gate, err
}

// facts is For plus the refusing charge quota the attempt's transaction
// enforces (-1: none). The plan is read once for both.
func (g *ChargeGates) facts(ctx context.Context, org *billing.Organization) (billing.ChargeGate, int64, error) {
	var out billing.ChargeGate
	if g == nil || org.Rail() != billing.RailBYOK {
		return out, -1, nil
	}
	conn, err := g.repo.GetConnection(ctx, org.AccountOrganizationID, org.Livemode, org.PayoutProvider)
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		return out, -1, nil
	case err != nil:
		return out, -1, err
	}
	out.Connection = billing.ConnectionFacts{Found: conn.State != "", Active: conn.State == byokconn.StateActive,
		Expired: conn.State == byokconn.StateExpired, Collection: conn.Has(byok.CapabilityCollection)}
	if !org.Livemode || g.plans == nil {
		return out, -1, nil
	}
	limits, ok, err := g.plans.For(ctx, org)
	if err != nil {
		return out, -1, err
	}
	out.Entitled = ok
	if !ok || !limits.RefuseOverage || limits.ChargesPerMonth < 0 {
		return out, -1, nil
	}
	// Spec § 12.5: only a refusing plan (Grátis) stops at its quota. This read
	// lets the console say so before a finalization; the transaction's
	// condition is what holds under concurrency.
	used, err := g.repo.ChargesThisMonth(ctx, org.ID, repositories.ChargeMonth(g.now()))
	if err != nil {
		return out, -1, err
	}
	out.QuotaExceeded = used >= limits.ChargesPerMonth
	return out, limits.ChargesPerMonth, nil
}

// Authorize is For followed by AuthorizeCharge: the gate every path calls.
func (g *ChargeGates) Authorize(ctx context.Context, org *billing.Organization) error {
	_, err := g.AuthorizeWithLimit(ctx, org)
	return err
}

// AuthorizeWithLimit is Authorize for a path that opens a charge: it also
// answers the quota the attempt's counter write must hold (-1: none).
func (g *ChargeGates) AuthorizeWithLimit(ctx context.Context, org *billing.Organization) (int64, error) {
	gate, limit, err := g.facts(ctx, org)
	if err != nil {
		return -1, err
	}
	return limit, org.AuthorizeCharge(gate)
}
