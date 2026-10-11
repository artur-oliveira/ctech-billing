package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var ErrNotUnclassified = errors.New("finance: the line is already classified")

// BankLine is one synced statement line, as the ledger posts it.
type BankLine struct {
	Provider, LineID     string
	Date                 brcal.Date
	Credit               bool
	Amount               billing.Cents
	Memo                 string
	ChargeRef, PaymentID string
}

func BankLineRef(accountID, lineID string) string { return "byok:" + accountID + ":" + lineID }

// bankLockKey is the FITID# lock of a line (spec § 8.2): "B:{provider}:{id}".
func bankLockKey(l BankLine) string { return "B:" + l.Provider + ":" + l.LineID }

// bankLockKeyByContent keys a line whose id the bank reused for another
// amount or date, like claim() does for a reused FITID.
func bankLockKeyByContent(l BankLine) string {
	return bankLockKey(l) + ":" + l.Date.String() + ":" + strconv.FormatInt(int64(l.Amount), 10)
}

type bankLockItem struct {
	keys
	TxID   string `dynamodbav:"tx_id"`
	Date   string `dynamodbav:"date"`
	Amount int64  `dynamodbav:"amount"`
	Credit bool   `dynamodbav:"credit,omitempty"`
	Linked bool   `dynamodbav:"linked,omitempty"` // part B: points at an invoice's posting
}

func (r *LedgerRepository) EnsureByokAccount(ctx context.Context, sp space.ResolvedSpace, id, name, provider string, now time.Time) (bool, error) {
	if err := r.EnsureReady(ctx, sp, now); err != nil {
		return false, err
	}
	for _, c := range finance.UnclassifiedCategories() {
		if _, err := r.ensureCategory(ctx, sp, c, now); err != nil {
			return false, err
		}
	}
	err := r.CreateAccount(ctx, sp, finance.LedgerAccount{ID: id, Name: name, Class: finance.ClassAsset,
		Source: finance.SourceBYOK, Provider: provider}, now)
	if errors.Is(err, ErrAccountExists) {
		return false, nil // reconnecting the same bank account resumes on it (spec § 3.6 step 5)
	}
	// ErrAccountExists is new: CreateAccount (ledger.go:306) today wraps the
	// duplicate as finance.ErrInvalidAccount; this task makes it
	// fmt.Errorf("%w: %w", ErrAccountExists, finance.ErrInvalidAccount) so both
	// callers keep working (the route still answers invalid_account).
	return err == nil, err
}

func (r *LedgerRepository) lockSK(accountID, key string) string { return ImportLockSK(accountID, key) }

func (r *LedgerRepository) readLock(ctx context.Context, sp space.ResolvedSpace, accountID, key string) (*bankLockItem, error) {
	item, err := r.imports.GetItem(ctx, sp.PK(), r.lockSK(accountID, key))
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[bankLockItem](item)
}

// PostBankLine posts the line once: the transaction and its lock row in one
// TransactWriteItems, the lock conditional on absence. posted=false means the
// line was already there (a replay of the same window).
func (r *LedgerRepository) PostBankLine(ctx context.Context, sp space.ResolvedSpace, accountID string, l BankLine, meta PostMeta, now time.Time) (string, bool, error) {
	if err := sp.Require(space.Write | space.Import); err != nil {
		return "", false, err
	}
	key := bankLockKey(l)
	held, err := r.readLock(ctx, sp, accountID, key)
	if err != nil {
		return "", false, err
	}
	if held != nil {
		if held.Date == l.Date.String() && held.Amount == int64(l.Amount) {
			return held.TxID, false, nil
		}
		key = bankLockKeyByContent(l)
		if again, err := r.readLock(ctx, sp, accountID, key); err != nil || again != nil {
			if again != nil {
				return again.TxID, false, nil
			}
			return "", false, err
		}
	}
	cat := finance.UnclassifiedOutID
	if l.Credit {
		cat = finance.UnclassifiedInID
	}
	tx, err := finance.BankLine(accountID, cat, l.Credit, l.Amount, l.Date)
	if err != nil {
		return "", false, err
	}
	meta.Origin, meta.Ref, meta.Memo = "bank_line", BankLineRef(accountID, l.LineID), l.Memo
	plan, err := r.planPost(sp, tx, meta, now)
	if err != nil {
		return "", false, err
	}
	lock, err := Encode(bankLockItem{keys: newKeys(sp.PK(), r.lockSK(accountID, key), RetentionImportLock, now),
		TxID: plan.TxID, Date: l.Date.String(), Amount: int64(l.Amount), Credit: l.Credit})
	if err != nil {
		return "", false, err
	}
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), r.imports.BuildPutTxItemIfAbsent(lock))
	if err := r.txs.TransactWrite(ctx, items); err != nil {
		if codes := cancellationCodes(err); len(codes) == len(items) && codes[len(items)-1] == codeConditionFailed {
			return "", false, nil // a concurrent sync posted it
		}
		return "", false, classifyPostCancel(err, plan.MarkerIdx)
	}
	return plan.TxID, true, nil
}

