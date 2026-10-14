package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/space"
)

const (
	railBYOK   = "byok"
	railWallet = "wallet"
	provider   = "inter"
)

// tenantRail is the operator action of spec § 12.6: tenant zero's collection
// rail is never moved by a console click (PUT /console/collection answers
// tenant_zero_rail), so the move is this audited command, run by a named
// operator in live mode. "byok" needs the organization's collection-capable
// connection and registers the payment notice; "wallet" is the way back.
// Invoices already issued keep the charge they have; only later ones follow
// the new rail.
func tenantRail(ctx context.Context, cfg *config.Config, rail, mode, operator string, fine, interest int, dryRun bool) int {
	if rail != railBYOK && rail != railWallet {
		fmt.Fprintln(os.Stderr, "usage: byok -tenant-rail=byok|wallet -mode=live -operator=<name> [-dry-run]")
		return 2
	}
	if mode != "live" || operator == "" {
		fmt.Fprintln(os.Stderr, "-tenant-rail needs -mode=live (no default on purpose) and -operator=<name>")
		return 2
	}
	if cfg.PortalOrganizationID == "" {
		fmt.Fprintln(os.Stderr, "PORTAL_ORGANIZATION_ID (tenant zero) is not set")
		return 2
	}
	setup, orgs, err := app.BuildByokOperator(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		return 1
	}
	tenant, err := orgs.Get(ctx, cfg.PortalOrganizationID, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reading tenant zero:", err)
		return 1
	}
	settings, err := collectionSettings(tenant.Collection, fine, interest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "late fees:", err)
		return 2
	}
	fmt.Printf("tenant zero %s: rail=%s provider=%q status=%s fine=%dbp interest=%dbp/month\n", tenant.ID, tenant.Rail(), tenant.PayoutProvider,
		tenant.PayoutStatus, tenant.Collection.FineBasisPoints, tenant.Collection.MonthlyInterestBasisPoints)
	if dryRun {
		fmt.Printf("dry run: would move to %s with fine=%dbp interest=%dbp/month\n", rail, settings.FineBasisPoints, settings.MonthlyInterestBasisPoints)
		return 0
	}
	now := time.Now()
	actor, request := "operator:"+operator, "tenant-rail-"+now.UTC().Format("20060102T150405Z")
	if rail == railBYOK {
		var sp space.ResolvedSpace
		if sp, err = space.ForJob(tenant.AccountOrganizationID, true); err == nil {
			err = setup.OperatorUseForCollection(ctx, sp, tenant, provider, settings, actor, request, now)
		}
	} else {
		err = setup.OperatorLeaveCollection(ctx, tenant, actor, request, now)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "not moved:", err)
		return 1
	}
	after, err := orgs.Get(ctx, cfg.PortalOrganizationID, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "moved, but reading it back failed:", err)
		return 1
	}
	fmt.Printf("now: rail=%s provider=%q status=%s fine=%dbp interest=%dbp/month (%s, request %s)\n", after.Rail(), after.PayoutProvider, after.PayoutStatus,
		after.Collection.FineBasisPoints, after.Collection.MonthlyInterestBasisPoints, actor, request)
	if after.Rail() != billing.PayoutRail(rail) {
		fmt.Fprintln(os.Stderr, "the rail did not move")
		return 1
	}
	return 0
}

// collectionSettings is the tenant's late fees with the flags applied: a flag
// below zero leaves that value as it is (zero is a value: no fine). Basis
// points, like the console: 200 is 2 %. Invoices already issued keep the fees
// they were finalized with; only later ones follow.
func collectionSettings(current billing.CollectionSettings, fine, interest int) (billing.CollectionSettings, error) {
	if fine >= 0 {
		current.FineBasisPoints = fine
	}
	if interest >= 0 {
		current.MonthlyInterestBasisPoints = interest
	}
	return current, current.Validate()
}
