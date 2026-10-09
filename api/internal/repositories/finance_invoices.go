package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ErrNoReceivingAccount is a space that has not chosen where its receipts land
// (F8), or whose choice is no longer an active asset account. A paid invoice is
// not recorded there: the posting rule has no account to put the cash in, and a
// guess would put real money in the wrong place.
var ErrNoReceivingAccount = errors.New("finance: the space has no default receiving account")

// InvoiceFact is a paid billing invoice as one space records it (spec § 3.8):
// a receivable for the organization that issued it, a payable for the one who
// paid it, both recognised at Competence and settled on Paid.
type InvoiceFact struct {
	InvoiceID   string
	Direction   finance.Direction
	Amount      billing.Cents
	Competence  brcal.Date // the invoice's period start
	Due         brcal.Date // zero means the paid day
	Paid        brcal.Date // paid_at as a civil day
	Description string
}

// InvoiceBillID is the id of the bill an invoice makes in a space. Derived from
// (space, invoice), so every settlement of the same invoice — the webhook, its
// retries, the reconciler — names the same row, and only the first is written.
func InvoiceBillID(sp space.ResolvedSpace, invoiceID string) string {
	return idempotentID(sp, "billing-invoice", invoiceID)
}

// recordAttempts bounds the retries of a write cancelled by a conflict: another
// settlement of the same invoice is writing the same rows, and the next look
// finds its bill.
const recordAttempts = 3

// RecordInvoice records a paid invoice in a space: the bill created and settled
// at once — its recognition at the competence date and its settlement on the
// paid day — in ONE TransactWriteItems with the ledger's entries, the bill put
// conditional on its absence. created is false when the invoice was already
// recorded, which a replay finds with one read and no write.
//
// The space's default receiving account is read first, before anything is
// written: with none, ErrNoReceivingAccount comes back and the space is not even
// created, so paying an invoice never opens a finance space for somebody.
func (r *BillRepository) RecordInvoice(ctx context.Context, sp space.ResolvedSpace, f InvoiceFact, meta PostMeta, now time.Time) (finance.Bill, bool, error) {
	if err := sp.Require(space.Read | space.Write | space.Settle); err != nil {
		return finance.Bill{}, false, err
	}
	billID := InvoiceBillID(sp, f.InvoiceID)
	for attempt := 1; ; attempt++ {
		b, created, err := r.recordInvoiceOnce(ctx, sp, billID, f, meta, now)
		if err == nil || !retryableCancel(err) || attempt == recordAttempts {
			return b, created, err
		}
		select {
		case <-ctx.Done():
			return finance.Bill{}, false, ctx.Err()
		case <-time.After(time.Duration(attempt) * 25 * time.Millisecond):
		}
	}
}

func (r *BillRepository) recordInvoiceOnce(ctx context.Context, sp space.ResolvedSpace, billID string, f InvoiceFact, meta PostMeta, now time.Time) (finance.Bill, bool, error) {
	if existing, err := r.get(ctx, sp, billID); err == nil {
		return *existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return finance.Bill{}, false, err
	}
	if f.Paid.IsZero() {
		return finance.Bill{}, false, fmt.Errorf("%w: a paid invoice needs its paid day", finance.ErrInvalidBill)
	}
	accountID, err := r.receivingAccount(ctx, sp)
	if err != nil {
		return finance.Bill{}, false, err
	}
	if err := r.ledger.EnsureReady(ctx, sp, now); err != nil {
		return finance.Bill{}, false, err
	}
	categoryID, err := r.ledger.ensureCategory(ctx, sp, finance.BillingCategory(f.Direction), now)
	if err != nil {
		return finance.Bill{}, false, err
	}
	due := f.Due
	if due.IsZero() {
		due = f.Paid
	}
	b := finance.Bill{
		ID: billID, Direction: f.Direction, Amount: f.Amount, AccountID: accountID, CategoryID: categoryID,
		Description: f.Description, Competence: f.Competence, Due: due, Status: finance.BillForecast,
		Origin: finance.OriginBillingInvoice, OriginRef: f.InvoiceID,
	}
	if err := b.Validate(); err != nil {
		return finance.Bill{}, false, err
	}
	sys, _ := finance.DefaultSystemAccounts()
	rec, err := finance.RecognizeBill(sys, b.Facts(), b.Competence)
	if err != nil {
		return finance.Bill{}, false, err
	}
	settle, err := finance.SettleBill(sys, b.Facts(), b.Amount, "", f.Paid)
	if err != nil {
		return finance.Bill{}, false, err
	}
	meta.Origin = string(finance.OriginBillingInvoice)
	recPlan, err := r.ledger.planPost(sp, rec, billMeta(meta, b), now)
	if err != nil {
		return finance.Bill{}, false, err
	}
	setPlan, err := r.ledger.planPost(sp, settle, billMeta(meta, b), now)
	if err != nil {
		return finance.Bill{}, false, err
	}
	b.Status, b.PaidDate, b.TransactionIDs = finance.BillPaid, f.Paid, []string{recPlan.TxID, setPlan.TxID}
	row, err := Encode(newBillItem(sp, b, now))
	if err != nil {
		return finance.Bill{}, false, err
	}
	// Recognition and settlement both ADD to receivables (or payables) and, in
	// the same month, to its summary: folded into one update each.
	items := mergeAdds(append(append([]types.TransactWriteItem(nil), recPlan.Items...), setPlan.Items...))
	billIdx := len(items)
	items = append(items, r.bills.BuildPutTxItemIfAbsent(row))
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		if codes := cancellationCodes(err); billIdx < len(codes) && codes[billIdx] == codeConditionFailed {
			// Another settlement of the same invoice wrote it first. The refused
			// put is the proof; the re-read may still miss it (eventually
			// consistent), and then the id is enough to say so.
			if existing, gerr := r.get(ctx, sp, billID); gerr == nil {
				return *existing, false, nil
			}
			return finance.Bill{ID: billID}, false, nil
		}
		if retryableCancel(err) {
			return finance.Bill{}, false, err
		}
		return finance.Bill{}, false, classifyPostCancel(err, -1)
	}
	return b, true, nil
}

