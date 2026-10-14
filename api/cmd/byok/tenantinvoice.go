package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// tenantInvoice is the operator's way to issue a one-off invoice in tenant zero
// (spec P1 task 0: the live proof of the collection path) without minting an
// OAuth token: it calls the same service as POST /v1.0/invoices, audited with
// the operator's name, and prints the checkout link where the Pix is shown.
func tenantInvoice(ctx context.Context, cfg *config.Config, mode, operator, customer, description, reference string, cents int64) int {
	if mode != "live" || operator == "" || description == "" || reference == "" || cents <= 0 {
		fmt.Fprintln(os.Stderr, "usage: byok -tenant-invoice=<customer id> -mode=live -operator=<name> -invoice-amount-cents=<n> -invoice-reference=<ref> -invoice-description=<text>")
		return 2
	}
	if cfg.PortalOrganizationID == "" {
		fmt.Fprintln(os.Stderr, "PORTAL_ORGANIZATION_ID (tenant zero) is not set")
		return 2
	}
	oneOff, links, err := app.BuildOneOff(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		return 1
	}
	now := time.Now()
	inv, _, created, err := oneOff.Create(ctx, services.OneOffInput{
		OrganizationID: cfg.PortalOrganizationID, Livemode: true, CustomerID: customer, Description: description,
		Reference: reference, AmountCents: cents,
	}, "operator:"+operator, "tenant-invoice-"+now.UTC().Format("20060102T150405Z"), now)
	switch {
	case errors.Is(err, services.ErrOneOffConflict):
		fmt.Fprintln(os.Stderr, "that reference already names another invoice (another customer or amount)")
		return 1
	case err != nil:
		fmt.Fprintln(os.Stderr, "not issued:", err)
		return 1
	}
	verb := "already existed"
	if created {
		verb = "issued"
	}
	fmt.Printf("invoice %s %s: number=%d status=%s total=%d due=%s\n", inv.ID, verb, inv.Number, inv.Status, int64(inv.Total), inv.DueDate)
	if url := links.URL(inv.OrganizationID, inv.Livemode, inv.ID); url != "" {
		fmt.Println("checkout:", url)
	}
	return 0
}
