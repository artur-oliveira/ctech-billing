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
	// Clears is the liability a payable settles instead of the system payables:
	// a card statement bill clears its card (spec § 3.6). Empty for every other bill.
	Clears string
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
			Leg{AccountID: b.CategoryID, Amount: b.Amount}, Leg{AccountID: sys.Payables, Amount: -b.Amount})
	}
	return NewTransaction(KindRecognition, competence,
		Leg{AccountID: sys.Receivables, Amount: b.Amount}, Leg{AccountID: b.CategoryID, Amount: -b.Amount})
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
		cleared, flow := sys.Payables, b.CategoryID
		if b.Clears != "" {
			cleared, flow = b.Clears, CardFlow(b.Clears)
		}
		legs = []Leg{{AccountID: cleared, Amount: b.Amount}, {AccountID: b.AccountID, Amount: -paid, Flow: flow}}
		if diff != 0 {
			legs = append(legs, Leg{AccountID: differenceCategoryID, Amount: diff})
		}
	} else {
		// cash ↑ by what arrived; receivables ↓ by the bill; the gap is a gain
		// (received more) or an expense (received less).
		legs = []Leg{{AccountID: b.AccountID, Amount: paid, Flow: b.CategoryID}, {AccountID: sys.Receivables, Amount: -b.Amount}}
		if diff != 0 {
			legs = append(legs, Leg{AccountID: differenceCategoryID, Amount: -diff})
		}
	}
	return NewTransaction(KindSettlement, date, legs...)
}

// CreditBill takes amount back out of a bill that was recognised and settled,
// as ONE transaction on date: the exact opposite of recognising and settling
// amount, with payables/receivables untouched because the settlement already
// cleared them. It is what a billing credit note against a paid invoice posts
// (spec § 3.8): the issuer's revenue and cash go down, the payer's expense and
// cash out go down. The cash leg keeps the bill's category as its flow, so the
// cash flow shows the money coming back under the line it went out on.
func CreditBill(b BillFacts, amount billing.Cents, date brcal.Date) (Transaction, error) {
	if err := b.validate(); err != nil {
		return Transaction{}, err
	}
	if amount <= 0 || amount > b.Amount {
		return Transaction{}, fmt.Errorf("%w: a credit of %s against a bill of %s", ErrInvalidTransaction, amount, b.Amount)
	}
	if b.Direction == Receivable {
		return NewTransaction(KindAdjustment, date,
			Leg{AccountID: b.CategoryID, Amount: amount}, Leg{AccountID: b.AccountID, Amount: -amount, Flow: b.CategoryID})
	}
	return NewTransaction(KindAdjustment, date,
		Leg{AccountID: b.AccountID, Amount: amount, Flow: b.CategoryID}, Leg{AccountID: b.CategoryID, Amount: -amount})
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
	return NewTransaction(KindTransfer, date, Leg{AccountID: toAccountID, Amount: amount, Flow: FlowNone}, Leg{AccountID: fromAccountID, Amount: -amount, Flow: FlowNone})
}

// CardPurchase records a purchase on a card for its **full** amount on the
// purchase date, however many installments it has (spec § 3.6): the expense
// happened that day. Installments only decide which statement each part of the
// card's balance is billed on (AllocateInstallments).
func CardPurchase(cardAccountID, categoryID string, total billing.Cents, date brcal.Date) (Transaction, error) {
	if total <= 0 {
		return Transaction{}, fmt.Errorf("%w: a purchase must be positive", ErrInvalidTransaction)
	}
	return NewTransaction(KindCardPurchase, date, Leg{AccountID: categoryID, Amount: total}, Leg{AccountID: cardAccountID, Amount: -total})
}

// PayStatement records paying a card statement from an asset account.
func PayStatement(cardAccountID, payingAccountID string, amount billing.Cents, date brcal.Date) (Transaction, error) {
	if amount <= 0 {
		return Transaction{}, fmt.Errorf("%w: a statement payment must be positive", ErrInvalidTransaction)
	}
	return NewTransaction(KindStatementPayment, date, Leg{AccountID: cardAccountID, Amount: amount}, Leg{AccountID: payingAccountID, Amount: -amount})
}

// OpeningBalance records what an account held before the ledger began. amount
// is in the leg convention — positive is a debit — so a checking account with
// R$ 500 is +500, an overdrawn one is negative, and a card owing R$ 300 is -300.
func OpeningBalance(sys SystemAccounts, accountID string, amount billing.Cents, date brcal.Date) (Transaction, error) {
	if amount == 0 {
		return Transaction{}, fmt.Errorf("%w: a zero opening balance is no transaction", ErrInvalidTransaction)
	}
	return NewTransaction(KindOpeningBalance, date, Leg{AccountID: accountID, Amount: amount, Flow: FlowNone}, Leg{AccountID: sys.OpeningBalance, Amount: -amount})
}

// AdjustBill is the net effect of editing a forecast bill's amount or category:
// the new recognition minus the old, per account, as ONE transaction. A reversal
// plus a fresh recognition would say the same thing in two transactions that both
// touch the payables account and the month's summary, and a single atomic write
// cannot update the same item twice. Accounts whose net is zero drop out.
func AdjustBill(sys SystemAccounts, was, next BillFacts, competence brcal.Date) (Transaction, error) {
	wasTx, err := RecognizeBill(sys, was, competence)
	if err != nil {
		return Transaction{}, err
	}
	nextTx, err := RecognizeBill(sys, next, competence)
	if err != nil {
		return Transaction{}, err
	}
	net := map[string]billing.Cents{}
	order := []string{}
	add := func(l Leg, sign billing.Cents) {
		if _, seen := net[l.AccountID]; !seen {
			order = append(order, l.AccountID)
		}
		net[l.AccountID] += sign * l.Amount
	}
	for _, l := range wasTx.Legs {
		add(l, -1)
	}
	for _, l := range nextTx.Legs {
		add(l, 1)
	}
	legs := make([]Leg, 0, len(order))
	for _, acct := range order {
		if net[acct] != 0 {
			legs = append(legs, Leg{AccountID: acct, Amount: net[acct]})
		}
	}
	if len(legs) < 2 {
		return Transaction{}, fmt.Errorf("%w: the edit changes nothing the ledger recognised", ErrInvalidTransaction)
	}
	return NewTransaction(KindAdjustment, competence, legs...)
}

// CancelBill removes a forecast bill's recognition: the negation of its current
// facts' recognition, whatever edits came before, so nothing of it stays in the
// DRE for its competence month.
func CancelBill(sys SystemAccounts, facts BillFacts, competence brcal.Date) (Transaction, error) {
	rec, err := RecognizeBill(sys, facts, competence)
	if err != nil {
		return Transaction{}, err
	}
	legs := make([]Leg, len(rec.Legs))
	for i, l := range rec.Legs {
		legs[i] = Leg{AccountID: l.AccountID, Amount: -l.Amount, Flow: l.Flow}
	}
	return NewTransaction(KindAdjustment, competence, legs...)
}
