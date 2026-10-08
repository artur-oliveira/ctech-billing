package finance

import (
	"errors"
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// AccountClass is what a ledger account is, in accounting terms. Users never see
// the word: their checking and cash accounts are assets, their cards are
// liabilities, their categories are income and expense, and payables,
// receivables and the opening balance are system accounts.
type AccountClass string

const (
	ClassAsset     AccountClass = "asset"
	ClassLiability AccountClass = "liability"
	ClassIncome    AccountClass = "income"
	ClassExpense   AccountClass = "expense"
	ClassEquity    AccountClass = "equity"
)

// DREGroup is the line of the income statement a category rolls up to. The set
// is fixed by the system (spec § 3.3): users name categories, never groups, so
// no space can build a DRE that does not add up.
type DREGroup string

const (
	GroupGrossRevenue      DREGroup = "gross_revenue"
	GroupDeductions        DREGroup = "deductions"
	GroupCosts             DREGroup = "costs"
	GroupOperatingExpenses DREGroup = "operating_expenses"
	GroupFinancialResult   DREGroup = "financial_result"
	GroupOther             DREGroup = "other"
)

// AllowsClass reports whether a category of class c may sit under g.
func (g DREGroup) AllowsClass(c AccountClass) bool {
	switch g {
	case GroupGrossRevenue:
		return c == ClassIncome
	case GroupDeductions, GroupCosts, GroupOperatingExpenses:
		return c == ClassExpense
	case GroupFinancialResult, GroupOther:
		return c == ClassIncome || c == ClassExpense
	}
	return false
}

// TxKind names the fact a transaction records. It is for tracing and display;
// the accounting is entirely in the legs.
type TxKind string

const (
	KindRecognition      TxKind = "recognition"
	KindSettlement       TxKind = "settlement"
	KindTransfer         TxKind = "transfer"
	KindCardPurchase     TxKind = "card_purchase"
	KindStatementPayment TxKind = "statement_payment"
	KindOpeningBalance   TxKind = "opening_balance"
	KindReversal         TxKind = "reversal"
	// KindAdjustment changes what a still-open bill recognised (an edit or a
	// cancellation) as one net transaction.
	KindAdjustment TxKind = "adjustment"
)

// Leg is one side of a transaction on one account. Amount is signed: **positive
// is a debit, negative a credit.** One sign convention for every class keeps the
// invariant a single sum; what "debit" means for an asset versus an expense is
// the report's business, not the leg's.
type Leg struct {
	AccountID string
	Amount    billing.Cents
}

// Transaction is one business fact as double entry, after Fowler's Accounting
// Transaction: two or more legs that sum to zero, all on one date. It is
// immutable once built — the fields are exported for storage, and nothing in
// this package or above it may change one after NewTransaction returns. A wrong
// transaction is answered with Reverse, never with an edit.
type Transaction struct {
	Kind    TxKind
	Date    brcal.Date
	Legs    []Leg
	Adjusts string // the id of the transaction this one reverses; empty otherwise
}

// MaxLegs bounds a transaction. Every leg becomes an entry row and two counter
// updates in one DynamoDB transaction (spec § 4), whose limit is 100 items.
const MaxLegs = 8

// MaxLegAmount bounds one leg's magnitude (R$ 10 billion, in centavos). With at
// most MaxLegs legs the sum can never wrap int64, so "sums to zero" cannot be
// forged by overflow, and negating a leg (Reverse) is always exact.
const MaxLegAmount billing.Cents = 1e12

// ErrInvalidTransaction wraps every reason a transaction is refused.
var ErrInvalidTransaction = errors.New("invalid ledger transaction")

// NewTransaction builds and validates a transaction. A transaction that does not
// balance is never returned, so a caller cannot persist one.
func NewTransaction(kind TxKind, date brcal.Date, legs ...Leg) (Transaction, error) {
	if !kind.valid() {
		return Transaction{}, fmt.Errorf("%w: unknown kind %q", ErrInvalidTransaction, kind)
	}
	if date.IsZero() {
		return Transaction{}, fmt.Errorf("%w: date is required", ErrInvalidTransaction)
	}
	if len(legs) < 2 || len(legs) > MaxLegs {
		return Transaction{}, fmt.Errorf("%w: %d legs, want 2..%d", ErrInvalidTransaction, len(legs), MaxLegs)
	}
	var sum billing.Cents
	seen := make(map[string]bool, len(legs))
	for _, l := range legs {
		if l.AccountID == "" {
			return Transaction{}, fmt.Errorf("%w: a leg has no account", ErrInvalidTransaction)
		}
		if l.Amount == 0 {
			return Transaction{}, fmt.Errorf("%w: a leg on %s is zero", ErrInvalidTransaction, l.AccountID)
		}
		if l.Amount > MaxLegAmount || l.Amount < -MaxLegAmount {
			return Transaction{}, fmt.Errorf("%w: a leg on %s exceeds the maximum amount", ErrInvalidTransaction, l.AccountID)
		}
		if seen[l.AccountID] {
			return Transaction{}, fmt.Errorf("%w: account %s appears in two legs", ErrInvalidTransaction, l.AccountID)
		}
		seen[l.AccountID] = true
		sum += l.Amount
	}
	if sum != 0 {
		return Transaction{}, fmt.Errorf("%w: legs sum to %d, not zero", ErrInvalidTransaction, sum)
	}
	return Transaction{Kind: kind, Date: date, Legs: append([]Leg(nil), legs...)}, nil
}

// Reverse returns the transaction that undoes original — every leg negated — on
// date, pointing back at originalID. This is Fowler's Reversal Adjustment: both
// the mistake and its correction stay in the record. A replacement adjustment is
// Reverse followed by the corrected transaction; it needs no function of its own.
func Reverse(original Transaction, originalID string, date brcal.Date) (Transaction, error) {
	if originalID == "" {
		return Transaction{}, fmt.Errorf("%w: a reversal must name what it reverses", ErrInvalidTransaction)
	}
	if original.Kind == KindReversal {
		return Transaction{}, fmt.Errorf("%w: a reversal is not reversed; post the original again", ErrInvalidTransaction)
	}
	legs := make([]Leg, len(original.Legs))
	for i, l := range original.Legs {
		legs[i] = Leg{AccountID: l.AccountID, Amount: -l.Amount}
	}
	tx, err := NewTransaction(KindReversal, date, legs...)
	if err != nil {
		return Transaction{}, err
	}
	tx.Adjusts = originalID
	return tx, nil
}

func (k TxKind) valid() bool {
	switch k {
	case KindRecognition, KindSettlement, KindTransfer, KindCardPurchase,
		KindStatementPayment, KindOpeningBalance, KindReversal, KindAdjustment:
		return true
	}
	return false
}
