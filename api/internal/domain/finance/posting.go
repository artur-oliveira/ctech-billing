package finance

import (
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// Posting rules, after Fowler: one function per kind of fact, and the only code
// allowed to decide which accounts a fact debits and credits (spec § 3.2). A
// handler, a job or the import never assembles legs itself — a second place that
// builds a settlement is a second place for the sign to be wrong.

// SystemAccounts are the ledger accounts every space is created with and nobody
// sees: what is owed, what is due to come in, and the counterpart of an opening
// balance.
type SystemAccounts struct {
	Payables       string // liability
	Receivables    string // asset
	OpeningBalance string // equity
}

// Direction is whether a bill is money going out or coming in.
type Direction string

const (
	Payable    Direction = "payable"
	Receivable Direction = "receivable"
)

// BillFacts is what a posting rule needs to know about a bill. It is not the
// Bill entity (that arrives with persistence); it is the accounting-relevant
// subset, so the rules stay testable without one.
type BillFacts struct {
	Direction  Direction
	Amount     billing.Cents // > 0
	CategoryID string        // income for a receivable, expense for a payable
	AccountID  string        // the asset account it is paid from or into
}

func (b BillFacts) validate() error {
	switch {
	case b.Direction != Payable && b.Direction != Receivable:
		return fmt.Errorf("%w: unknown direction %q", ErrInvalidTransaction, b.Direction)
	case b.Amount <= 0:
		return fmt.Errorf("%w: a bill amount must be positive", ErrInvalidTransaction)
	case b.CategoryID == "" || b.AccountID == "":
		return fmt.Errorf("%w: a bill needs a category and an account", ErrInvalidTransaction)
	}
	return nil
}

// RecognizeBill records that a bill exists, at its competence date: this is what
// puts it in the accrual DRE. Payable: expense ↑ / payables ↑.
// Receivable: receivables ↑ / income ↑.
func RecognizeBill(sys SystemAccounts, b BillFacts, competence brcal.Date) (Transaction, error) {
	if err := b.validate(); err != nil {
		return Transaction{}, err
	}
	if b.Direction == Payable {
		return NewTransaction(KindRecognition, competence,
			Leg{b.CategoryID, b.Amount}, Leg{sys.Payables, -b.Amount})
	}
	return NewTransaction(KindRecognition, competence,
		Leg{sys.Receivables, b.Amount}, Leg{b.CategoryID, -b.Amount})
}

// SettleBill records the cash side, at the payment date: this is what puts it in
// the cash flow. paid may differ from the bill's amount — interest, a fine, a
// discount — and the difference goes to differenceCategoryID, which is required
// only when there is one. The bill's own amount is what clears payables or
// receivables, so a settlement for a different amount still closes the bill.
func SettleBill(sys SystemAccounts, b BillFacts, paid billing.Cents, differenceCategoryID string, date brcal.Date) (Transaction, error) {
	if err := b.validate(); err != nil {
		return Transaction{}, err
	}
	if paid <= 0 {
		return Transaction{}, fmt.Errorf("%w: a settlement must be positive", ErrInvalidTransaction)
	}
	diff := paid - b.Amount
	if diff != 0 && differenceCategoryID == "" {
		return Transaction{}, fmt.Errorf("%w: paid %s against %s needs a category for the difference",
			ErrInvalidTransaction, paid, b.Amount)
	}
	var legs []Leg
	if b.Direction == Payable {
		// payables ↓ by the bill; cash ↓ by what left; the gap is an expense
		// (paid more: interest) or a gain (paid less: discount).
		legs = []Leg{{sys.Payables, b.Amount}, {b.AccountID, -paid}}
		if diff != 0 {
			legs = append(legs, Leg{differenceCategoryID, diff})
		}
	} else {
		// cash ↑ by what arrived; receivables ↓ by the bill; the gap is a gain
		// (received more) or an expense (received less).
		legs = []Leg{{b.AccountID, paid}, {sys.Receivables, -b.Amount}}
		if diff != 0 {
			legs = append(legs, Leg{differenceCategoryID, -diff})
		}
	}
	return NewTransaction(KindSettlement, date, legs...)
}

// Transfer moves money between two of the space's own accounts. It touches no
// category, so it never appears in the DRE.
func Transfer(fromAccountID, toAccountID string, amount billing.Cents, date brcal.Date) (Transaction, error) {
	if amount <= 0 {
		return Transaction{}, fmt.Errorf("%w: a transfer must be positive", ErrInvalidTransaction)
	}
	if fromAccountID == toAccountID {
		return Transaction{}, fmt.Errorf("%w: a transfer needs two different accounts", ErrInvalidTransaction)
	}
	return NewTransaction(KindTransfer, date, Leg{toAccountID, amount}, Leg{fromAccountID, -amount})
}

// CardPurchase records a purchase on a card for its **full** amount on the
// purchase date, however many installments it has (spec § 3.6): the expense
// happened that day. Installments only decide which statement each part of the
// card's balance is billed on (AllocateInstallments).
func CardPurchase(cardAccountID, categoryID string, total billing.Cents, date brcal.Date) (Transaction, error) {
	if total <= 0 {
		return Transaction{}, fmt.Errorf("%w: a purchase must be positive", ErrInvalidTransaction)
	}
	return NewTransaction(KindCardPurchase, date, Leg{categoryID, total}, Leg{cardAccountID, -total})
}

// PayStatement records paying a card statement from an asset account.
func PayStatement(cardAccountID, payingAccountID string, amount billing.Cents, date brcal.Date) (Transaction, error) {
	if amount <= 0 {
		return Transaction{}, fmt.Errorf("%w: a statement payment must be positive", ErrInvalidTransaction)
	}
	return NewTransaction(KindStatementPayment, date, Leg{cardAccountID, amount}, Leg{payingAccountID, -amount})
}

// OpeningBalance records what an account held before the ledger began. amount
// is in the leg convention — positive is a debit — so a checking account with
// R$ 500 is +500, an overdrawn one is negative, and a card owing R$ 300 is -300.
func OpeningBalance(sys SystemAccounts, accountID string, amount billing.Cents, date brcal.Date) (Transaction, error) {
	if amount == 0 {
		return Transaction{}, fmt.Errorf("%w: a zero opening balance is no transaction", ErrInvalidTransaction)
	}
	return NewTransaction(KindOpeningBalance, date, Leg{accountID, amount}, Leg{sys.OpeningBalance, -amount})
}
