package services

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ByokLinker keeps a paid BYOK invoice and its statement line to one cash
// entry (spec § 8.4): the line of the payment that settled the invoice is
// linked to the invoice's posting, whichever arrives first. It implements
// part A's LineLinker.
type ByokLinker struct {
	collector *ByokCollector
	payments  *repositories.PaymentRepository
	bills     *repositories.BillRepository
	ledger    *repositories.LedgerRepository
	clock     func() time.Time
}

func NewByokLinker(c *ByokCollector, payments *repositories.PaymentRepository, bills *repositories.BillRepository,
	ledger *repositories.LedgerRepository) *ByokLinker {
	return &ByokLinker{collector: c, payments: payments, bills: bills, ledger: ledger, clock: time.Now}
}

// Link answers handled=true when the line must not post now: linked to the
// invoice's posting, or held back (no lock written) because its charge is not
// settled yet and the line is younger than a day, so the next sync looks again.
// handled=false posts the line as a bank line to classify: not ours, another
// organization's reference, an excess payment (its own payment id), or a line
// still unmatched after a day (an alert says so).
func (k *ByokLinker) Link(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, l repositories.BankLine) (bool, error) {
	if !l.Credit || l.ChargeRef == "" {
		return false, nil
	}
	ref, err := k.payments.ChargeRef(ctx, conn.Provider, conn.Livemode, l.ChargeRef)
	if errors.Is(err, repositories.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ref.AccountOrganizationID != conn.AccountOrganizationID {
		return false, nil
	}
	a, err := k.payments.GetAttempt(ctx, ref.TenantID, ref.Livemode, ref.InvoiceID, ref.AttemptNumber)
	if err != nil {
		return false, err
	}
	if a.Status == billing.AttemptPending {
		// A third wake-up (spec § 7.4): the re-query decides, never the line.
		if _, err := k.collector.Confirm(ctx, *ref, WakeStatement, k.clock()); err != nil {
			slog.WarnContext(ctx, "byok statement line: re-query failed", "invoice_id", ref.InvoiceID, "error", err)
		}
		if a, err = k.payments.GetAttempt(ctx, ref.TenantID, ref.Livemode, ref.InvoiceID, ref.AttemptNumber); err != nil {
			return false, err
		}
	}
	if a.Status == billing.AttemptSucceeded && (l.PaymentID == "" || l.PaymentID == a.PaymentID) {
		bill, err := k.bills.Get(ctx, sp, repositories.InvoiceBillID(sp, ref.InvoiceID))
		if err == nil && len(bill.TransactionIDs) > 1 {
			if _, err := k.ledger.LinkBankLine(ctx, sp, conn.LedgerAccountID, l, bill.TransactionIDs[1], k.clock()); err != nil {
				return false, err
			}
			return true, nil
		}
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
			return false, err
		}
	} else if a.Status == billing.AttemptSucceeded {
		return false, nil // another payment on the same charge: excess, the bank's own line
	}
	if !l.Date.Before(brcal.FromTime(k.clock()).AddDays(-1)) {
		return true, nil // wait: the next sync re-reads this window (3 days)
	}
	slog.ErrorContext(ctx, "byok statement line unmatched after a day; posted to classify", "invoice_id", ref.InvoiceID, "line_id", l.LineID)
	return false, nil
}

// WithClock pins "today" (tests).
func (k *ByokLinker) WithClock(c func() time.Time) *ByokLinker { k.clock = c; return k }
