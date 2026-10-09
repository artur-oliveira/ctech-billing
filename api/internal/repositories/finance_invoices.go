package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
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
// The credits on one bill never sum past it: the bill's `credited` moves by
// compare-and-set in the same write, so two notes racing on one bill cannot
// both commit, and the loser re-reads and is refused when the rest no longer
// covers it (finance.ErrInvalidTransaction). A conflict is retried like
// RecordInvoice's.
//
// The credit is not added to the bill's transaction list: "Desfazer pagamento"
// reverses the bill's last transaction, which must stay its settlement. An
// invoice never recorded here is ErrNotFound; a bill no longer paid (reopened in
// Finanças) is finance.ErrBillState. Both write nothing.
func (r *BillRepository) RecordInvoiceCredit(ctx context.Context, sp space.ResolvedSpace, invoiceID, creditNoteID string, amount billing.Cents, date brcal.Date, meta PostMeta, now time.Time) (bool, error) {
	if err := sp.Require(space.Read | space.Write | space.Settle); err != nil {
		return false, err
	}
	for attempt := 1; ; attempt++ {
		posted, again, err := r.recordInvoiceCreditOnce(ctx, sp, invoiceID, creditNoteID, amount, date, meta, now)
		if !again {
			return posted, err
		}
		if attempt == recordAttempts {
			return false, fmt.Errorf("%w: the bill for invoice %s kept changing while it was credited: %v", ErrConcurrentModification, invoiceID, err)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(attempt) * 25 * time.Millisecond):
		}
	}
}

// recordInvoiceCreditOnce is one attempt; again reports a write that lost a race
// (the bill's credited moved, or a conflict) and should be tried from a fresh
// read.
func (r *BillRepository) recordInvoiceCreditOnce(ctx context.Context, sp space.ResolvedSpace, invoiceID, creditNoteID string, amount billing.Cents, date brcal.Date, meta PostMeta, now time.Time) (posted, again bool, err error) {
	billID := InvoiceBillID(sp, invoiceID)
	b, err := r.getConsistent(ctx, sp, billID)
	if err != nil {
		return false, false, err
	}
	if b.Status != finance.BillPaid {
		return false, false, fmt.Errorf("%w: the invoice's bill is %s", finance.ErrBillState, b.Status)
	}
	if amount > b.Amount-b.Credited {
		return false, false, fmt.Errorf("%w: a credit of %s on a bill of %s with %s already credited",
			finance.ErrInvalidTransaction, amount, b.Amount, b.Credited)
	}
	tx, err := finance.CreditBill(b.Facts(), amount, date)
	if err != nil {
		return false, false, err
	}
	meta = billMeta(meta, *b)
	meta.Origin = "billing_credit_note"
	meta.Memo = "Nota de crédito · " + b.Description
	meta.txID = idempotentID(sp, "billing-credit-note", creditNoteID)
	plan, err := r.ledger.planPost(sp, tx, meta, now)
	if err != nil {
		return false, false, err
	}
	cond := "#status = :paid AND #cr = :before"
	if b.Credited == 0 {
		cond = "#status = :paid AND (attribute_not_exists(#cr) OR #cr = :before)"
	}
	sk := BillSK(billID)
	guard := r.bills.BuildRawUpdateTxItem(sp.PK(), &sk, "SET #cr = :after, updated_at = :now", cond,
		map[string]string{"#status": "status", "#cr": "credited"},
		map[string]types.AttributeValue{
			":paid":   str(string(finance.BillPaid)),
			":before": numberValue(int64(b.Credited)),
			":after":  numberValue(int64(b.Credited + amount)),
			":now":    r.stamp(now),
		})
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), guard)
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		switch {
		case len(codes) > 0 && codes[0] == codeConditionFailed:
			return false, false, nil // the header exists: this note was already recorded
		case len(codes) == len(items) && codes[len(items)-1] == codeConditionFailed, retryableCancel(err):
			return false, true, err // another credit (or an unsettle) moved the bill first
		}
		return false, false, classifyPostCancel(err, -1)
	}
	return true, false, nil
}

// getConsistent reads a bill with a strongly consistent read: a credit compares
// against the bill's credited total, and a stale one would only lose the race.
func (r *BillRepository) getConsistent(ctx context.Context, sp space.ResolvedSpace, billID string) (*finance.Bill, error) {
	out, err := r.bills.QueryRaw(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("pk = :pk AND sk = :sk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": str(sp.PK()), ":sk": str(BillSK(billID)),
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	if len(out.Items) == 0 {
		return nil, fmt.Errorf("%w: bill %s", ErrNotFound, billID)
	}
	it, err := Decode[billItem](out.Items[0])
	if err != nil {
		return nil, err
	}
	b, err := it.bill()
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// receivingAccount is where a paid invoice's cash goes in this space: the
// default receiving account (F8) if it is still an active asset account the
// person holds; otherwise, when the space holds exactly one active bank or cash
// account, that one (decided 2026-10-09). Zero or several candidates and no
// usable choice is ErrNoReceivingAccount — never a guess between two.
//
// There is no account kind in the ledger: every active, non-system asset account
// is a bank or cash account (cards are liabilities), so an investment account
// counts as one.
func (r *BillRepository) receivingAccount(ctx context.Context, sp space.ResolvedSpace) (string, error) {
	settings, err := r.ledger.GetSettings(ctx, sp)
	if err != nil {
		return "", err
	}
	if settings.DefaultReceivingAccountID != "" {
		acct, err := r.ledger.getAccount(ctx, sp, settings.DefaultReceivingAccountID)
		switch {
		case err == nil && usableCash(*acct):
			return acct.ID, nil
		case err != nil && !errors.Is(err, ErrNotFound):
			return "", err
		}
	}
	accounts, err := r.ledger.ListAccounts(ctx, sp)
	if err != nil {
		return "", err
	}
	only := ""
	for _, a := range accounts {
		if !usableCash(a) {
			continue
		}
		if only != "" {
			return "", fmt.Errorf("%w: no usable default and more than one account to choose from", ErrNoReceivingAccount)
		}
		only = a.ID
	}
	if only == "" {
		return "", ErrNoReceivingAccount
	}
	return only, nil
}

func usableCash(a AccountRow) bool {
	return a.Class == finance.ClassAsset && !a.System && !a.Archived
}