// BankLineLock reports whether a line was posted or linked, and to which tx.
func (r *LedgerRepository) BankLineLock(ctx context.Context, sp space.ResolvedSpace, accountID, provider, lineID string) (string, bool, error) {
	if err := sp.Require(space.Read); err != nil {
		return "", false, err
	}
	held, err := r.readLock(ctx, sp, accountID, bankLockKey(BankLine{Provider: provider, LineID: lineID}))
	if err != nil || held == nil {
		return "", false, err
	}
	return held.TxID, true, nil
}

// LinkBankLine claims the line's lock for an existing transaction (the paid
// invoice's posting, spec § 8.4) and posts nothing.
func (r *LedgerRepository) LinkBankLine(ctx context.Context, sp space.ResolvedSpace, accountID string, l BankLine, txID string, now time.Time) (bool, error) {
	if err := sp.Require(space.Write | space.Import); err != nil {
		return false, err
	}
	lock, err := Encode(bankLockItem{keys: newKeys(sp.PK(), r.lockSK(accountID, bankLockKey(l)), RetentionImportLock, now),
		TxID: txID, Date: l.Date.String(), Amount: int64(l.Amount), Credit: l.Credit, Linked: true})
	if err != nil {
		return false, err
	}
	err = r.imports.TransactWrite(ctx, txItems(r.imports.BuildPutTxItemIfAbsent(lock)))
	if IsConditionFailed(err) {
		return false, nil
	}
	return err == nil, conflictErr(err)
}

func (r *LedgerRepository) SetProviderBalance(ctx context.Context, sp space.ResolvedSpace, accountID string, b ProviderBalance) error {
	if err := sp.Require(space.Write | space.Import); err != nil {
		return err
	}
	sk := LedgerAccountSK(accountID)
	return r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET provider_balance = :a, provider_blocked = :b, provider_balance_at = :d, synced_at = :s",
		"attribute_exists(pk) AND #src = :byok", map[string]string{"#src": "source"},
		map[string]types.AttributeValue{
			":a": numberValue(int64(b.Available)), ":b": numberValue(int64(b.Blocked)),
			":d": str(b.At.String()), ":s": str(b.SyncedAt.UTC().Format(time.RFC3339)), ":byok": str(finance.SourceBYOK),
		})))
}

// PostProviderOpening is PostOpeningBalance for a synced account: the amount
// is the bank's balance on the day before the first synced day (spec § 8.2).
func (r *LedgerRepository) PostProviderOpening(ctx context.Context, sp space.ResolvedSpace, accountID string, amount billing.Cents, date brcal.Date, meta PostMeta, now time.Time) (string, error) {
	if err := sp.Require(space.Write | space.Import); err != nil {
		return "", err
	}
	return r.postOpening(ctx, sp, accountID, amount, date, meta, now)
}

type UnclassifiedLine struct {
	TxID   string
	Date   brcal.Date
	Amount billing.Cents
	Credit bool
	Memo   string
}

