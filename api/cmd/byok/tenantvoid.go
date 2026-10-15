package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// tenantVoid voids an open tenant-zero invoice the way the console's "Anular"
// does: the open Pix charge at the bank is removed first (an invoice paid in the
// meantime wins over the void), then the invoice goes void. Audited with the
// operator's name. It is how an operator cancels what a service client left
// open, where there is no console session to do it from.
func tenantVoid(ctx context.Context, cfg *config.Config, mode, operator, invoiceID string) int {
	if mode != "live" || operator == "" {
		fmt.Fprintln(os.Stderr, "usage: byok -tenant-void-invoice=<invoice id> -mode=live -operator=<name>")
		return 2
	}
	if cfg.PortalOrganizationID == "" {
		fmt.Fprintln(os.Stderr, "PORTAL_ORGANIZATION_ID (tenant zero) is not set")
		return 2
	}
	op, err := app.BuildByokOperator(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		return 1
	}
	inv, err := op.Invoices.Get(ctx, cfg.PortalOrganizationID, true, invoiceID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invoice:", err)
		return 1
	}
	switch inv.Status {
	case billing.InvoiceVoid:
		fmt.Printf("%s is already void; nothing to do\n", inv.ID)
		return 0
	case billing.InvoiceOpen:
	default:
		fmt.Fprintf(os.Stderr, "refused: %s is %s; only an open invoice is voided here\n", inv.ID, inv.Status)
		return 1
	}
	now := time.Now()
	paid, err := op.Collector.Voided(ctx, inv, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "removing the charge:", err)
		return 1
	}
	if paid {
		fmt.Printf("%s was paid meanwhile; it stays paid and is not voided\n", inv.ID)
		return 0
	}
	if _, err := op.Invoices.Transition(ctx, inv, billing.InvoiceVoid, billing.CauseManual,
		"operator:"+operator, "tenant-void-"+now.UTC().Format("20060102T150405Z"), now); err != nil {
		fmt.Fprintln(os.Stderr, "void:", err)
		return 1
	}
	fmt.Printf("%s voided\n", inv.ID)
	return 0
}
