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
	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// tenantCredit records, on a tenant-zero invoice, that its money was returned
// outside Billing: a credit note marked refunded externally. It moves no money.
// It calls the same repository as POST /console/invoices/:id/credit-notes, so
// the audit row and the over-crediting check are the console's own.
func tenantCredit(ctx context.Context, cfg *config.Config, mode, operator, invoiceID, reason, ref string, cents int64) int {
	if mode != "live" || operator == "" || reason == "" || cents <= 0 {
		fmt.Fprintln(os.Stderr, "usage: byok -tenant-credit=<invoice id> -mode=live -operator=<name> -credit-amount-cents=<n> -credit-reason=<text> [-credit-ref=<bank reference>]")
		return 2
	}
	if cfg.PortalOrganizationID == "" {
		fmt.Fprintln(os.Stderr, "PORTAL_ORGANIZATION_ID (tenant zero) is not set")
		return 2
	}
	credits, invoices, err := app.BuildCreditNotes(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		return 1
	}
	inv, err := invoices.Get(ctx, cfg.PortalOrganizationID, true, invoiceID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invoice:", err)
		return 1
	}
	cn := &billing.CreditNote{
		ID:                 id.NewWithPrefix(id.PrefixCreditNote),
		InvoiceID:          inv.ID,
		Amount:             billing.Cents(cents),
		Reason:             reason,
		RefundedExternally: true,
		ExternalRefundRef:  ref,
	}
	now := time.Now()
	err = credits.Issue(ctx, cn, inv, "operator:"+operator, "tenant-credit-"+now.UTC().Format("20060102T150405Z"), now)
	switch {
	case errors.Is(err, billing.ErrInvalidCreditNote):
		fmt.Fprintln(os.Stderr, "not recorded:", err)
		return 1
	case err != nil:
		fmt.Fprintln(os.Stderr, "failed:", err)
		return 1
	}
	fmt.Printf("credit note %s recorded on %s: %d centavos, refunded externally\n", cn.ID, inv.ID, cents)
	return 0
}