// Unclassified lists the account's lines still on the two system categories:
// one key-condition read per category entry partition, filtered by the line
// ref prefix (several BYOK accounts share the two categories).
func (r *LedgerRepository) Unclassified(ctx context.Context, sp space.ResolvedSpace, accountID string) ([]UnclassifiedLine, error) {
	prefix := "byok:" + accountID + ":"
	var out []UnclassifiedLine
	for _, cat := range []string{finance.UnclassifiedInID, finance.UnclassifiedOutID} {
		entries, err := r.EntriesFrom(ctx, sp, cat, brcal.Date{})
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Reversed || e.Reversal || !strings.HasPrefix(e.Ref, prefix) {
				continue
			}
			amt := -e.Amount // income leg is a credit (negative); show the cash amount positive
			credit := cat == finance.UnclassifiedInID
			if !credit {
				amt = e.Amount
			}
			out = append(out, UnclassifiedLine{TxID: e.TxID, Date: e.Date, Amount: amt, Credit: credit, Memo: e.Memo})
		}
	}
	return out, nil
}

type Classification struct{ CategoryID, BillID, TransferAccountID string }

// Classify replaces an unclassified bank line, in one TransactWriteItems: the
// line's reversal plus its replacement (category, bill settlement or transfer).
// The bank account's balance is unchanged by construction.
func (r *LedgerRepository) Classify(ctx context.Context, bills *BillRepository, sp space.ResolvedSpace, txID string, c Classification, meta PostMeta, now time.Time) (string, error) {
	if err := sp.Require(space.Import | space.Write); err != nil {
		return "", err
	}
	orig, err := r.GetTransaction(ctx, sp, txID)
	if err != nil {
		return "", err
	}
	// A line already classified is reversed: planReverse's REVERSAL# marker
	// refuses it at write time (mapped to ErrNotUnclassified below).
	if orig.Tx.Kind != finance.KindBankLine {
		return "", ErrNotUnclassified
	}
	var cash, cat finance.Leg
	for _, l := range orig.Tx.Legs {
		if l.AccountID == finance.UnclassifiedInID || l.AccountID == finance.UnclassifiedOutID {
			cat = l
		} else {
			cash = l
		}
	}
	if cat.AccountID == "" {
		return "", ErrNotUnclassified
	}
	credit := cash.Amount > 0
	amount := cash.Amount
	if !credit {
		amount = -amount
	}
	rev, err := r.planReverse(ctx, sp, txID, orig.Tx.Date, meta, now)
	if err != nil {
		return "", err
	}
	var next ledgerPlan
	var extra []types.TransactWriteItem
	switch {
	case c.CategoryID != "":
		acct, err := r.getAccount(ctx, sp, c.CategoryID)
		if err != nil || (acct.Class != finance.ClassIncome && acct.Class != finance.ClassExpense) || acct.Archived {
			return "", fmt.Errorf("%w: an active income or expense category", ErrUnknownAccount)
		}
		tx, err := finance.BankLine(cash.AccountID, c.CategoryID, credit, amount, orig.Tx.Date)
		if err != nil {
			return "", err
		}
		meta.Origin, meta.Ref, meta.Memo = "bank_line", orig.Ref, orig.Memo
		if next, err = r.planPost(sp, tx, meta, now); err != nil {
			return "", err
		}
	case c.TransferAccountID != "":
		if err := r.activeManualCash(ctx, sp, c.TransferAccountID); err != nil {
			return "", err
		}
		from, to := c.TransferAccountID, cash.AccountID
		if !credit {
			from, to = cash.AccountID, c.TransferAccountID
		}
		tx, err := finance.Transfer(from, to, amount, orig.Tx.Date)
		if err != nil {
			return "", err
		}
		meta.Origin, meta.Ref = "bank_line", orig.Ref
		if next, err = r.planPost(sp, tx, meta, now); err != nil {
			return "", err
		}
	case c.BillID != "":
		p, err := bills.planSettleInto(ctx, sp, c.BillID, amount, cash.AccountID, credit, orig.Tx.Date, meta, now)
		if err != nil {
			return "", err
		}
		next = ledgerPlan{TxID: p.txID}
		extra = p.items
	default:
		return "", fmt.Errorf("%w: choose a category, a bill or a transfer", finance.ErrInvalidTransaction)
	}
	items := mergeAdds(append(append(append([]types.TransactWriteItem(nil), rev.Items...), next.Items...), extra...))
	if err := r.txs.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			return "", ErrNotUnclassified // a concurrent classification reversed it first
		}
		return "", classifyPostCancel(err, -1)
	}
	return next.TxID, nil
}
