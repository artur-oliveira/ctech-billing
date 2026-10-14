package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	// Payer is who sent a received payment, when the bank's line says so.
	Payer *billing.Payer
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
	pending, err := Encode(unclassifiedItem{keys: newKeys(sp.PK(), UnclassifiedSK(accountID, plan.TxID), RetentionPermanent, now),
		TxID: plan.TxID, Date: l.Date.String(), Amount: int64(l.Amount), Credit: l.Credit, Memo: l.Memo})
	if err != nil {
		return "", false, err
	}
	// The lock stays last: a concurrent sync is recognised by its condition.
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), r.txs.BuildPutTxItem(pending), r.imports.BuildPutTxItemIfAbsent(lock))
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

// unclassifiedItem is a bank line still on "A classificar": a sparse row
// written with the line and deleted by its classification, so the list (and
// the count on the accounts screen) reads only what is left, never the
// history (final review I5).
type unclassifiedItem struct {
	keys
	TxID   string `dynamodbav:"tx_id"`
	Date   string `dynamodbav:"date"`
	Amount int64  `dynamodbav:"amount"`
	Credit bool   `dynamodbav:"credit,omitempty"`
	Memo   string `dynamodbav:"memo,omitempty"`
}

// UnclassifiedSK keys a pending line under its account.
func UnclassifiedSK(accountID, txID string) string { return "UNCLASSIFIED#" + accountID + "#" + txID }

// Unclassified lists the account's lines still on the two system categories:
// one key-condition query of its pending rows.
func (r *LedgerRepository) Unclassified(ctx context.Context, sp space.ResolvedSpace, accountID string) ([]UnclassifiedLine, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	var out []UnclassifiedLine
	var start map[string]types.AttributeValue
	for {
		res, err := r.txs.Query(ctx, QueryOpts{PK: sp.PK(), SKPrefix: "UNCLASSIFIED#" + accountID + "#", ExclusiveStartKey: start, Limit: 500})
		if err != nil {
			return nil, err
		}
		rows, err := DecodeItems[unclassifiedItem](res.Items)
		if err != nil {
			return nil, err
		}
		for _, it := range rows {
			d, _ := brcal.Parse(it.Date)
			out = append(out, UnclassifiedLine{TxID: it.TxID, Date: d, Amount: billing.Cents(it.Amount), Credit: it.Credit, Memo: it.Memo})
		}
		if res.LastEvaluatedKey == nil {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
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
	// The pending row goes in the same transaction, conditional on existing:
	// of two classifications of one line, exactly one commits.
	sk := UnclassifiedSK(cash.AccountID, txID)
	pending := types.TransactWriteItem{Delete: &types.Delete{
		TableName:           aws.String(r.txs.TableName),
		Key:                 map[string]types.AttributeValue{"pk": str(sp.PK()), "sk": str(sk)},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	}}
	items := mergeAdds(append(append(append([]types.TransactWriteItem{pending}, rev.Items...), next.Items...), extra...))
	if err := r.txs.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			return "", ErrNotUnclassified // a concurrent classification reversed it first
		}
		return "", classifyPostCancel(err, -1)
	}
	return next.TxID, nil
}

// SetSyncState marks a BYOK account's sync ("active" on reconnect,
// "disconnected" on disconnect). It writes the account row only.
func (r *LedgerRepository) SetSyncState(ctx context.Context, sp space.ResolvedSpace, accountID, state string) error {
	if err := sp.Require(space.Configure); err != nil {
		return err
	}
	sk := LedgerAccountSK(accountID)
	return r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET sync_state = :st", "attribute_exists(pk) AND #src = :byok", map[string]string{"#src": "source"},
		map[string]types.AttributeValue{":st": str(state), ":byok": str(finance.SourceBYOK)})))
}

// refuseSyncedLegs keeps a bank-synced account equal to its bank: a person may
// not undo a fact that moved its cash (a classified line's transfer or bill
// settlement, the bank's opening). The original bank line is already reversed
// and its FITID# lock kept, so the sync would never post it again and the
// difference would be permanent (BYOK spec § 8.1, final review C1). One read
// per distinct account, only on a manual undo.
func (r *LedgerRepository) refuseSyncedLegs(ctx context.Context, sp space.ResolvedSpace, tx finance.Transaction) error {
	seen := map[string]bool{}
	for _, l := range tx.Legs {
		if seen[l.AccountID] {
			continue
		}
		seen[l.AccountID] = true
		acct, err := r.getAccount(ctx, sp, l.AccountID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if acct.Source == finance.SourceBYOK {
			return fmt.Errorf("%w: %s", ErrAccountIsSynced, l.AccountID)
		}
	}
	return nil
}

// OpeningDate is the date of the account's opening balance, if it has one.
func (r *LedgerRepository) OpeningDate(ctx context.Context, sp space.ResolvedSpace, accountID string) (brcal.Date, bool, error) {
	if err := sp.Require(space.Read); err != nil {
		return brcal.Date{}, false, err
	}
	txID, err := r.openingTx(ctx, sp, accountID)
	if err != nil || txID == "" {
		return brcal.Date{}, false, err
	}
	tx, err := r.GetTransaction(ctx, sp, txID)
	if err != nil {
		return brcal.Date{}, false, err
	}
	return tx.Tx.Date, true, nil
}
