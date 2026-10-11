package middleware

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// MerchantAccessKey is the Fiber locals key of the MerchantAccess.
const MerchantAccessKey = "merchant_access"

// MerchantAccess is who reached the billing half of the console and as what
// (ADR 0028). It is set by RequireMerchantRole and ResolveMerchantTenant, from
// the resolved space only, never from the request.
type MerchantAccess struct {
	// AccountOrganizationID is the selected ctech-account organization.
	AccountOrganizationID string
	// Role is "owner" or "admin": nothing else gets this far.
	Role string
	// Livemode is the mode the request asked for (X-Billing-Mode).
	Livemode bool
}

// GetMerchantAccess returns the request's MerchantAccess; the zero value when
// neither middleware ran.
func GetMerchantAccess(c fiber.Ctx) MerchantAccess {
	a, _ := c.Locals(MerchantAccessKey).(MerchantAccess)
	return a
}

// MerchantTenants is the one read the billing half makes before it knows the
// tenant (OrganizationRepository satisfies it).
type MerchantTenants interface {
	GetByAccountOrganization(ctx context.Context, accountOrgID string, livemode bool) (*billing.Organization, error)
}

// RequireUserSession refuses a request that is not a person's: no claims is a
// 401, a service token (no sid) a 403. It runs before ResolveSpace so an M2M
// client id is never sent to ctech-account as if it were a person.
func RequireUserSession() fiber.Handler {
	return func(c fiber.Ctx) error {
		cl := GetClaims(c)
		if cl == nil {
			return problem.Unauthorized("credenciais ausentes").Send(c)
		}
		if cl.SID == "" {
			return problem.Forbidden("this route requires a user session").WithCode("user_session_required").Send(c)
		}
		return c.Next()
	}
}

func merchantRoleRequired() *problem.Problem {
	return problem.Forbidden("only an owner or admin of the organization reaches billing").WithCode("merchant_role_required")
}

func noMerchant() *problem.Problem {
	return problem.New(fiber.StatusNotFound, problem.TypeNoMerchant, "No merchant",
		"this organization does not bill through CTech Billing in this mode").WithCode("no_merchant")
}

// merchantAccessOf turns the resolved space into a MerchantAccess, or the
// refusal. Only an organization space held as owner or admin is a merchant
// (ADR 0028); personal and personal workspaces get the space-not-found body
// byte for byte, so the answer does not say what kind of space was named.
func merchantAccessOf(c fiber.Ctx) (MerchantAccess, *problem.Problem) {
	sp := GetSpace(c)
	if sp.IsZero() {
		// A chain without ResolveSpace in front: a wiring bug, refused.
		slog.Error("console: merchant chain ran without a resolved space", "path", c.Path())
		return MerchantAccess{}, problem.Internal("could not resolve organization").WithCode("organization_resolve_failed")
	}
	if sp.Personal() || sp.Kind() != space.KindOrganization {
		return MerchantAccess{}, spaceNotFound()
	}
	if !space.IsMerchantRole(sp.Kind(), sp.Role()) {
		return MerchantAccess{}, merchantRoleRequired()
	}
	return MerchantAccess{AccountOrganizationID: sp.OrganizationID(), Role: sp.Role(), Livemode: sp.Livemode()}, nil
}

// RequireMerchantRole gates a route on being an owner or admin of the selected
// organization, without needing a tenant (part 2's application route). It
// runs after ResolveSpace.
func RequireMerchantRole() fiber.Handler {
	return func(c fiber.Ctx) error {
		acc, p := merchantAccessOf(c)
		if p != nil {
			return p.Send(c)
		}
		c.Locals(MerchantAccessKey, acc)
		return c.Next()
	}
}

// ResolveMerchantTenant turns a resolved organization space into the tenant the
// request acts for (ADR 0028, replacing ResolveConsoleTenant). It runs after
// ResolveSpace, which has already checked membership before any table read,
// and sets the locals every console handler reads: OrganizationKey, TenantKey,
// and MerchantAccessKey.
//
// Order of answers: not an organization space → 404 space-not-found; not owner
// or admin → 403 merchant_role_required (no table read); no tenant in this mode
// → 404 no_merchant; a read failure → 500. The mode can be either: both mode
// partitions of a tenant belong to the same organization, so the header cannot
// widen access.
func ResolveMerchantTenant(src MerchantTenants) fiber.Handler {
	return func(c fiber.Ctx) error {
		acc, p := merchantAccessOf(c)
		if p != nil {
			return p.Send(c)
		}
		sub := ""
		if cl := GetClaims(c); cl != nil {
			sub = cl.Sub
		}
		org, err := LookupMerchantTenant(c.Context(), src, sub, acc.AccountOrganizationID, acc.Livemode)
		switch {
		case err == nil:
		case errors.Is(err, repositories.ErrNotFound):
			return noMerchant().Send(c)
		default:
			slog.Error("console: resolving the merchant tenant failed",
				"account_organization", acc.AccountOrganizationID, "mode", repositories.Mode(acc.Livemode), "error", err)
			return problem.Internal("could not resolve organization").WithCode("organization_resolve_failed").Send(c)
		}
		c.Locals(MerchantAccessKey, acc)
		c.Locals(OrganizationKey, org)
		c.Locals(TenantKey, Tenant{OrganizationID: org.ID, Livemode: org.Livemode})
		return c.Next()
	}
}

// LookupMerchantTenant reads the tenant of an account organization in one
// mode. The caller has already established that sub is an owner or admin of
// accountOrgID (or listed it from ctech-account's answer); sub is kept in the
// signature for the callers' sake and for logging, and decides nothing.
func LookupMerchantTenant(ctx context.Context, src MerchantTenants, sub, accountOrgID string, livemode bool) (*billing.Organization, error) {
	_ = sub
	return src.GetByAccountOrganization(ctx, accountOrgID, livemode)
}
