package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// tenantMoveSubscription moves a tenant-zero subscription's single item to
// another price, through the same ChangePlan the console route uses (the audit
// row and the same-cycle and same-owner rules are its own). It exists for the
// catalogue moves a price cannot make for itself: a price is immutable and
// belongs to its product, so changing the product means changing the price.
//
// It keeps the item's quantity, refuses a subscription with several items (a
// move of one of many is a decision, not a default), and does nothing when the
// subscription is already on the price, so a second run is harmless.
func tenantMoveSubscription(ctx context.Context, cfg *config.Config, mode, operator, subscriptionID, toPrice string) int {
	if mode != "live" || operator == "" || toPrice == "" {
		fmt.Fprintln(os.Stderr, "usage: byok -tenant-move-subscription=<subscription id> -mode=live -operator=<name> -to-price=<price id>")
		return 2
	}
	if cfg.PortalOrganizationID == "" {
		fmt.Fprintln(os.Stderr, "PORTAL_ORGANIZATION_ID (tenant zero) is not set")
		return 2
	}
	subscriber, subs, err := app.BuildSubscriber(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		return 1
	}
	sub, err := subs.Get(ctx, cfg.PortalOrganizationID, true, subscriptionID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "subscription:", err)
		return 1
	}
	items, err := subs.ListItems(ctx, sub.OrganizationID, sub.Livemode, sub.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "items:", err)
		return 1
	}
	if len(items) != 1 {
		fmt.Fprintf(os.Stderr, "refused: %s has %d items; only a single-item subscription is moved here\n", sub.ID, len(items))
		return 1
	}
	if items[0].PriceID == toPrice {
		fmt.Printf("%s is already on %s; nothing to do\n", sub.ID, toPrice)
		return 0
	}
	if sub.Status == billing.SubscriptionCanceled {
		fmt.Fprintf(os.Stderr, "refused: %s is canceled\n", sub.ID)
		return 1
	}
	now := time.Now()
	inv, err := subscriber.ChangePlan(ctx, sub, services.ChangeInput{
		Items:     []services.SubscribeItem{{PriceID: toPrice, Quantity: items[0].Quantity}},
		Cause:     billing.CauseManual,
		Actor:     "operator:" + operator,
		RequestID: "tenant-move-" + now.UTC().Format("20060102T150405Z"),
	}, now)
	switch {
	case errors.Is(err, billing.ErrInvalidSubscriptionItem):
		fmt.Fprintln(os.Stderr, "not moved:", err)
		return 1
	case err != nil:
		fmt.Fprintln(os.Stderr, "failed:", err)
		return 1
	}
	invoiced := "no invoice"
	if inv != nil {
		invoiced = fmt.Sprintf("invoice %s for %d centavos", inv.ID, int64(inv.Total))
	}
	fmt.Printf("%s moved from %s to %s (%s)\n", sub.ID, items[0].PriceID, toPrice, invoiced)
	return 0
}
