package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
)

// tenantRefundExcess finishes a refund that stopped halfway: the credit note stands and its
// excess row is still open, so the money never went back to the payer. It is the same step
// the console's "Devolver" takes, run by an operator where there is no console session.
// Idempotent: a row already refunding or refunded is reported and left alone.
func tenantRefundExcess(ctx context.Context, cfg *config.Config, mode, operator, excessID string) int {
	if mode != "live" || operator == "" {
		fmt.Fprintln(os.Stderr, "usage: byok -tenant-refund-excess=<ex id> -mode=live -operator=<name>")
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
	now := time.Now()
	e, err := op.Collector.RefundExcess(ctx, cfg.PortalOrganizationID, true, excessID,
		"operator:"+operator, "tenant-refund-"+now.UTC().Format("20060102T150405Z"), now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "refund:", err)
		return 1
	}
	fmt.Printf("%s is %s (%d centavos on %s)\n", e.ID, e.Status, e.Amount, e.InvoiceID)
	return 0
}