// RecordInvoiceCredit posts a billing credit note against the bill a paid
// invoice made in this space (spec § 3.8): finance.CreditBill for the note's
// amount, dated the note's day, as one transaction whose id derives from
// (space, note) — a second post finds its header and posts nothing (false, nil).
//
// The credit is not added to the bill's transaction list: "Desfazer pagamento"
// reverses the bill's last transaction, which must stay its settlement. An
// invoice never recorded here is ErrNotFound; a bill no longer paid (reopened in
// Finanças) is finance.ErrBillState. Both write nothing.
func (r *BillRepository) RecordInvoiceCredit(ctx context.Context, sp space.ResolvedSpace, invoiceID, creditNoteID string, amount billing.Cents, date brcal.Date, meta PostMeta, now time.Time) (bool, error) {
	if err := sp.Require(space.Read | space.Write | space.Settle); err != nil {
		return false, err
	}
	b, err := r.get(ctx, sp, InvoiceBillID(sp, invoiceID))
	if err != nil {
		return false, err
	}
	if b.Status != finance.BillPaid {
		return false, fmt.Errorf("%w: the invoice's bill is %s", finance.ErrBillState, b.Status)
	}
	tx, err := finance.CreditBill(b.Facts(), amount, date)
	if err != nil {
		return false, err
	}
	meta = billMeta(meta, *b)
	meta.Origin = "billing_credit_note"
	meta.Memo = "Nota de crédito · " + b.Description
	meta.txID = idempotentID(sp, "billing-credit-note", creditNoteID)
	plan, err := r.ledger.planPost(sp, tx, meta, now)
	if err != nil {
		return false, err
	}
	if err := r.bills.TransactWrite(ctx, plan.Items); err != nil {
		if codes := cancellationCodes(err); len(codes) > 0 && codes[0] == codeConditionFailed {
			return false, nil // the header exists: this note was already recorded
		}
		return false, classifyPostCancel(err, -1)
	}
	return true, nil
}

// receivingAccount is the space's default receiving account, if it is still an
// active asset account the person holds.
func (r *BillRepository) receivingAccount(ctx context.Context, sp space.ResolvedSpace) (string, error) {
	settings, err := r.ledger.GetSettings(ctx, sp)
	if err != nil {
		return "", err
	}
	if settings.DefaultReceivingAccountID == "" {
		return "", ErrNoReceivingAccount
	}
	acct, err := r.ledger.getAccount(ctx, sp, settings.DefaultReceivingAccountID)
	if errors.Is(err, ErrNotFound) {
		return "", ErrNoReceivingAccount
	}
	if err != nil {
		return "", err
	}
	if acct.Class != finance.ClassAsset || acct.System || acct.Archived {
		return "", fmt.Errorf("%w: %s is not an active asset account", ErrNoReceivingAccount, acct.ID)
	}
	return acct.ID, nil
}
